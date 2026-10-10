package geo

import (
	"errors"
	"math"
)

// 支持的坐标系常量定义
const (
	WGS84 = "WGS-84"
	GCJ02 = "GCJ-02"
)

// 椭球参数 (克拉索夫斯基椭球 / GCJ-02 算法常用参数)
const (
	a  = 6378245.0
	ee = 0.00669342162296594323
)

var (
	ErrInvalidLatitude  = errors.New("纬度必须在 -90 到 90 之间且为有效数字")
	ErrInvalidLongitude = errors.New("经度必须在 -180 到 180 之间且为有效数字")
	ErrUnsupportedCoord = errors.New("不支持的坐标系统")
)

// Validate 校验经纬度是否在合法地理范围内
func Validate(lat, lng float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90.0 || lat > 90.0 {
		return ErrInvalidLatitude
	}
	if math.IsNaN(lng) || math.IsInf(lng, 0) || lng < -180.0 || lng > 180.0 {
		return ErrInvalidLongitude
	}
	return nil
}

// OutOfChina 使用常见近似范围排除远离中国的坐标，并非精确国界判断。
func OutOfChina(lat, lng float64) bool {
	if lng < 72.004 || lng > 137.8347 {
		return true
	}
	if lat < 0.8293 || lat > 55.8271 {
		return true
	}
	return false
}

func transformLat(x, y float64) float64 {
	ret := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	ret += (160.0*math.Sin(y/12.0*math.Pi) + 320*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return ret
}

func transformLng(x, y float64) float64 {
	ret := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	ret += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return ret
}

func delta(lat, lng float64) (dLat, dLng float64) {
	dLat = transformLat(lng-105.0, lat-35.0)
	dLng = transformLng(lng-105.0, lat-35.0)
	radLat := lat / 180.0 * math.Pi
	magic := math.Sin(radLat)
	magic = 1 - ee*magic*magic
	sqrtMagic := math.Sqrt(magic)
	dLat = (dLat * 180.0) / ((a * (1 - ee)) / (magic * sqrtMagic) * math.Pi)
	dLng = (dLng * 180.0) / (a / sqrtMagic * math.Cos(radLat) * math.Pi)
	return dLat, dLng
}

// WGS84ToGCJ02 将 WGS-84 坐标转换为 GCJ-02 (火星坐标)
func WGS84ToGCJ02(lat, lng float64) (float64, float64) {
	if OutOfChina(lat, lng) {
		return lat, lng
	}
	dLat, dLng := delta(lat, lng)
	return lat + dLat, lng + dLng
}

// GCJ02ToWGS84 迭代反算坐标，避免单次差值修正带来的数米回显误差。
func GCJ02ToWGS84(lat, lng float64) (float64, float64) {
	if OutOfChina(lat, lng) {
		return lat, lng
	}
	wLat, wLng := lat, lng
	for i := 0; i < 10; i++ {
		gLat, gLng := WGS84ToGCJ02(wLat, wLng)
		dLat, dLng := gLat-lat, gLng-lng
		wLat, wLng = wLat-dLat, wLng-dLng
		if math.Abs(dLat) < 1e-8 && math.Abs(dLng) < 1e-8 {
			break
		}
	}
	return wLat, wLng
}

// ToWGS84 根据输入的坐标系，统一转换为待注入 Android 系统的 WGS-84 坐标
func ToWGS84(lat, lng float64, coordSys string) (float64, float64, error) {
	if err := Validate(lat, lng); err != nil {
		return 0, 0, err
	}
	switch coordSys {
	case WGS84, "":
		return lat, lng, nil
	case GCJ02:
		wLat, wLng := GCJ02ToWGS84(lat, lng)
		return wLat, wLng, nil
	default:
		return 0, 0, ErrUnsupportedCoord
	}
}

// FromWGS84 将 WGS-84 坐标转换为目标底图坐标系，用于在地图上正确回显
func FromWGS84(lat, lng float64, targetSys string) (float64, float64, error) {
	if err := Validate(lat, lng); err != nil {
		return 0, 0, err
	}
	switch targetSys {
	case WGS84, "":
		return lat, lng, nil
	case GCJ02:
		gLat, gLng := WGS84ToGCJ02(lat, lng)
		return gLat, gLng, nil
	default:
		return 0, 0, ErrUnsupportedCoord
	}
}
