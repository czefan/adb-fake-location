package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"adb-fake-location/internal/platform"
)

type RecoveryRecord struct {
	Version              int          `json:"version"`
	Serial               string       `json:"serial"`
	State                SessionState `json:"state"`
	TargetLat            float64      `json:"target_lat"`
	TargetLng            float64      `json:"target_lng"`
	CoordSys             string       `json:"coord_sys"`
	StartedAt            time.Time    `json:"started_at"`
	AppOpsUID            string       `json:"appops_uid"`
	OriginalMockMode     string       `json:"original_mock_mode"`
	AppOpsMayHaveChanged bool         `json:"appops_may_have_changed"`
	ProviderMayExist     bool         `json:"provider_may_exist"`
}

func getSessionFilePath() string {
	dir, err := platform.DataDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "session.json")
}

func (s *SessionManager) saveRecoveryAtomic(record *RecoveryRecord) error {
	if s.recoveryFile == "" {
		return errors.New("无法确定用户数据目录")
	}
	if err := os.MkdirAll(filepath.Dir(s.recoveryFile), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.recoveryFile), "session-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, s.recoveryFile)
}

func (s *SessionManager) clearRecovery() error {
	err := os.Remove(s.recoveryFile)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *SessionManager) loadRecovery() (*RecoveryRecord, error) {
	if s.recoveryFile == "" {
		return nil, errors.New("无法确定用户数据目录")
	}
	data, err := os.ReadFile(s.recoveryFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rec RecoveryRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("恢复记录损坏，保留文件以便处理: %w", err)
	}
	if rec.Serial == "" {
		return nil, errors.New("恢复记录缺少设备标识，不能自动清理")
	}
	return &rec, nil
}
