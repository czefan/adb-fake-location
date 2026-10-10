package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"adb-fake-location/internal/geo"
)

type LocationRequest struct {
	Latitude         *float64 `json:"latitude"`
	Longitude        *float64 `json:"longitude"`
	CoordinateSystem string   `json:"coordinate_system"`
}

type APIResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

type Server struct {
	sessionManager *SessionManager
	staticFS       fs.FS
	token          string
	mapConfig      MapConfig
	configMessage  string
}

func NewServer(sm *SessionManager, staticFS fs.FS) *Server {
	return &Server{sessionManager: sm, staticFS: staticFS, token: rand.Text(), mapConfig: defaultMapConfig()}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/preview", s.handlePreview)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/location", s.handleUpdateLocation)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/recover", s.handleRecover)
	if s.staticFS != nil {
		mux.Handle("/", http.FileServer(http.FS(s.staticFS)))
	}
	return s.securityMiddleware(mux)
}

func localHost(host string) bool {
	name := host
	if strings.Contains(host, ":") {
		var port string
		var err error
		name, port, err = net.SplitHostPort(host)
		if err != nil {
			return false
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return false
		}
	}
	return name == "127.0.0.1" || name == "localhost" || name == "::1"
}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		deny := func(message string) { writeJSON(w, http.StatusForbidden, APIResponse{Message: message}) }
		if !localHost(r.Host) {
			deny("仅支持本地回环主机")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			deny("不允许跨来源请求")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			deny("不允许跨站点请求")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Session-Token")), []byte(s.token)) != 1 {
				deny("会话令牌无效，请刷新页面")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Message: "请求方法不支持"})
	return false
}

func decodeLocation(w http.ResponseWriter, r *http.Request) (LocationRequest, error) {
	var req LocationRequest
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		return req, errors.New("请求应使用 application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, fmt.Errorf("请求格式无效: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return req, errors.New("请求只能包含一个 JSON 对象")
	}
	if req.Latitude == nil || req.Longitude == nil {
		return req, errors.New("必须填写 latitude 和 longitude，不能为 null")
	}
	if req.CoordinateSystem == "" {
		req.CoordinateSystem = geo.WGS84
	}
	_, _, err = geo.ToWGS84(*req.Latitude, *req.Longitude, req.CoordinateSystem)
	return req, err
}

func operationResult(w http.ResponseWriter, err error, message string) {
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrSessionBusy) {
			status = http.StatusConflict
		}
		writeJSON(w, status, APIResponse{Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, APIResponse{Success: true, Message: message})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	config := s.mapConfig
	// Leaflet renders attribution as HTML; treat the config value as plain text.
	config.Attribution = html.EscapeString(config.Attribution)
	writeJSON(w, http.StatusOK, map[string]any{"token": s.token, "map": config, "message": s.configMessage})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.sessionManager.RefreshStatus(ctx))
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	req, err := decodeLocation(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Message: err.Error()})
		return
	}
	lat, lng, _ := geo.ToWGS84(*req.Latitude, *req.Longitude, req.CoordinateSystem)
	lat, lng, err = geo.FromWGS84(lat, lng, s.mapConfig.CoordinateSystem)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"latitude": lat, "longitude": lng})
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	req, err := decodeLocation(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Message: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err = s.sessionManager.Start(ctx, *req.Latitude, *req.Longitude, req.CoordinateSystem)
	operationResult(w, err, "模拟已启动；命令成功不代表所有应用都会采用此位置")
}

func (s *Server) handleUpdateLocation(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	req, err := decodeLocation(w, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{Message: err.Error()})
		return
	}
	err = s.sessionManager.UpdateLocation(*req.Latitude, *req.Longitude, req.CoordinateSystem)
	operationResult(w, err, "新坐标已提交，将由定位任务发送")
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	operationResult(w, s.sessionManager.Stop(ctx), "已清理模拟定位，等待真实定位更新")
}

func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	operationResult(w, s.sessionManager.Recover(ctx), "遗留会话已清理，等待真实定位更新")
}
