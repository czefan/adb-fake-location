package app

import (
	"context"
	"fmt"
	"io"

	"adb-fake-location/internal/adb"
)

// RunReadOnlyCheck intentionally has no SessionManager, journal or shutdown hook.
// Only queries are allowed here; even exiting must not trigger device cleanup.
func RunReadOnlyCheck(ctx context.Context, client *adb.Client, output io.Writer) error {
	device, err := client.DetectAndSelectTarget(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "设备: %s (%s)\n", device.Model, device.Serial)
	sdk, err := client.CheckSDKVersion(ctx)
	if err != nil {
		return fmt.Errorf("读取系统版本: %w", err)
	}
	fmt.Fprintf(output, "Android SDK: %d\n", sdk)
	supported, err := client.CheckLocationCommandSupport(ctx)
	if err != nil {
		return fmt.Errorf("读取定位命令能力: %w", err)
	}
	if !supported {
		return fmt.Errorf("设备缺少完整的定位提供商命令")
	}
	fmt.Fprintln(output, "定位提供商命令: 可用")
	mock, err := client.HasMockGPS(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "GPS 当前为模拟提供商: %t\n", mock)
	uid, err := client.ShellUID(ctx)
	if err != nil {
		return err
	}
	mode, err := client.GetMockMode(ctx, uid)
	if err != nil {
		return err
	}
	fmt.Fprintf(output, "Shell UID: %s；模拟位置授权模式: %s\n", uid, mode)
	fmt.Fprintln(output, "只读检查完成：未注入坐标、未修改权限或设置、未清理提供商。")
	return nil
}
