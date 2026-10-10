package app

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadOnlyCheckDoesNotTouchRecoveryOrDevice(t *testing.T) {
	s, fake := newTestSession(t)
	original := []byte("existing recovery record must not be read or modified by diagnostics")
	if err := os.WriteFile(s.recoveryFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := RunReadOnlyCheck(context.Background(), s.adbClient, &output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.recoveryFile)
	if err != nil || !bytes.Equal(data, original) {
		t.Fatal("diagnostics changed recovery file")
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, cmd := range fake.calls {
		if strings.Contains(cmd, "appops set") || strings.Contains(cmd, " providers ") {
			t.Fatalf("diagnostics sent mutation: %s", cmd)
		}
	}
	if fake.mock || fake.mode != "ignore" {
		t.Fatal("diagnostics changed device")
	}
}
