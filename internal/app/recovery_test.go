package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"adb-fake-location/internal/adb"
	"adb-fake-location/internal/geo"
)

type fakeADB struct {
	mu           sync.Mutex
	mode         string
	mock         bool
	devices      string
	calls        []string
	hook         func(context.Context, string) error
	recoveryFile string
}

func newTestSession(t *testing.T) (*SessionManager, *fakeADB) {
	t.Helper()
	f := &fakeADB{mode: "ignore", devices: "List of devices attached\ndevice-a device model:Pixel\n"}
	client := adb.NewClientWithRunner("unused-test-adb", "", f.run)
	s := NewSessionManager(client)
	s.recoveryFile = filepath.Join(t.TempDir(), "session.json")
	s.interval = time.Hour
	f.recoveryFile = s.recoveryFile
	t.Cleanup(func() {
		f.setHook(nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Stop(ctx)
	})
	return s, f
}

func (f *fakeADB) setHook(hook func(context.Context, string) error) {
	f.mu.Lock()
	f.hook = hook
	f.mu.Unlock()
}

func (f *fakeADB) run(ctx context.Context, args ...string) (string, error) {
	command := strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, command)
	hook := f.hook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, command); err != nil {
			return "", err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if command == "devices -l" {
		return f.devices, nil
	}
	if len(args) < 3 || args[0] != "-s" || args[1] != "device-a" {
		return "", fmt.Errorf("wrong device or missing serial: %s", command)
	}
	command = strings.Join(args[2:], " ")
	switch command {
	case "shell getprop ro.build.version.sdk":
		return "36", nil
	case "shell cmd location help":
		return "providers add-test-provider set-test-provider-enabled set-test-provider-location remove-test-provider", nil
	case "shell cmd location is-location-enabled":
		return "true", nil
	case "shell id -u":
		return "2000", nil
	case "shell dumpsys location":
		if f.mock {
			return "  gps provider [mock]:\n    allowed=true\n", nil
		}
		return "  gps provider:\n    allowed=true\n", nil
	case "shell cmd appops get 2000 android:mock_location":
		if f.mode == "default" {
			return "No operations.\nDefault mode: ignore", nil
		}
		return "Uid mode: MOCK_LOCATION: " + f.mode, nil
	}
	var rec RecoveryRecord
	data, err := os.ReadFile(f.recoveryFile)
	if err != nil {
		return "", fmt.Errorf("mutation without recovery file: %s", command)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", err
	}
	if strings.HasPrefix(command, "shell cmd appops set 2000 android:mock_location ") {
		if !rec.AppOpsMayHaveChanged {
			return "", errors.New("permission mutation without persisted intent")
		}
		f.mode = args[len(args)-1]
		return "", nil
	}
	switch {
	case strings.Contains(command, " add-test-provider "):
		want := "shell cmd location providers add-test-provider gps --requiresSatellite --supportsAltitude --supportsSpeed --supportsBearing --powerRequirement 1 --accuracy 1"
		if command != want {
			return "", fmt.Errorf("invalid AOSP provider syntax: %s", command)
		}
		if !rec.ProviderMayExist {
			return "", errors.New("provider mutation without persisted intent")
		}
		f.mock = true
	case strings.Contains(command, " set-test-provider-enabled "):
		if !f.mock {
			return "", errors.New("provider absent")
		}
	case strings.Contains(command, " set-test-provider-location "):
		if !f.mock {
			return "", errors.New("injection after cleanup")
		}
	case strings.Contains(command, " remove-test-provider "):
		f.mock = false
	default:
		return "", fmt.Errorf("unexpected command: %s", command)
	}
	return "", nil
}

func startTestSession(t *testing.T, s *SessionManager) {
	t.Helper()
	if err := s.Start(context.Background(), 39.9, 116.4, geo.WGS84); err != nil {
		t.Fatal(err)
	}
}

func requireRecovery(t *testing.T, s *SessionManager) *RecoveryRecord {
	t.Helper()
	rec, err := s.loadRecovery()
	if err != nil || rec == nil {
		t.Fatalf("recovery must remain: rec=%+v err=%v", rec, err)
	}
	return rec
}

func TestRunStopRestoresPermissionAndMapCoordinates(t *testing.T) {
	s, f := newTestSession(t)
	s.mapCoordSys = geo.GCJ02
	startTestSession(t, s)
	status := s.GetStatus()
	if status.State != StateRunning || status.PendingRecovery {
		t.Fatalf("running session is not pending recovery: %+v", status)
	}
	gLat, gLng := geo.WGS84ToGCJ02(39.9, 116.4)
	if math.Abs(status.DisplayLat-gLat) > 1e-9 || math.Abs(status.DisplayLng-gLng) > 1e-9 {
		t.Fatalf("WGS input must display in map coordinates: %+v", status)
	}
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("recover must not race a running worker")
	}
	requireRecovery(t, s)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec, err := s.loadRecovery(); err != nil || rec != nil {
		t.Fatalf("cleanup should delete recovery: %v %v", rec, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mock || f.mode != "ignore" {
		t.Fatalf("device not restored: mock=%v mode=%s", f.mock, f.mode)
	}
}

func TestFailedSetupCanBeRecovered(t *testing.T) {
	s, f := newTestSession(t)
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.Contains(cmd, "set-test-provider-enabled") {
			return errors.New("device disconnected")
		}
		return nil
	})
	if err := s.Start(context.Background(), 0, 0, geo.WGS84); err == nil {
		t.Fatal("setup should fail")
	}
	if s.GetStatus().State != StatePendingCleanup {
		t.Fatal("partial setup needs cleanup")
	}
	requireRecovery(t, s)
	f.setHook(nil)
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCleanupSurvivesRestartAndUsesOriginalDevice(t *testing.T) {
	s, f := newTestSession(t)
	startTestSession(t, s)
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.Contains(cmd, "remove-test-provider") {
			return errors.New("offline")
		}
		return nil
	})
	if err := s.Stop(context.Background()); err == nil {
		t.Fatal("cleanup should fail")
	}
	requireRecovery(t, s)
	if !s.GetStatus().PendingRecovery {
		t.Fatal("must expose pending recovery")
	}
	client := adb.NewClientWithRunner("unused-test-adb", "other-device", f.run)
	restarted := NewSessionManager(client)
	restarted.recoveryFile = s.recoveryFile
	if err := restarted.Start(context.Background(), 1, 2, geo.WGS84); err == nil {
		t.Fatal("start must inspect recovery before any status poll")
	}
	requireRecovery(t, restarted)
	f.setHook(nil)
	if err := restarted.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFailureRetainsProgress(t *testing.T) {
	s, f := newTestSession(t)
	startTestSession(t, s)
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.HasSuffix(cmd, "android:mock_location ignore") {
			return errors.New("restore failed")
		}
		return nil
	})
	if err := s.Stop(context.Background()); err == nil {
		t.Fatal("restore should fail")
	}
	rec := requireRecovery(t, s)
	if rec.ProviderMayExist || !rec.AppOpsMayHaveChanged {
		t.Fatalf("must persist cleanup progress: %+v", rec)
	}
	f.setHook(nil)
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStopCancelsInFlightUpdateBeforeRemovingProvider(t *testing.T) {
	s, f := newTestSession(t)
	startTestSession(t, s)
	injectionStarted := make(chan struct{})
	injectionEnded := make(chan struct{})
	var once sync.Once
	f.setHook(func(ctx context.Context, cmd string) error {
		if strings.Contains(cmd, "set-test-provider-location") {
			once.Do(func() { close(injectionStarted) })
			<-ctx.Done()
			close(injectionEnded)
			return ctx.Err()
		}
		if strings.Contains(cmd, "remove-test-provider") {
			select {
			case <-injectionEnded:
			default:
				return errors.New("removed provider before injection ended")
			}
		}
		return nil
	})
	if err := s.UpdateLocation(1, 2, geo.WGS84); err != nil {
		t.Fatal(err)
	}
	select {
	case <-injectionStarted:
	case <-time.After(time.Second):
		t.Fatal("update never started")
	}
	for i := 0; i < 20; i++ {
		if err := s.UpdateLocation(2, float64(i), geo.WGS84); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptRecoveryPreventsMutation(t *testing.T) {
	s, f := newTestSession(t)
	if err := os.WriteFile(s.recoveryFile, []byte("{bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background(), 0, 0, geo.WGS84); err == nil {
		t.Fatal("corrupt recovery must block start")
	}
	f.mu.Lock()
	if len(f.calls) != 0 {
		t.Fatal("must not contact device with corrupt recovery")
	}
	f.mu.Unlock()
	if err := s.Recover(context.Background()); err == nil {
		t.Fatal("corrupt recovery cannot be discarded")
	}
	data, _ := os.ReadFile(s.recoveryFile)
	if string(data) != "{bad" {
		t.Fatal("corrupt recovery was overwritten")
	}
}

func TestRecoveryWriteFailureDoesNotChangeDevice(t *testing.T) {
	s, f := newTestSession(t)
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.Contains(cmd, "appops get") {
			return os.Mkdir(s.recoveryFile, 0700)
		}
		return nil
	})
	if err := s.Start(context.Background(), 1, 2, geo.WGS84); err == nil {
		t.Fatal("writing recovery over a directory must fail")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mock || f.mode != "ignore" {
		t.Fatal("device was modified despite journal write failure")
	}
}

func TestStopDeadlineDoesNotRemoveProviderWhileWorkerIsAlive(t *testing.T) {
	s, f := newTestSession(t)
	startTestSession(t, s)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.Contains(cmd, "set-test-provider-location") {
			close(started)
			<-release // Simulate a slow subprocess that has not finished cancellation.
			return context.Canceled
		}
		return nil
	})
	if err := s.UpdateLocation(1, 2, geo.WGS84); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout: %v", err)
	}
	requireRecovery(t, s)
	f.mu.Lock()
	for _, call := range f.calls {
		if strings.Contains(call, "remove-test-provider") {
			t.Fatal("cleanup ran before worker terminated")
		}
	}
	f.mu.Unlock()
	once.Do(func() { close(release) })
	if err := s.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerFailureRemainsPending(t *testing.T) {
	s, f := newTestSession(t)
	startTestSession(t, s)
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.Contains(cmd, "set-test-provider-location") {
			return errors.New("offline")
		}
		return nil
	})
	if err := s.UpdateLocation(0, 0, geo.WGS84); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	done := s.workerDone
	s.mu.RUnlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on failure")
	}
	if s.RefreshStatus(context.Background()).State != StatePendingCleanup {
		t.Fatal("polling must not reset failed session")
	}
	requireRecovery(t, s)
}

func TestExistingMockProviderIsNotReplaced(t *testing.T) {
	s, f := newTestSession(t)
	f.mock = true
	if err := s.Start(context.Background(), 1, 2, geo.WGS84); err == nil {
		t.Fatal("foreign mock provider must block start")
	}
	if rec, _ := s.loadRecovery(); rec != nil {
		t.Fatal("no mutations should be recorded")
	}
}

func TestADBRefusesAmbiguousSelectionAndUsesCapabilityProbe(t *testing.T) {
	s, f := newTestSession(t)
	f.devices += "device-b offline\n"
	if err := s.Start(context.Background(), 1, 2, geo.WGS84); err == nil {
		t.Fatal("one online + one offline is ambiguous")
	}
	f.mu.Lock()
	f.devices = "device-a device\n"
	f.mu.Unlock()
	f.setHook(func(_ context.Context, cmd string) error {
		if strings.HasSuffix(cmd, "location help") {
			return errors.New("unsupported")
		}
		return nil
	})
	if s.RefreshStatus(context.Background()).State != StateUnavailable {
		t.Fatal("version alone must not imply ready")
	}
}

func TestIdleProbeCacheDoesNotBypassStartValidation(t *testing.T) {
	s, f := newTestSession(t)
	if s.RefreshStatus(context.Background()).State != StateReady {
		t.Fatal("expected ready")
	}
	f.mu.Lock()
	firstCalls := len(f.calls)
	f.mu.Unlock()
	for i := 0; i < 10; i++ {
		s.RefreshStatus(context.Background())
	}
	f.mu.Lock()
	laterCalls := len(f.calls)
	f.mu.Unlock()
	if laterCalls != firstCalls {
		t.Fatalf("idle polls repeated ADB probes: %d -> %d", firstCalls, laterCalls)
	}
	f.setHook(func(_ context.Context, command string) error {
		if command == "devices -l" {
			return errors.New("device disconnected")
		}
		return nil
	})
	if err := s.Start(context.Background(), 1, 2, geo.WGS84); err == nil {
		t.Fatal("start must recheck the device even with a cached ready state")
	}
}
