package adb

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildFakeADB(t *testing.T) string {
	t.Helper()
	name := "fakeadb"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if output, err := exec.Command("go", "build", "-o", path, "../../testdata/fakeadb").CombinedOutput(); err != nil {
		t.Fatalf("build simulator: %v: %s", err, output)
	}
	return path
}

func TestADBSubprocessTimeoutAndShellError(t *testing.T) {
	path := buildFakeADB(t)
	client := NewClient(path, "")
	if _, err := client.RunCommand(context.Background(), "test-error"); err == nil || !strings.Contains(err.Error(), "simulated shell error") {
		t.Fatalf("zero-exit shell error must fail: %v", err)
	}
	client.timeout = 100 * time.Millisecond
	if _, err := client.RunCommand(context.Background(), "test-timeout"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-nil background context must still time out: %v", err)
	}
}

func TestLocationHelpExit255CompatibilityIsStrictlyReadOnly(t *testing.T) {
	client := NewClient(buildFakeADB(t), "")
	for _, mode := range []string{"255", "incomplete", "denied", "other-exit"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("FAKE_ADB_HELP_MODE", mode)
			supported, err := client.CheckLocationCommandSupport(context.Background())
			if mode == "255" {
				if err != nil || !supported {
					t.Fatalf("valid help with exit 255 must be accepted: %v", err)
				}
				// Same output/exit code must NEVER make a modifying command succeed.
				if err := client.AddTestProvider(context.Background()); err == nil {
					t.Fatal("must not ignore nonzero status for mutations")
				}
			} else if err == nil || supported {
				t.Fatalf("must reject %s help response", mode)
			}
		})
	}
}
