package platform

import "testing"

func TestInstanceLockLifecycle(t *testing.T) {
	path := t.TempDir() + "/instance.lock"
	release, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if extra, err := AcquireInstanceLock(path); err == nil {
		extra()
		t.Fatal("second instance acquired same lock")
	}
	release()
	again, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatal(err)
	}
	again()
}
