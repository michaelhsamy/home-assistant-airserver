package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeDevice struct {
	mu           sync.Mutex
	state        State
	writes       []string
	status       int
	delay        time.Duration
	config       DeviceConfig
	ignoreWrites bool
}

func newDevice(t *testing.T, index int) *fakeDevice {
	t.Helper()
	d := &fakeDevice{state: State{Serial: fmt.Sprintf("serial-%d", index), Model: "Connect 1", Version: "test", Livestream: boolPtr(false), RTSP: boolPtr(false)}, status: 200}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(401)
			return
		}
		d.mu.Lock()
		if r.Method == http.MethodGet {
			status := d.status
			data, _ := json.Marshal(d.state)
			d.mu.Unlock()
			w.WriteHeader(status)
			_, _ = w.Write(data)
			return
		}
		if r.Method == http.MethodPatch {
			var patch map[string]bool
			if json.NewDecoder(r.Body).Decode(&patch) != nil {
				d.mu.Unlock()
				w.WriteHeader(400)
				return
			}
			data, _ := json.Marshal(patch)
			d.writes = append(d.writes, string(data))
			if v, ok := patch["livestreaming_enabled"]; ok && !d.ignoreWrites {
				d.state.Livestream = boolPtr(v)
			}
			if v, ok := patch["livestreaming_rtsp"]; ok && !d.ignoreWrites {
				d.state.RTSP = boolPtr(v)
			}
		} else {
			d.writes = append(d.writes, r.Method+" "+r.URL.Path)
		}
		delay := d.delay
		d.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(server.Close)
	d.config = DeviceConfig{Name: fmt.Sprintf("Room %d", index), Host: server.URL, APIKey: "test-key", VerifySSL: boolPtr(false)}
	return d
}
func (d *fakeDevice) count() int       { d.mu.Lock(); defer d.mu.Unlock(); return len(d.writes) }
func (d *fakeDevice) modify(fn func()) { d.mu.Lock(); defer d.mu.Unlock(); fn() }

type testBroker struct {
	server   *mochi.Server
	listener *listeners.TCP
	once     sync.Once
}

func (b *testBroker) Close() error {
	b.once.Do(func() {
		// Mochi 2.7.9's normal close path recursively RLocks its client map;
		// a concurrent disconnect writer can deadlock that path. Close through
		// a snapshot first, so Server.Close only sees an already-closed listener.
		b.listener.Close(func(string) {
			for _, client := range b.server.Clients.GetAll() {
				client.Stop(fmt.Errorf("test broker stopped"))
			}
		})
		_ = b.server.Close()
	})
	return nil
}

func startBroker(t *testing.T, address string) (*testBroker, brokerConfig) {
	t.Helper()
	server := mochi.New(&mochi.Options{Logger: quiet})
	if err := server.AddHook(new(auth.AllowHook), nil); err != nil {
		t.Fatal(err)
	}
	listener := listeners.NewTCP(listeners.Config{ID: "test", Address: address})
	if err := server.AddListener(listener); err != nil {
		t.Fatal(err)
	}
	if err := server.Serve(); err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(listener.Address())
	number, _ := strconv.Atoi(port)
	return &testBroker{server: server, listener: listener}, brokerConfig{host: host, port: number}
}

type observer struct {
	mu            sync.Mutex
	values        map[string]string
	counts        map[string]int
	offlineCounts map[string]int
	client        paho.Client
}

func observe(t *testing.T, c brokerConfig) *observer {
	t.Helper()
	o := &observer{values: make(map[string]string), counts: make(map[string]int), offlineCounts: make(map[string]int)}
	options := paho.NewClientOptions().AddBroker("tcp://" + net.JoinHostPort(c.host, strconv.Itoa(c.port))).SetAutoReconnect(false).SetCleanSession(true)
	options.SetDefaultPublishHandler(func(_ paho.Client, m paho.Message) {
		o.mu.Lock()
		defer o.mu.Unlock()
		o.values[m.Topic()] = string(m.Payload())
		o.counts[m.Topic()]++
		if string(m.Payload()) == "offline" {
			o.offlineCounts[m.Topic()]++
		}
	})
	o.client = paho.NewClient(options)
	if err := waitToken(context.Background(), o.client.Connect()); err != nil {
		t.Fatal(err)
	}
	if err := waitToken(context.Background(), o.client.Subscribe("#", 0, nil)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { o.client.Disconnect(100) })
	return o
}
func (o *observer) value(topic string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.values[topic]
}
func (o *observer) count(topic string) int { o.mu.Lock(); defer o.mu.Unlock(); return o.counts[topic] }
func (o *observer) offlineCount(topic string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.offlineCounts[topic]
}
func (o *observer) publish(t *testing.T, topic, payload string, retain bool) {
	t.Helper()
	if err := waitToken(context.Background(), o.client.Publish(topic, 0, retain, payload)); err != nil {
		t.Fatal(err)
	}
}
func eventually(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !fn() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func startBridge(t *testing.T, c brokerConfig, path string, devices ...*fakeDevice) func() {
	t.Helper()
	registry, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	options := Options{PollInterval: 2}
	for _, d := range devices {
		options.Devices = append(options.Devices, d.config)
	}
	b := New(options, registry, quiet)
	b.pollInterval = 100 * time.Millisecond
	b.retryInterval = 100 * time.Millisecond
	b.service = func(context.Context, string) (brokerConfig, error) { return c, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := b.Run(ctx, "test-token"); err != nil {
			t.Error(err)
		}
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("bridge did not stop")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
func waitOnline(t *testing.T, o *observer, devices ...*fakeDevice) {
	t.Helper()
	eventually(t, func() bool {
		for _, d := range devices {
			if o.value(topic(deviceID(d.state.Serial), "availability")) != "online" {
				return false
			}
		}
		return true
	})
}

func TestThreeIndependentDevicesAndExternalState(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, c)
	a, b, d := newDevice(t, 1), newDevice(t, 2), newDevice(t, 3)
	stop := startBridge(t, c, filepath.Join(t.TempDir(), "registry.json"), a, b, d)
	defer stop()
	waitOnline(t, o, a, b, d)
	ids := []string{deviceID(a.state.Serial), deviceID(b.state.Serial), deviceID(d.state.Serial)}
	for _, id := range ids {
		for _, control := range controls {
			var config map[string]any
			if err := json.Unmarshal([]byte(o.value(discoveryTopic(id, control))), &config); err != nil {
				t.Fatal(err)
			}
			if config["unique_id"] != id+"_"+control {
				t.Fatal("wrong identity")
			}
		}
	}
	if a.count()+b.count()+d.count() != 0 {
		t.Fatal("startup wrote settings")
	}
	o.publish(t, topic(ids[1], "rtsp/set"), "ON", false)
	eventually(t, func() bool { return o.value(topic(ids[1], "rtsp/state")) == "ON" })
	if o.value(topic(ids[1], "livestream/state")) != "OFF" || a.count() != 0 || d.count() != 0 {
		t.Fatal("command affected another control/device")
	}
	o.publish(t, topic(ids[2], "end_session/set"), "PRESS", false)
	eventually(t, func() bool { return d.count() == 1 })
	a.modify(func() { a.state.Livestream = boolPtr(true) })
	eventually(t, func() bool { return o.value(topic(ids[0], "livestream/state")) == "ON" })
	if a.count() != 0 {
		t.Fatal("external change was written back")
	}
}

func TestUnconfirmedSwitchCommandMarksDeviceUnavailable(t *testing.T) {
	for _, cmd := range []command{{"rtsp", "OFF"}, {"livestream", "ON"}} {
		t.Run(cmd.control, func(t *testing.T) {
			broker, c := startBroker(t, "127.0.0.1:0")
			defer broker.Close()
			o := observe(t, c)
			d := newDevice(t, 1)
			d.modify(func() {
				d.state.RTSP = boolPtr(true)
				d.ignoreWrites = true
			})
			stop := startBridge(t, c, filepath.Join(t.TempDir(), "registry.json"), d)
			defer stop()
			waitOnline(t, o, d)
			id := deviceID(d.state.Serial)
			before := o.offlineCount(topic(id, "availability"))
			o.publish(t, topic(id, cmd.control+"/set"), cmd.payload, false)
			deadline := time.Now().Add(2 * time.Second)
			for o.offlineCount(topic(id, "availability")) == before {
				if time.Now().After(deadline) {
					t.Fatal("successful HTTP response with unchanged setting was treated as confirmed")
				}
				time.Sleep(10 * time.Millisecond)
			}
			waitOnline(t, o, d)
			if d.count() != 1 {
				t.Fatal("unconfirmed command was retried")
			}
		})
	}
}

func TestDeviceOutageAndNoCommandReplay(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, c)
	a, b := newDevice(t, 1), newDevice(t, 2)
	stop := startBridge(t, c, filepath.Join(t.TempDir(), "registry.json"), a, b)
	defer stop()
	waitOnline(t, o, a, b)
	id := deviceID(a.state.Serial)
	a.modify(func() { a.status = 503 })
	eventually(t, func() bool { return o.value(topic(id, "availability")) == "offline" })
	o.publish(t, topic(id, "end_session/set"), "PRESS", false)
	o.publish(t, topic(deviceID(b.state.Serial), "livestream/set"), "ON", false)
	eventually(t, func() bool { return b.count() == 1 })
	a.modify(func() { a.status = 200 })
	waitOnline(t, o, a)
	if a.count() != 0 {
		t.Fatal("offline command replayed")
	}
}

func TestRetainedCommandsRestartAndDiscoveryCleanup(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, c)
	a, b := newDevice(t, 1), newDevice(t, 2)
	id := deviceID(a.state.Serial)
	path := filepath.Join(t.TempDir(), "registry.json")
	o.publish(t, topic(id, "end_session/set"), "PRESS", true)
	o.publish(t, topic(id, "rtsp/set"), "ON", true)
	stop := startBridge(t, c, path, a, b)
	waitOnline(t, o, a, b)
	key := discoveryTopic(id, "rtsp")
	count := o.count(key)
	o.publish(t, "homeassistant/status", "online", false)
	eventually(t, func() bool { return o.count(key) > count })
	stop()
	eventually(t, func() bool { return o.value(bridgeAvailability) == "offline" })
	o.publish(t, topic(id, "livestream/set"), "ON", false)
	stop = startBridge(t, c, path, a, b)
	waitOnline(t, o, a, b)
	stop()
	if a.count() != 0 || b.count() != 0 {
		t.Fatal("retained/offline command replayed")
	}
	stop = startBridge(t, c, path, b)
	defer stop()
	waitOnline(t, o, b)
	eventually(t, func() bool { return o.value(key) == "" })
	registry, err := LoadRegistry(path)
	if err != nil || len(registry.snapshot()) != 1 {
		t.Fatalf("cleanup failed: %v", err)
	}
}

func TestBrokerRestartRestoresDiscovery(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	o := observe(t, c)
	a, b := newDevice(t, 1), newDevice(t, 2)
	stop := startBridge(t, c, filepath.Join(t.TempDir(), "registry.json"), a, b)
	defer stop()
	waitOnline(t, o, a, b)
	if err := broker.Close(); err != nil {
		t.Fatal(err)
	}
	replacement, _ := startBroker(t, net.JoinHostPort(c.host, strconv.Itoa(c.port)))
	defer replacement.Close()
	next := observe(t, c)
	waitOnline(t, next, a, b)
	if next.value(discoveryTopic(deviceID(a.state.Serial), "rtsp")) == "" {
		t.Fatal("discovery not restored")
	}
	if a.count()+b.count() != 0 {
		t.Fatal("broker restart wrote settings")
	}
}

func TestSlowCommandDoesNotBlockOtherDevicesOrRepeat(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, c)
	a, b := newDevice(t, 1), newDevice(t, 2)
	stop := startBridge(t, c, filepath.Join(t.TempDir(), "registry.json"), a, b)
	defer stop()
	waitOnline(t, o, a, b)
	a.modify(func() { a.delay = 6 * time.Second })
	id := deviceID(a.state.Serial)
	o.publish(t, topic(id, "end_session/set"), "PRESS", false)
	eventually(t, func() bool { return a.count() == 1 })
	o.publish(t, topic(id, "end_session/set"), "PRESS", false)
	o.publish(t, topic(deviceID(b.state.Serial), "rtsp/set"), "ON", false)
	eventually(t, func() bool { return b.count() == 1 })
	if o.value(topic(id, "availability")) != "online" {
		t.Fatal("slow command should still be in flight")
	}
	// After the timeout a subsequent poll reconciles state without repeating the POST.
	before := o.count(topic(id, "rtsp/state"))
	eventually(t, func() bool { return o.count(topic(id, "rtsp/state")) > before })
	if a.count() != 1 {
		t.Fatal("timed-out command was retried or queued")
	}
}

func TestFailedPollDiscardsWaitingCommand(t *testing.T) {
	// Exercise the worker's admission boundary: a pending command must not survive
	// an unavailable transition even if a later poll recovers before it is selected.
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s := &session{ctx: ctx, bridge: &Bridge{log: quiet}}
	w := newWorker(s, DeviceConfig{}, "")
	defer w.api.Close()
	w.online.Store(true)
	w.submit(command{"end_session", "PRESS"})
	w.unavailable(fmt.Errorf("read failed"))
	if len(w.commands) != 0 || w.busy.Load() || w.online.Load() {
		t.Fatal("pending command survived failed read")
	}
}

func TestRegistryNeverPersistsCredentials(t *testing.T) {
	r, err := LoadRegistry(filepath.Join(t.TempDir(), "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	config := DeviceConfig{Host: "https://private-host", APIKey: "secret"}
	if err := r.set(config.cacheKey(), deviceID("serial")); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(r.snapshot())
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "private-host") || strings.Contains(string(data), "serial") {
		t.Fatal("sensitive data persisted")
	}
}

func TestConcurrentAdmissionAndDeviceFailure(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s := &session{ctx: ctx, bridge: &Bridge{log: quiet}}
	w := newWorker(s, DeviceConfig{}, "")
	defer w.api.Close()
	for range 1000 {
		w.online.Store(true)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			w.submit(command{"end_session", "PRESS"})
		}()
		go func() {
			defer wg.Done()
			<-start
			w.unavailable(fmt.Errorf("read failed"))
		}()
		close(start)
		wg.Wait()
		if len(w.commands) != 0 || w.busy.Load() || w.online.Load() {
			t.Fatal("concurrent admission left a command waiting after failure")
		}
	}
}
