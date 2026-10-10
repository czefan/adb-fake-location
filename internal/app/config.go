package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"adb-fake-location/internal/geo"
)

type MapConfig struct {
	TileURL          string `json:"tile_url"`
	CoordinateSystem string `json:"coordinate_system"`
	Attribution      string `json:"attribution"`
	MinZoom          int    `json:"min_zoom"`
	MaxZoom          int    `json:"max_zoom"`
	Subdomains       string `json:"subdomains"`
}

func defaultMapConfig() MapConfig {
	// Preserve the demo's source, but allow replacement or disabling without a rebuild.
	return MapConfig{
		TileURL:          "https://wprd0{s}.is.autonavi.com/appmaptile?x={x}&y={y}&z={z}&lang=zh_cn&size=1&scale=1&style=7",
		CoordinateSystem: geo.GCJ02,
		Attribution:      "© AutoNavi / 高德地图",
		MinZoom:          3, MaxZoom: 18, Subdomains: "1234",
	}
}

func loadMapConfig(path string) (MapConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return MapConfig{}, err
	}
	config := MapConfig{MinZoom: 3, MaxZoom: 18, Subdomains: "abc"}
	if strings.TrimSpace(string(data)) == "null" {
		return MapConfig{}, errors.New("地图配置应为 JSON 对象")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return MapConfig{}, fmt.Errorf("地图配置解析失败: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return MapConfig{}, errors.New("地图配置只能包含一个 JSON 对象")
	}
	if err := config.Validate(); err != nil {
		return MapConfig{}, err
	}
	if config.TileURL == "" && config.CoordinateSystem == "" {
		config.CoordinateSystem = geo.WGS84
	}
	return config, nil
}

// Missing optional user configuration uses defaults; an explicit path must exist.
func resolveMapConfig(path string, optional bool) (MapConfig, error) {
	config, err := loadMapConfig(path)
	if optional && os.IsNotExist(err) {
		return defaultMapConfig(), nil
	}
	return config, err
}

func (c MapConfig) Validate() error {
	if c.TileURL == "" {
		return nil
	}
	if c.CoordinateSystem != geo.WGS84 && c.CoordinateSystem != geo.GCJ02 {
		return errors.New("地图坐标系必须明确为 WGS-84 或 GCJ-02")
	}
	if c.MinZoom < 0 || c.MaxZoom > 22 || c.MaxZoom < c.MinZoom {
		return errors.New("地图缩放级别应在 0–22 之间且 min_zoom 不大于 max_zoom")
	}
	for _, field := range []string{"{x}", "{y}", "{z}"} {
		if !strings.Contains(c.TileURL, field) {
			return fmt.Errorf("瓦片地址缺少 %s", field)
		}
	}
	probe := strings.NewReplacer("{s}", "1", "{x}", "0", "{y}", "0", "{z}", "0").Replace(c.TileURL)
	u, err := url.Parse(probe)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return errors.New("瓦片地址必须是有效的 HTTPS XYZ 地址")
	}
	if strings.TrimSpace(c.Attribution) == "" {
		return errors.New("请配置地图来源署名 attribution")
	}
	return nil
}
