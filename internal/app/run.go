package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"adb-fake-location/internal/adb"
	"adb-fake-location/internal/geo"
	"adb-fake-location/internal/platform"
	webassets "adb-fake-location/web"
)

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	platform.HideCommandWindow(cmd)
	if err := cmd.Start(); err != nil {
		log.Printf("未能自动打开浏览器，请手动访问 %s: %v", url, err)
		return
	}
	go func() { _ = cmd.Wait() }()
}

func Run(args []string) error {
	flags := flag.NewFlagSet("adb-fake-location", flag.ContinueOnError)
	serial := flags.String("serial", "", "指定目标 ADB 设备序列号或 IP:端口")
	port := flags.Int("port", 8080, "本地 HTTP 服务端口")
	adbPath := flags.String("adb-path", "adb", "自定义 ADB 可执行文件路径")
	noBrowser := flags.Bool("no-browser", false, "启动后不自动打开浏览器")
	configPath := flags.String("config", "", "地图配置文件路径；默认使用用户数据目录的 config.json")
	checkOnly := flags.Bool("check", false, "只读检查设备能力，不启动服务、不修改或恢复手机状态")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *checkOnly {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return RunReadOnlyCheck(ctx, adb.NewClient(*adbPath, *serial), os.Stdout)
	}
	if *port < 1 || *port > 65535 {
		return fmt.Errorf("端口必须在 1–65535 之间")
	}
	dir, err := platform.DataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	release, err := platform.AcquireInstanceLock(filepath.Join(dir, "instance.lock"))
	if err != nil {
		return err
	}
	defer release()
	optionalConfig := *configPath == ""
	if optionalConfig {
		*configPath = filepath.Join(dir, "config.json")
	}
	config, configErr := resolveMapConfig(*configPath, optionalConfig)
	if configErr != nil {
		log.Printf("%v；以坐标输入模式启动", configErr)
		config = MapConfig{CoordinateSystem: geo.WGS84}
	}
	client := adb.NewClient(*adbPath, *serial)
	sm := NewSessionManager(client)
	sm.mapCoordSys = config.CoordinateSystem
	staticFS, err := webassets.Files()
	if err != nil {
		return err
	}
	server := NewServer(sm, staticFS)
	server.mapConfig = config
	if configErr != nil {
		server.configMessage = configErr.Error()
	}
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("无法监听 %s，请关闭占用端口的程序，或指定 --port: %w", addr, err)
	}
	defer listener.Close()
	httpServer := &http.Server{Handler: server.Routes(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: time.Minute}
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Serve(listener) }()
	log.Printf("服务已监听 http://%s；数据目录: %s", addr, dir)
	if !*noBrowser {
		openBrowser("http://" + addr)
	}
	var serveErr error
	select {
	case <-signals.Done():
	case serveErr = <-serveDone:
	}
	// Stop accepting new work and wait for any in-flight lifecycle request first.
	shutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), 35*time.Second)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		_ = httpServer.Close()
	}
	cancelHTTP()
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()
	if err := sm.Shutdown(cleanupCtx); err != nil {
		return fmt.Errorf("退出时未完成恢复，记录已保留；重新连接后执行清理: %w", err)
	}
	log.Println("工具已停止；已清理的模拟定位需等待手机重新获取真实位置。")
	if serveErr != nil && serveErr != http.ErrServerClosed {
		return serveErr
	}
	return nil
}
