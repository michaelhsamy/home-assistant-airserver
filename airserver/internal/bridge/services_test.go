package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

func TestServicesReadUsesWebSocketInsteadOfStaleAPI(t *testing.T) {
	var live atomic.Bool
	live.Store(true)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/system" {
			fmt.Fprint(w, `{"device_serial":"serial","livestreaming_enabled":false,"livestreaming_rtsp":true}`)
			return
		}
		if r.URL.Path != "/live/ws/" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("service probe received the API key")
		}
		if !live.Load() {
			w.WriteHeader(502)
			return
		}
		upgrader := websocket.Upgrader{}
		connection, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			connection.Close()
		}
	}))
	defer server.Close()
	var config DeviceConfig
	data, _ := json.Marshal(map[string]any{"name": "Room", "host": server.URL, "api_key": "secret", "verify_ssl": false, "state_source": "services"})
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	api := NewAPI(config)
	defer api.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	api.rtspAddress = listener.Addr().String()
	state, err := api.Read(context.Background())
	if err != nil || state.Livestream == nil || !*state.Livestream || state.RTSP == nil || !*state.RTSP {
		t.Fatalf("active livestream was overwritten by stale REST state: %v, %v", state, err)
	}
	listener.Close()
	state, err = api.Read(context.Background())
	if err != nil || state.RTSP == nil || *state.RTSP {
		t.Fatalf("closed RTSP listener was overwritten by stale REST state: %v, %v", state, err)
	}
	live.Store(false)
	state, err = api.Read(context.Background())
	if err != nil || state.Livestream == nil || *state.Livestream || state.RTSP != nil {
		t.Fatalf("disabled livestream must leave the saved RTSP setting unknown: %v, %v", state, err)
	}
}

func TestServiceProbeFailuresDoNotBecomeOff(t *testing.T) {
	for _, status := range []int{200, 301, 401, 403, 404, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/unexpected-redirect")
				w.WriteHeader(status)
				fmt.Fprint(w, "secret-response")
			}))
			defer server.Close()
			api := NewAPI(DeviceConfig{Host: server.URL})
			defer api.Close()
			live, rtsp, err := api.readServices(context.Background())
			if err == nil || live != nil || rtsp != nil || strings.Contains(err.Error(), "secret-response") {
				t.Fatalf("unexpected/unsafe probe result: %v", err)
			}
		})
	}
}

func TestServiceProbeTLSAndCancellation(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer server.Close()
	secure := NewAPI(DeviceConfig{Host: server.URL})
	defer secure.Close()
	if _, _, err := secure.readServices(context.Background()); err == nil {
		t.Fatal("untrusted websocket certificate accepted")
	}
	exception := NewAPI(DeviceConfig{Host: server.URL, VerifySSL: boolPtr(false)})
	defer exception.Close()
	live, rtsp, err := exception.readServices(context.Background())
	if err != nil || live == nil || *live || rtsp != nil {
		t.Fatalf("per-device TLS exception failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if live, rtsp, err := exception.readServices(ctx); err == nil || live != nil || rtsp != nil {
		t.Fatal("cancellation was interpreted as a disabled service")
	}
}

func TestServiceWorkerConfirmsCommandsDespiteFrozenRESTState(t *testing.T) {
	broker, brokerConfig := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, brokerConfig)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var live atomic.Bool
	var pendingReads atomic.Int32
	var writesMu sync.Mutex
	var writes []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live/ws/" {
			// Reproduce a service that takes several readbacks to start.
			if pendingReads.Load() > 0 && pendingReads.Add(-1) == 0 {
				live.Store(true)
			}
			if !live.Load() {
				w.WriteHeader(502)
				return
			}
			upgrader := websocket.Upgrader{}
			connection, err := upgrader.Upgrade(w, r, nil)
			if err == nil {
				connection.Close()
			}
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.URL.Path != "/api/v1/system" {
			t.Error("wrong authenticated API request")
			w.WriteHeader(401)
			return
		}
		if r.Method == http.MethodPatch {
			var patch map[string]bool
			if json.NewDecoder(r.Body).Decode(&patch) != nil || len(patch) != 1 {
				t.Error("command changed more than one setting")
				w.WriteHeader(400)
				return
			}
			data, _ := json.Marshal(patch)
			writesMu.Lock()
			writes = append(writes, string(data))
			writesMu.Unlock()
			if enabled, ok := patch["livestreaming_enabled"]; ok && enabled {
				pendingReads.Store(3)
			}
			if enabled, ok := patch["livestreaming_rtsp"]; ok && !enabled {
				listener.Close()
			}
			w.WriteHeader(204)
			return
		}
		// The device's API keeps returning boot-time settings after commands.
		fmt.Fprint(w, `{"device_serial":"service-device","livestreaming_enabled":false,"livestreaming_rtsp":true}`)
	}))
	defer server.Close()
	registry, err := LoadRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	b := New(Options{PollInterval: 2}, registry, quiet)
	b.pollInterval = 50 * time.Millisecond
	s := &session{bridge: b, ctx: ctx, cancel: cancel, owners: make(map[string]*worker)}
	s.client = paho.NewClient(clientOptions(brokerConfig, s.handle, nil))
	if err := waitToken(ctx, s.client.Connect()); err != nil {
		t.Fatal(err)
	}
	defer s.client.Disconnect(100)
	if err := waitToken(ctx, s.client.Subscribe(root+"/+/+/set", 0, nil)); err != nil {
		t.Fatal(err)
	}
	w := newWorker(s, DeviceConfig{Name: "Room", Host: server.URL, APIKey: "test-key", VerifySSL: boolPtr(false), StateSource: "services"}, "")
	w.api.rtspAddress = listener.Addr().String()
	s.workers = []*worker{w}
	done := make(chan struct{})
	go func() { defer close(done); w.run() }()
	defer func() { cancel(nil); <-done; w.api.Close() }()
	id := deviceID("service-device")
	eventually(t, func() bool { return o.value(topic(id, "availability")) == "online" })
	if o.value(topic(id, "livestream/state")) != "OFF" || o.value(topic(id, "rtsp/availability")) != "offline" {
		t.Fatal("RTSP was presented as known while Livestream was off")
	}
	var discoveryConfig map[string]any
	if err := json.Unmarshal([]byte(o.value(discoveryTopic(id, "rtsp"))), &discoveryConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(o.value(discoveryTopic(id, "rtsp")), topic(id, "rtsp/availability")) {
		t.Fatal("Home Assistant cannot see per-switch availability")
	}
	o.publish(t, topic(id, "rtsp/set"), "ON", false) // Unknown control must reject this.
	o.publish(t, topic(id, "livestream/set"), "ON", false)
	eventually(t, func() bool {
		return o.value(topic(id, "livestream/state")) == "ON" && o.value(topic(id, "rtsp/state")) == "ON"
	})
	before := o.count(topic(id, "livestream/state"))
	eventually(t, func() bool { return o.count(topic(id, "livestream/state")) >= before+3 })
	if o.value(topic(id, "livestream/state")) != "ON" || o.offlineCount(topic(id, "availability")) != 0 {
		t.Fatal("delayed service activation reverted to stale API state or became unavailable")
	}
	o.publish(t, topic(id, "rtsp/set"), "OFF", false)
	eventually(t, func() bool { return o.value(topic(id, "rtsp/state")) == "OFF" })
	writesMu.Lock()
	gotWrites := append([]string(nil), writes...)
	writesMu.Unlock()
	if !reflect.DeepEqual(gotWrites, []string{`{"livestreaming_enabled":true}`, `{"livestreaming_rtsp":false}`}) {
		t.Fatalf("unexpected writes or command replay: %v", gotWrites)
	}
	live.Store(false) // External change must be observed without another write.
	eventually(t, func() bool {
		return o.value(topic(id, "livestream/state")) == "OFF" && o.value(topic(id, "rtsp/availability")) == "offline"
	})
	if o.value(topic(id, "availability")) != "online" {
		t.Fatal("unknown RTSP state made the whole device unavailable")
	}
}

func TestOptionsRejectUnknownStateSource(t *testing.T) {
	_, err := ParseOptions([]byte(`{"devices":[{"name":"Room","host":"device","api_key":"secret","state_source":"invalid"}]}`))
	if err == nil {
		t.Fatal("unknown state source was accepted")
	}
}
