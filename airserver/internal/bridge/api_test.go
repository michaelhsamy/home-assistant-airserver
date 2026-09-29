package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPIIndependentWrites(t *testing.T) {
	var writes []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"device_serial":"serial","livestreaming_enabled":false,"livestreaming_rtsp":true,"api_key":"do-not-persist"}`)
			return
		}
		if r.Method == http.MethodPatch {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			data, _ := json.Marshal(body)
			writes = append(writes, string(data))
		} else {
			writes = append(writes, r.Method+" "+r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	api := NewAPI(DeviceConfig{Name: "Room", Host: server.URL, APIKey: "secret", VerifySSL: boolPtr(false)})
	defer api.Close()
	state, err := api.Read(context.Background())
	if err != nil || *state.Livestream || !*state.RTSP {
		t.Fatalf("read: %v %v", state, err)
	}
	for _, cmd := range []command{{"rtsp", "ON"}, {"livestream", "ON"}, {"rtsp", "OFF"}, {"end_session", "PRESS"},
		{"airplay", "password"}, {"googlecast", "prompt"}, {"miracast", "pin8"}, {"livestream_quality", "high"}, {"reboot", "PRESS"}, {"power_off", "PRESS"}} {
		if err := api.Command(context.Background(), cmd.control, cmd.payload); err != nil {
			t.Fatal(err)
		}
	}
	expected := []string{`{"livestreaming_rtsp":true}`, `{"livestreaming_enabled":true}`, `{"livestreaming_rtsp":false}`, "POST /api/v1/system/endSession",
		`{"airplay":"password"}`, `{"googlecast":"prompt"}`, `{"miracast":"pin8"}`, `{"livestreaming_quality":"high"}`, "POST /api/v1/system/reboot", "POST /api/v1/system/powerOff"}
	if !reflect.DeepEqual(writes, expected) {
		t.Fatalf("wrong writes: %v", writes)
	}
	for _, cmd := range []command{{"rtsp", "true"}, {"reboot", "ON"}, {"end_session", "ON"}, {"airplay", "PRESS"}, {"airplay", "Everyone"},
		{"googlecast", "pin8"}, {"livestream_quality", "ultra"}, {"hostname", "x"}, {"boot_time", "PRESS"}, {"unknown", "PRESS"}} {
		if api.Command(context.Background(), cmd.control, cmd.payload) == nil {
			t.Fatal("invalid command accepted")
		}
	}
	if len(writes) != len(expected) {
		t.Fatal("invalid command reached server")
	}
}

func TestAPIHTTPErrorsAndFirmware(t *testing.T) {
	for _, status := range []int{301, 400, 401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.WriteHeader(status)
				fmt.Fprint(w, "secret")
			}))
			defer server.Close()
			api := NewAPI(DeviceConfig{Host: server.URL, APIKey: "secret"})
			defer api.Close()
			_, err := api.Read(context.Background())
			if err == nil || !strings.Contains(err.Error(), fmt.Sprint(status)) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe/wrong error: %v", err)
			}
			if count.Load() != 1 {
				t.Fatal("request retried")
			}
		})
	}
	for _, body := range []string{`{}`, `[]`, `not-json`, `{"device_serial":"s","livestreaming_enabled":false,"livestreaming_rtsp":"false"}`, `{"livestreaming_enabled":false,"livestreaming_rtsp":true}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			api := NewAPI(DeviceConfig{Host: server.URL})
			defer api.Close()
			if _, err := api.Read(context.Background()); err == nil {
				t.Fatal("unsupported response accepted")
			}
		})
	}
}

func TestTLSExceptionIsPerDevice(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_serial":"s","livestreaming_enabled":false,"livestreaming_rtsp":true}`)
	}))
	defer server.Close()
	secure := NewAPI(DeviceConfig{Host: server.URL})
	defer secure.Close()
	exception := NewAPI(DeviceConfig{Host: server.URL, VerifySSL: boolPtr(false)})
	defer exception.Close()
	for i := range 2 {
		if _, err := secure.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "certificate verification") {
			t.Fatalf("TLS accepted: %v", err)
		}
		if i == 0 {
			if _, err := exception.Read(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	var reached atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	api := NewAPI(DeviceConfig{Host: source.URL, APIKey: "secret"})
	defer api.Close()
	if _, err := api.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatal(err)
	}
	if reached.Load() {
		t.Fatal("redirect followed")
	}
}

func TestTimedOutCommandNeverRetried(t *testing.T) {
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count.Add(1); <-r.Context().Done() }))
	defer server.Close()
	api := NewAPI(DeviceConfig{Host: server.URL})
	defer api.Close()
	api.client.Timeout = 40 * time.Millisecond
	if err := api.Command(context.Background(), "end_session", "PRESS"); err == nil {
		t.Fatal("expected timeout")
	}
	if count.Load() != 1 {
		t.Fatal("command repeated")
	}
}

func TestSupervisorCredentials(t *testing.T) {
	for _, port := range []string{`"8883"`, `8883`} {
		t.Run(port, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Error("missing Supervisor token")
				}
				fmt.Fprintf(w, `{"result":"ok","data":{"host":"broker","port":%s,"username":"u","password":"p","ssl":true}}`, port)
			}))
			defer server.Close()
			c, err := mqttService(context.Background(), server.Client(), server.URL, "token")
			if err != nil {
				t.Fatal(err)
			}
			if c.host != "broker" || c.port != 8883 || c.password != "p" || !c.tls {
				t.Fatal("incorrect credentials")
			}
			options := clientOptions(c, nil, nil)
			if options.TLSConfig.InsecureSkipVerify || options.AutoReconnect || options.ConnectRetry || !options.CleanSession {
				t.Fatal("unsafe TLS or reconnection defaults")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); fmt.Fprint(w, "secret-response") }))
	defer server.Close()
	_, err := mqttService(context.Background(), server.Client(), server.URL, "token")
	if err == nil || strings.Contains(err.Error(), "secret-response") {
		t.Fatalf("unsafe response: %v", err)
	}
}
