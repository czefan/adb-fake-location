package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"adb-fake-location/internal/adb"
	"adb-fake-location/internal/geo"
)

type SessionState string

const (
	StateUnavailable    SessionState = "不可用"
	StateReady          SessionState = "就绪"
	StateStarting       SessionState = "启动中"
	StateRunning        SessionState = "模拟中"
	StateStopping       SessionState = "停止中"
	StatePendingCleanup SessionState = "待清理"
)

var ErrSessionBusy = errors.New("另一个会话操作正在执行，请稍后重试")

type SessionStatus struct {
	State           SessionState    `json:"state"`
	Device          *adb.DeviceInfo `json:"device,omitempty"`
	TargetLat       float64         `json:"target_lat"`
	TargetLng       float64         `json:"target_lng"`
	DisplayLat      float64         `json:"display_lat"`
	DisplayLng      float64         `json:"display_lng"`
	CoordSys        string          `json:"coord_sys"`
	LastSuccessTime *time.Time      `json:"last_success_time,omitempty"`
	ErrorMessage    string          `json:"error_message,omitempty"`
	PendingRecovery bool            `json:"pending_recovery"`
}

type SessionManager struct {
	// opMu serializes lifecycle operations. mu protects short in-memory updates only.
	opMu                 sync.Mutex
	nextDeviceCheck      time.Time // Protected by opMu; cache successful idle probes briefly.
	mu                   sync.RWMutex
	adbClient            *adb.Client
	state                SessionState
	currentDevice        *adb.DeviceInfo
	targetLat, targetLng float64
	sentLat, sentLng     float64
	inputCoordSys        string
	mapCoordSys          string
	lastSuccessTime      *time.Time
	errorMessage         string
	recoveryFile         string
	cancelWorker         context.CancelFunc
	workerDone           chan struct{}
	updates              chan struct{}
	interval             time.Duration
}

func NewSessionManager(client *adb.Client) *SessionManager {
	return &SessionManager{adbClient: client, state: StateUnavailable, recoveryFile: getSessionFilePath(), mapCoordSys: geo.WGS84, interval: time.Second}
}

func (s *SessionManager) setError(state SessionState, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if err != nil {
		s.errorMessage = err.Error()
	} else {
		s.errorMessage = ""
	}
}

func (s *SessionManager) checkDevice(ctx context.Context) error {
	device, err := s.adbClient.DetectAndSelectTarget(ctx)
	s.mu.Lock()
	s.currentDevice = device
	s.mu.Unlock()
	if err != nil {
		return err
	}
	supported, err := s.adbClient.CheckLocationCommandSupport(ctx)
	if err != nil {
		return err
	}
	if !supported {
		return errors.New("设备缺少完整的 cmd location providers 命令，无法使用此方案")
	}
	return nil
}

func (s *SessionManager) RefreshStatus(ctx context.Context) SessionStatus {
	if !s.opMu.TryLock() {
		return s.GetStatus()
	}
	defer s.opMu.Unlock()
	state := s.GetStatus().State
	if state == StateRunning || state == StateStarting || state == StateStopping || state == StatePendingCleanup {
		return s.GetStatus()
	}
	rec, err := s.loadRecovery()
	if err != nil || rec != nil {
		if err == nil {
			err = fmt.Errorf("发现设备 %s 的遗留会话，请先清理恢复", rec.Serial)
		}
		s.setError(StatePendingCleanup, err)
		return s.GetStatus()
	}
	if state == StateReady && time.Now().Before(s.nextDeviceCheck) {
		return s.GetStatus()
	}
	if err := s.checkDevice(ctx); err != nil {
		s.setError(StateUnavailable, err)
	} else {
		s.setError(StateReady, nil)
		s.nextDeviceCheck = time.Now().Add(3 * time.Second)
	}
	return s.GetStatus()
}

func (s *SessionManager) GetStatus() SessionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var device *adb.DeviceInfo
	if s.currentDevice != nil {
		d := *s.currentDevice
		device = &d
	}
	var last *time.Time
	if s.lastSuccessTime != nil {
		t := *s.lastSuccessTime
		last = &t
	}
	lat, lng := s.targetLat, s.targetLng
	if last != nil {
		lat, lng = s.sentLat, s.sentLng
	}
	displayLat, displayLng, _ := geo.FromWGS84(lat, lng, s.mapCoordSys)
	return SessionStatus{State: s.state, Device: device, TargetLat: lat, TargetLng: lng, DisplayLat: displayLat, DisplayLng: displayLng, CoordSys: s.inputCoordSys, LastSuccessTime: last, ErrorMessage: s.errorMessage, PendingRecovery: s.state == StatePendingCleanup}
}

func (s *SessionManager) Start(ctx context.Context, lat, lng float64, coordSys string) error {
	wLat, wLng, err := geo.ToWGS84(lat, lng, coordSys)
	if err != nil {
		return err
	}
	if !s.opMu.TryLock() {
		return ErrSessionBusy
	}
	defer s.opMu.Unlock()
	state := s.GetStatus().State
	if state == StateRunning || state == StateStarting || state == StateStopping {
		return errors.New("会话正在运行或切换状态")
	}
	// Check disk even when Start is the first request after a process restart.
	rec, err := s.loadRecovery()
	if err != nil || rec != nil || state == StatePendingCleanup {
		if err == nil {
			err = errors.New("存在待清理会话，请先执行恢复")
		}
		s.setError(StatePendingCleanup, err)
		return err
	}
	s.setError(StateStarting, nil)
	preflight := func() error {
		if err := s.checkDevice(ctx); err != nil {
			return err
		}
		enabled, err := s.adbClient.RunCommand(ctx, "shell", "cmd", "location", "is-location-enabled")
		if err != nil {
			return err
		}
		if enabled != "true" {
			return errors.New("请先在手机上开启系统定位服务")
		}
		mock, err := s.adbClient.HasMockGPS(ctx)
		if err != nil {
			return err
		}
		if mock {
			return errors.New("gps 已由其他模拟会话占用，请先停止原会话")
		}
		return nil
	}
	if err := preflight(); err != nil {
		s.setError(StateUnavailable, err)
		return err
	}
	uid, err := s.adbClient.ShellUID(ctx)
	if err != nil {
		s.setError(StateUnavailable, err)
		return err
	}
	mode, err := s.adbClient.GetMockMode(ctx, uid)
	if err != nil {
		s.setError(StateUnavailable, err)
		return err
	}
	rec = &RecoveryRecord{Version: 1, Serial: s.adbClient.Serial, State: StateStarting, TargetLat: wLat, TargetLng: wLng, CoordSys: coordSys, StartedAt: time.Now(), AppOpsUID: uid, OriginalMockMode: mode, AppOpsMayHaveChanged: mode != "allow"}
	if err := s.saveRecoveryAtomic(rec); err != nil {
		s.setError(StateUnavailable, err)
		return err
	}
	fail := func(err error) error { s.setError(StatePendingCleanup, err); return err }
	if rec.AppOpsMayHaveChanged {
		if err := s.adbClient.SetMockMode(ctx, uid, "allow"); err != nil {
			return fail(err)
		}
	}
	currentMode, err := s.adbClient.GetMockMode(ctx, uid)
	if err != nil {
		return fail(err)
	}
	if currentMode != "allow" {
		return fail(errors.New("模拟位置授权未生效"))
	}
	// Persist intent before commands: even a timeout may have changed the device.
	rec.ProviderMayExist = true
	if err := s.saveRecoveryAtomic(rec); err != nil {
		return fail(err)
	}
	if err := s.adbClient.AddTestProvider(ctx); err != nil {
		return fail(err)
	}
	if err := s.adbClient.EnableTestProvider(ctx); err != nil {
		return fail(err)
	}
	mock, err := s.adbClient.HasMockGPS(ctx)
	if err != nil {
		return fail(err)
	}
	if !mock {
		return fail(errors.New("命令已返回，但 gps 测试提供商未生效"))
	}
	if err := s.adbClient.SetLocation(ctx, wLat, wLng); err != nil {
		return fail(err)
	}
	rec.State = StateRunning
	if err := s.saveRecoveryAtomic(rec); err != nil {
		return fail(err)
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	done, updates := make(chan struct{}), make(chan struct{}, 1)
	s.mu.Lock()
	s.targetLat, s.targetLng, s.sentLat, s.sentLng = wLat, wLng, wLat, wLng
	s.inputCoordSys = coordSys
	now := time.Now()
	s.lastSuccessTime = &now
	s.state, s.errorMessage = StateRunning, ""
	s.cancelWorker, s.workerDone, s.updates = cancel, done, updates
	s.mu.Unlock()
	go s.runWorkerLoop(workerCtx, done, updates)
	return nil
}

func (s *SessionManager) UpdateLocation(lat, lng float64, coordSys string) error {
	wLat, wLng, err := geo.ToWGS84(lat, lng, coordSys)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != StateRunning {
		return errors.New("当前未在模拟中，无法更新坐标")
	}
	s.targetLat, s.targetLng, s.inputCoordSys = wLat, wLng, coordSys
	// Coalesce changes; only the single worker can inject, never API goroutines.
	select {
	case s.updates <- struct{}{}:
	default:
	}
	return nil
}

func (s *SessionManager) runWorkerLoop(ctx context.Context, done chan struct{}, updates <-chan struct{}) {
	defer close(done)
	timer := time.NewTimer(s.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-updates:
		}
		if ctx.Err() != nil {
			return
		}
		s.mu.RLock()
		lat, lng, state := s.targetLat, s.targetLng, s.state
		s.mu.RUnlock()
		if state != StateRunning {
			return
		}
		err := s.adbClient.SetLocation(ctx, lat, lng)
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if s.state != StateRunning {
			s.mu.Unlock()
			return
		}
		if err != nil {
			s.state, s.errorMessage = StatePendingCleanup, fmt.Sprintf("定位注入失败，保留恢复记录: %v", err)
			s.mu.Unlock()
			return
		}
		now := time.Now()
		s.lastSuccessTime, s.sentLat, s.sentLng = &now, lat, lng
		s.mu.Unlock()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(s.interval)
	}
}

func (s *SessionManager) Stop(ctx context.Context) error {
	if !s.opMu.TryLock() {
		return ErrSessionBusy
	}
	defer s.opMu.Unlock()
	return s.stopLocked(ctx)
}

func (s *SessionManager) stopLocked(ctx context.Context) error {
	s.mu.Lock()
	if s.cancelWorker != nil {
		s.cancelWorker()
	}
	done := s.workerDone
	s.state = StateStopping
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			s.setError(StatePendingCleanup, ctx.Err())
			return ctx.Err()
		}
	}
	s.mu.Lock()
	s.cancelWorker, s.workerDone, s.updates = nil, nil, nil
	s.mu.Unlock()
	rec, err := s.loadRecovery()
	if err != nil {
		s.setError(StatePendingCleanup, err)
		return err
	}
	if rec == nil {
		s.setError(StateUnavailable, nil)
		return nil
	}
	fail := func(err error) error { s.setError(StatePendingCleanup, err); return err }
	if rec.Version != 1 || !adb.ValidMockMode(rec.OriginalMockMode) {
		return fail(errors.New("旧版恢复记录缺少原授权状态，不能猜测还原；请按 README 的旧版记录步骤处理"))
	}
	if _, err := strconv.ParseUint(rec.AppOpsUID, 10, 32); err != nil {
		return fail(errors.New("恢复记录中的 shell UID 无效"))
	}
	// Always recover the recorded device, never an unrelated --serial target.
	recoveryClient := *s.adbClient
	recoveryClient.Serial = rec.Serial
	device, err := recoveryClient.DetectAndSelectTarget(ctx)
	if err != nil {
		return fail(err)
	}
	s.mu.Lock()
	s.currentDevice = device
	s.mu.Unlock()
	if rec.ProviderMayExist {
		mock, err := recoveryClient.HasMockGPS(ctx)
		if err != nil {
			return fail(err)
		}
		if mock {
			if err := recoveryClient.RemoveTestProvider(ctx); err != nil {
				return fail(err)
			}
			mock, err = recoveryClient.HasMockGPS(ctx)
			if err != nil {
				return fail(err)
			}
			if mock {
				return fail(errors.New("测试提供商仍存在，保留恢复记录供重试"))
			}
		}
		rec.ProviderMayExist = false
		if err := s.saveRecoveryAtomic(rec); err != nil {
			return fail(err)
		}
	}
	if rec.AppOpsMayHaveChanged {
		if err := recoveryClient.SetMockMode(ctx, rec.AppOpsUID, rec.OriginalMockMode); err != nil {
			return fail(err)
		}
		mode, err := recoveryClient.GetMockMode(ctx, rec.AppOpsUID)
		if err != nil {
			return fail(err)
		}
		if mode != rec.OriginalMockMode {
			return fail(errors.New("原模拟位置授权尚未恢复，保留恢复记录"))
		}
		rec.AppOpsMayHaveChanged = false
		if err := s.saveRecoveryAtomic(rec); err != nil {
			return fail(err)
		}
	}
	if err := s.clearRecovery(); err != nil {
		return fail(err)
	}
	s.mu.Lock()
	s.state, s.errorMessage = StateUnavailable, ""
	s.targetLat, s.targetLng, s.sentLat, s.sentLng = 0, 0, 0, 0
	s.lastSuccessTime = nil
	s.mu.Unlock()
	return nil
}

func (s *SessionManager) Recover(ctx context.Context) error {
	if !s.opMu.TryLock() {
		return ErrSessionBusy
	}
	defer s.opMu.Unlock()
	if s.GetStatus().State == StateRunning {
		return errors.New("会话仍在运行，请使用停止模拟")
	}
	return s.stopLocked(ctx)
}

// Shutdown waits for an in-progress start/stop, then cleans up before exiting.
func (s *SessionManager) Shutdown(ctx context.Context) error {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	return s.stopLocked(ctx)
}
