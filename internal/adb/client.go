package adb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"adb-fake-location/internal/geo"
	"adb-fake-location/internal/platform"
)

type DeviceInfo struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
	Model  string `json:"model"`
}

type Client struct {
	AdbPath string
	Serial  string
	timeout time.Duration
	// Tests replace only the process boundary; commands still use the real parser.
	run func(context.Context, ...string) (string, error)
}

func NewClient(adbPath, serial string) *Client {
	if adbPath == "" {
		adbPath = "adb"
	}
	return &Client{AdbPath: adbPath, Serial: serial, timeout: 5 * time.Second}
}

// NewClientWithRunner replaces the process boundary for deterministic tests.
// Command construction and parsing stay identical to the real ADB path.
func NewClientWithRunner(adbPath, serial string, runner func(context.Context, ...string) (string, error)) *Client {
	client := NewClient(adbPath, serial)
	client.run = runner
	return client
}

func (c *Client) RunCommand(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	cmdArgs := append([]string(nil), args...)
	if c.Serial != "" {
		cmdArgs = append([]string{"-s", c.Serial}, cmdArgs...)
	}
	if c.run != nil {
		return c.run(ctx, cmdArgs...)
	}
	cmd := exec.CommandContext(ctx, c.AdbPath, cmdArgs...)
	platform.HideCommandWindow(cmd)
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("ADB 命令中断: %w", ctx.Err())
		}
		var exitErr *exec.ExitError
		// Android's help handler can print valid help and still return -1 (ADB: 255).
		// This exception is only for this exact read-only query, never mutations.
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 255 && validLocationHelp(args, out, stderr.String()) {
			return out, nil
		}
		detail := strings.TrimSpace(stderr.String() + "\n" + out)
		return "", fmt.Errorf("ADB 命令 [%s] 失败: %w: %s", strings.Join(args, " "), err, detail)
	}
	// Some vendor commands report errors with a successful process exit code.
	for _, line := range strings.Split(out+"\n"+stderr.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "Exception") || strings.HasPrefix(line, "Unknown command") || strings.Contains(line, "java.lang.SecurityException") {
			return "", errors.New(line)
		}
	}
	return out, nil
}

func validLocationHelp(args []string, out, stderr string) bool {
	if len(args) != 4 || args[0] != "shell" || args[1] != "cmd" || args[2] != "location" || (args[3] != "help" && args[3] != "-h") {
		return false
	}
	if strings.TrimSpace(stderr) != "" || !strings.HasPrefix(out, "Location service commands:") {
		return false
	}
	for _, marker := range []string{"Error:", "Exception", "Permission Denial", "Unknown command"} {
		if strings.Contains(out, marker) {
			return false
		}
	}
	return hasLocationProviderCommands(out)
}

func hasLocationProviderCommands(out string) bool {
	for _, name := range []string{"providers", "add-test-provider", "set-test-provider-enabled", "set-test-provider-location", "remove-test-provider"} {
		if !strings.Contains(out, name) {
			return false
		}
	}
	return true
}

func (c *Client) ListDevices(ctx context.Context) ([]DeviceInfo, error) {
	listClient := *c
	listClient.Serial = ""
	out, err := listClient.RunCommand(ctx, "devices", "-l")
	if err != nil {
		return nil, err
	}
	var devices []DeviceInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := DeviceInfo{Serial: fields[0], State: fields[1]}
		for _, f := range fields[2:] {
			if strings.HasPrefix(f, "model:") {
				d.Model = strings.TrimPrefix(f, "model:")
			}
		}
		devices = append(devices, d)
	}
	return devices, nil
}

func (c *Client) DetectAndSelectTarget(ctx context.Context) (*DeviceInfo, error) {
	devices, err := c.ListDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("无法获取 ADB 设备列表: %w", err)
	}
	if c.Serial == "" {
		if len(devices) == 0 {
			return nil, errors.New("未检测到 Android 设备，请连接手机并开启调试")
		}
		if len(devices) != 1 {
			return nil, errors.New("存在多个设备（包括离线或待授权设备），请使用 --serial 指定目标")
		}
		if devices[0].State != "device" {
			return &devices[0], fmt.Errorf("设备状态为 %s，请检查连接和调试授权", devices[0].State)
		}
		c.Serial = devices[0].Serial
	}
	for _, d := range devices {
		if d.Serial == c.Serial {
			if d.State != "device" {
				return &d, fmt.Errorf("设备 %s 状态为 %s，请检查连接和调试授权", d.Serial, d.State)
			}
			return &d, nil
		}
	}
	return nil, fmt.Errorf("设备 %s 未连接；不会自动切换到其他手机", c.Serial)
}

func (c *Client) CheckSDKVersion(ctx context.Context) (int, error) {
	out, err := c.RunCommand(ctx, "shell", "getprop", "ro.build.version.sdk")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

func (c *Client) CheckLocationCommandSupport(ctx context.Context) (bool, error) {
	out, err := c.RunCommand(ctx, "shell", "cmd", "location", "help")
	if err != nil {
		return false, err
	}
	return hasLocationProviderCommands(out), nil
}

var gpsProviderLine = regexp.MustCompile(`(?mi)^\s*gps provider( \[mock\])?:\s*$`)
var mockModeLine = regexp.MustCompile(`(?mi)^\s*(?:Uid mode:\s*)?(?:android:)?MOCK_LOCATION:\s*(allow|ignore|deny|default|foreground)\b`)

func (c *Client) HasMockGPS(ctx context.Context) (bool, error) {
	out, err := c.RunCommand(ctx, "shell", "dumpsys", "location")
	if err != nil {
		return false, err
	}
	match := gpsProviderLine.FindStringSubmatch(out)
	if match == nil {
		return false, errors.New("无法确认 gps 提供商状态，暂不修改；请检查该设备 dumpsys location 的格式")
	}
	return match[1] != "", nil
}

func (c *Client) ShellUID(ctx context.Context) (string, error) {
	out, err := c.RunCommand(ctx, "shell", "id", "-u")
	if err != nil {
		return "", err
	}
	uid, err := strconv.Atoi(out)
	if err != nil || uid <= 0 {
		return "", errors.New("无法识别普通 ADB shell UID，请使用非 root 的 ADB 连接")
	}
	return out, nil
}

func ValidMockMode(mode string) bool {
	switch mode {
	case "allow", "ignore", "deny", "default", "foreground":
		return true
	}
	return false
}

func (c *Client) GetMockMode(ctx context.Context, uid string) (string, error) {
	out, err := c.RunCommand(ctx, "shell", "cmd", "appops", "get", uid, "android:mock_location")
	if err != nil {
		return "", err
	}
	if match := mockModeLine.FindStringSubmatch(out); match != nil {
		return strings.ToLower(match[1]), nil
	}
	if strings.Contains(out, "No operations.") {
		return "default", nil
	}
	return "", fmt.Errorf("无法读取原模拟位置授权，停止修改: %s", out)
}

func (c *Client) SetMockMode(ctx context.Context, uid, mode string) error {
	if _, err := strconv.ParseUint(uid, 10, 32); err != nil || !ValidMockMode(mode) {
		return errors.New("无效的 AppOps 恢复参数")
	}
	_, err := c.RunCommand(ctx, "shell", "cmd", "appops", "set", uid, "android:mock_location", mode)
	return err
}

func (c *Client) AddTestProvider(ctx context.Context) error {
	// Boolean options are flags; power/accuracy take numeric Criteria values.
	_, err := c.RunCommand(ctx, "shell", "cmd", "location", "providers", "add-test-provider", "gps", "--requiresSatellite", "--supportsAltitude", "--supportsSpeed", "--supportsBearing", "--powerRequirement", "1", "--accuracy", "1")
	return err
}

func (c *Client) EnableTestProvider(ctx context.Context) error {
	_, err := c.RunCommand(ctx, "shell", "cmd", "location", "providers", "set-test-provider-enabled", "gps", "true")
	return err
}

func (c *Client) SetLocation(ctx context.Context, lat, lng float64) error {
	if err := geo.Validate(lat, lng); err != nil {
		return err
	}
	_, err := c.RunCommand(ctx, "shell", "cmd", "location", "providers", "set-test-provider-location", "gps", "--location", fmt.Sprintf("%.8f,%.8f", lat, lng), "--accuracy", "3.0")
	return err
}

func (c *Client) RemoveTestProvider(ctx context.Context) error {
	// Removal restores the real provider; a separate disable command is unnecessary.
	_, err := c.RunCommand(ctx, "shell", "cmd", "location", "providers", "remove-test-provider", "gps")
	return err
}
