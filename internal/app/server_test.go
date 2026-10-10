package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	webassets "adb-fake-location/web"
)

func TestAPILocalOriginAndSessionToken(t *testing.T) {
	sm, _ := newTestSession(t)
	s := NewServer(sm, nil)
	for _, tc := range []struct {
		name, host, origin, token string
		want                      int
	}{
		{"valid", "127.0.0.1:8080", "http://127.0.0.1:8080", s.token, 200},
		{"missing token", "127.0.0.1:8080", "", "", 403},
		{"wrong token", "127.0.0.1:8080", "", "wrong", 403},
		{"cross origin", "127.0.0.1:8080", "https://example.com", s.token, 403},
		{"prefix host", "127.0.0.1.evil.test", "", s.token, 403},
		{"localhost prefix", "localhost.evil.test", "", s.token, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://127.0.0.1/api/stop", strings.NewReader("{}"))
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Session-Token", tc.token)
			w := httptest.NewRecorder()
			s.Routes().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestMissingNullAndInvalidCoordinatesNeverStartADB(t *testing.T) {
	sm, f := newTestSession(t)
	s := NewServer(sm, nil)
	for _, body := range []string{`{}`, `null`, `{"latitude":null,"longitude":1}`, `{"latitude":1}`, `{"latitude":91,"longitude":0}`, `{"latitude":0,"longitude":0} {}`, `{"latitude":0,"longitude":0,"extra":1}`, `{"latitude":0,"longitude":0,"coordinate_system":"BD-09"}`} {
		r := httptest.NewRequest("POST", "http://127.0.0.1/api/start", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Session-Token", s.token)
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 {
		t.Fatal("invalid input must not reach ADB")
	}
}

func TestZeroCoordinatesAreValidAndResourcesEmbedded(t *testing.T) {
	sm, _ := newTestSession(t)
	staticFS, err := webassets.Files()
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(sm, staticFS)
	r := httptest.NewRequest("POST", "http://127.0.0.1/api/preview", strings.NewReader(`{"latitude":0,"longitude":0}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Session-Token", s.token)
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"latitude":0`) {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/", "/vendor/leaflet/leaflet.js", "/vendor/leaflet/leaflet.css", "/vendor/leaflet/images/marker-icon.png", "/vendor/leaflet/LICENSE"} {
		w := httptest.NewRecorder()
		s.Routes().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1"+path, nil))
		if w.Code != 200 {
			t.Errorf("missing embedded resource %s: %d", path, w.Code)
		}
	}
}
