package platform

import (
	"os"
	"path/filepath"
)

func DataDir() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "adb-fake-location"), nil
}
