package probe_test

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/probe"
)

// redirectTransport routes requests intended for 127.0.0.1:80 to a test server.
type redirectTransport struct {
	base       http.RoundTripper
	targetAddr string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "127.0.0.1" || req.URL.Host == "localhost" {
		newReq := *req
		newURL := *req.URL
		newURL.Host = rt.targetAddr
		newReq.URL = &newURL
		return rt.base.RoundTrip(&newReq)
	}
	return rt.base.RoundTrip(req)
}

func makeClient(targetAddr string) *http.Client {
	return &http.Client{
		Transport: &redirectTransport{
			base:       http.DefaultTransport,
			targetAddr: targetAddr,
		},
	}
}

func TestProbeWhoami_Hit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Witsaba-Device", "true")
		json.NewEncoder(w).Encode(map[string]string{
			"name": "cam-01",
			"mac":  "aa:bb:cc:dd:ee:ff",
			"fw":   "1.2.3",
			"chip": "esp32",
		})
	}))
	defer srv.Close()

	client := makeClient(srv.Listener.Addr().String())
	ev, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 5*time.Second, client)
	if err != nil {
		t.Fatalf("ProbeWithClient returned unexpected error: %v", err)
	}
	if !matched {
		t.Fatal("expected matched=true for witsaba device")
	}
	if ev.Name != "cam-01" {
		t.Errorf("expected Name=cam-01, got %q", ev.Name)
	}
	if ev.SourceIP == "" {
		t.Error("expected SourceIP to be set")
	}
}

func TestProbeWhoami_MissHeaderFalse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Witsaba-Device", "false")
		json.NewEncoder(w).Encode(map[string]string{"name": "plain"})
	}))
	defer srv.Close()

	client := makeClient(srv.Listener.Addr().String())
	_, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 5*time.Second, client)
	if err != nil {
		t.Fatalf("ProbeWithClient returned unexpected error: %v", err)
	}
	if matched {
		t.Error("expected matched=false for X-Witsaba-Device: false")
	}
}

func TestProbeWhoami_MissHeaderAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := makeClient(srv.Listener.Addr().String())
	_, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 5*time.Second, client)
	if err != nil {
		t.Fatalf("ProbeWithClient returned unexpected error: %v", err)
	}
	if matched {
		t.Error("expected matched=false when header is absent")
	}
}

func TestProbeWhoami_ConnectionRefused(t *testing.T) {
	client := &http.Client{
		Transport: &http.Transport{},
		Timeout:   100 * time.Millisecond,
	}
	_, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 100*time.Millisecond, client)
	if err == nil {
		t.Error("expected error for connection refused")
	}
	if matched {
		t.Error("expected matched=false for connection refused")
	}
}

func TestProbeWhoami_HeaderCaseInsensitive(t *testing.T) {
	for _, tc := range []struct {
		headerName  string
		headerValue string
		wantMatch   bool
	}{
		{"x-witsaba-device", "TRUE", true},
		{"x-witsaba-device", "True", true},
		{"x-witsaba-device", "true", true},
		{"X-WITSABA-DEVICE", "true", true},
		{"X-Witsaba-Device", "false", false},
	} {
		tc := tc
		t.Run(tc.headerName+"-"+tc.headerValue, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(tc.headerName, tc.headerValue)
				json.NewEncoder(w).Encode(map[string]string{
					"name": "test",
					"mac":  "00:00:00:00:00:00",
					"fw":   "0.0.0",
					"chip": "test",
				})
			})
			srv := httptest.NewServer(mux)
			client := makeClient(srv.Listener.Addr().String())
			_, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 5*time.Second, client)
			srv.Close()
			if err != nil {
				t.Fatalf("ProbeWithClient error: %v", err)
			}
			if matched != tc.wantMatch {
				t.Errorf("matched=%v, want %v", matched, tc.wantMatch)
			}
		})
	}
}

func TestProbeWhoami_BodyParseError_Miss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Witsaba-Device", "true")
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	client := makeClient(srv.Listener.Addr().String())
	_, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 5*time.Second, client)
	if err != nil {
		t.Fatalf("ProbeWithClient returned unexpected error: %v", err)
	}
	if matched {
		t.Error("expected matched=false for unparseable body")
	}
}

func TestProbeWhoami_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("X-Witsaba-Device", "true")
	}))
	defer srv.Close()

	client := makeClient(srv.Listener.Addr().String())
	_, matched, err := probe.ProbeWithClient(net.ParseIP("127.0.0.1"), 50*time.Millisecond, client)
	if err == nil {
		t.Error("expected timeout error")
	}
	if matched {
		t.Error("expected matched=false on timeout")
	}
}
