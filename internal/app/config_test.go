package app

import (
	"os"
	"path/filepath"
	"testing"

	"adb-fake-location/internal/geo"
)

func TestMissingExplicitConfigDoesNotSilentlyUseDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	if _, err := resolveMapConfig(path, false); !os.IsNotExist(err) {
		t.Fatalf("explicit missing config must report its error: %v", err)
	}
	config, err := resolveMapConfig(path, true)
	if err != nil || config.TileURL == "" {
		t.Fatalf("optional config should use the existing default map: %+v %v", config, err)
	}
}

func TestCustomTileSourceRequiresExplicitCoordinates(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.json")
	data := `{"tile_url":"https://example.com/{z}/{x}/{y}.png","attribution":"Example"}`
	if err := os.WriteFile(file, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadMapConfig(file); err == nil {
		t.Fatal("new tile source must not inherit demo's GCJ-02 assumption")
	}
	if err := os.WriteFile(file, []byte(`{"tile_url":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := loadMapConfig(file)
	if err != nil || config.CoordinateSystem != geo.WGS84 {
		t.Fatalf("coordinate-only config: %+v %v", config, err)
	}
}

func TestMapConfigValidation(t *testing.T) {
	config := defaultMapConfig()
	if err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	config.CoordinateSystem = "BD-09"
	if err := config.Validate(); err == nil {
		t.Fatal("unsupported coordinates accepted")
	}
	config.TileURL = ""
	if err := config.Validate(); err != nil {
		t.Fatal("disabled map must allow coordinate-only mode")
	}
}
