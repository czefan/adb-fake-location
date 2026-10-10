package geo

import (
	"math"
	"testing"
)

func TestValidateCoordinates(t *testing.T) {
	tests := []struct {
		name    string
		lat     float64
		lng     float64
		wantErr bool
	}{
		{"Valid Beijing", 39.9042, 116.4074, false},
		{"Valid Min Bounds", -90.0, -180.0, false},
		{"Valid Max Bounds", 90.0, 180.0, false},
		{"Invalid Lat > 90", 90.0001, 100.0, true},
		{"Invalid Lat < -90", -90.0001, 100.0, true},
		{"Invalid Lng > 180", 30.0, 180.0001, true},
		{"Invalid Lng < -180", 30.0, -180.0001, true},
		{"NaN Lat", math.NaN(), 100.0, true},
		{"Inf Lng", 30.0, math.Inf(1), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.lat, tt.lng)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, %v) error = %v, wantErr %v", tt.lat, tt.lng, err, tt.wantErr)
			}
		})
	}
}

func TestOutOfChina(t *testing.T) {
	// Beijing is inside China
	if OutOfChina(39.9042, 116.4074) {
		t.Errorf("Beijing should be inside China")
	}
	// London is outside China
	if !OutOfChina(51.5074, -0.1278) {
		t.Errorf("London should be outside China")
	}
	// Tokyo is outside China
	if !OutOfChina(35.6762, 139.6503) {
		t.Errorf("Tokyo should be outside China")
	}
}

func TestWGS84AndGCJ02RoundTrip(t *testing.T) {
	// Beijing Tiananmen: approx WGS-84 (39.90403, 116.407526)
	wLat := 39.90403
	wLng := 116.407526

	gLat, gLng := WGS84ToGCJ02(wLat, wLng)
	// GCJ-02 should differ by roughly 100-500 meters in Beijing
	if math.Abs(gLat-wLat) < 0.0001 || math.Abs(gLng-wLng) < 0.0001 {
		t.Errorf("GCJ-02 offset expected, but got almost identical values: (%v, %v)", gLat, gLng)
	}

	// Inverse conversion back to WGS-84
	backLat, backLng := GCJ02ToWGS84(gLat, gLng)
	// Roundtrip difference should be within 1-2 meters (< 0.00005 degrees)
	if math.Abs(backLat-wLat) > 0.00005 || math.Abs(backLng-wLng) > 0.00005 {
		t.Errorf("Roundtrip precision error too large: diff lat=%v, lng=%v", math.Abs(backLat-wLat), math.Abs(backLng-wLng))
	}
}

func TestOutOfChinaNoOffset(t *testing.T) {
	lat := 40.7128 // New York
	lng := -74.0060

	gLat, gLng := WGS84ToGCJ02(lat, lng)
	if gLat != lat || gLng != lng {
		t.Errorf("OutOfChina coordinates should not be offset, got (%v, %v) want (%v, %v)", gLat, gLng, lat, lng)
	}

	wLat, wLng := GCJ02ToWGS84(lat, lng)
	if wLat != lat || wLng != lng {
		t.Errorf("OutOfChina coordinates should not be offset in inverse, got (%v, %v) want (%v, %v)", wLat, wLng, lat, lng)
	}
}

func TestConvertCoordSystems(t *testing.T) {
	// Conversion to WGS84
	lat, lng, err := ToWGS84(39.9, 116.4, WGS84)
	if err != nil || lat != 39.9 || lng != 116.4 {
		t.Errorf("ToWGS84 for WGS84 failed: %v", err)
	}

	_, _, err = ToWGS84(39.9, 116.4, "BD-09")
	if err != ErrUnsupportedCoord {
		t.Errorf("Expected ErrUnsupportedCoord for BD-09, got %v", err)
	}

	// Conversion from WGS84 to GCJ02
	gLat, gLng, err := FromWGS84(39.9, 116.4, GCJ02)
	if err != nil {
		t.Fatalf("FromWGS84 error: %v", err)
	}
	if gLat == 39.9 || gLng == 116.4 {
		t.Errorf("Expected offset for GCJ02, got identical coords")
	}
}

func TestKnownBeijingOffsetAndIterativeInverse(t *testing.T) {
	// Reference coordinate pair, independently of the inverse roundtrip assertion.
	wLat, wLng := 39.908823, 116.397470
	gLat, gLng := WGS84ToGCJ02(wLat, wLng)
	if math.Abs(gLat-39.91022649807321) > 1e-8 || math.Abs(gLng-116.4037135824225) > 1e-8 {
		t.Fatalf("unexpected offset: %.10f %.10f", gLat, gLng)
	}
	backLat, backLng := GCJ02ToWGS84(gLat, gLng)
	if math.Abs(backLat-wLat) > 1e-8 || math.Abs(backLng-wLng) > 1e-8 {
		t.Fatalf("inverse drift: %.10f %.10f", backLat, backLng)
	}
}
