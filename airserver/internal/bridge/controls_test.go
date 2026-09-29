package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiscoveryDomainsAndCategories(t *testing.T) {
	id := deviceID("serial")
	configs := discovery(id, "Room", State{Model: "Connect 2", Version: "2026.03.13"})
	expected := map[string]string{
		"livestream": "switch", "rtsp": "switch", "end_session": "button", "reboot": "button", "power_off": "button",
		"airplay": "select", "googlecast": "select", "miracast": "select", "livestream_quality": "select",
		"boot_time": "sensor", "firmware": "sensor", "hostname": "sensor", "device_name": "sensor", "cloud_organization": "sensor", "timezone": "sensor",
	}
	if len(configs) != len(expected) || len(controls) != len(expected) {
		t.Fatalf("expected %d entities, got %d", len(expected), len(configs))
	}
	for key, domain := range expected {
		config := configs["homeassistant/"+domain+"/"+id+"/"+key+"/config"]
		if config == nil {
			t.Fatalf("%s is not a %s", key, domain)
		}
		if config["unique_id"] != id+"_"+key {
			t.Fatalf("%s changed identity", key)
		}
		switch domain {
		case "sensor":
			if config["entity_category"] != "diagnostic" || config["command_topic"] != nil || config["state_topic"] == nil {
				t.Fatalf("sensor %s must be diagnostic and read-only: %v", key, config)
			}
		case "select":
			if config["entity_category"] != "config" || config["optimistic"] != false || config["command_topic"] == nil || config["state_topic"] == nil {
				t.Fatalf("select %s must be confirmed from the device: %v", key, config)
			}
			options, _ := config["options"].([]string)
			if len(options) < 3 {
				t.Fatalf("select %s has no options", key)
			}
		case "button":
			if config["payload_press"] != "PRESS" || config["state_topic"] != nil {
				t.Fatalf("button %s misconfigured: %v", key, config)
			}
		}
	}
	if configs[discoveryTopic(id, "boot_time")]["device_class"] != "timestamp" || configs[discoveryTopic(id, "reboot")]["entity_category"] != "config" {
		t.Fatal("boot time must be a timestamp and reboot a configuration action")
	}
	if configs[discoveryTopic(id, "livestream")]["entity_category"] != nil || configs[discoveryTopic(id, "end_session")]["entity_category"] != nil {
		t.Fatal("existing controls must stay primary entities")
	}
}

func TestReadingsNormalizeAndRejectUnknownValues(t *testing.T) {
	state := State{Version: "2026.03.13", AirPlay: stringValue("everyone"), GoogleCast: stringValue("unexpected"), Quality: stringValue("high"),
		BootTime: stringValue("2026-09-28T01:02:03.5+10:00"), Hostname: stringValue("airserver-1"), Organization: stringValue(""), Livestream: boolPtr(true)}
	read := func(key string) string {
		c, _ := findControl(key)
		v := c.reading(state)
		if v == nil {
			return "<unavailable>"
		}
		return *v
	}
	expected := map[string]string{
		"livestream": "ON", "rtsp": "<unavailable>", "airplay": "everyone", "googlecast": "<unavailable>", "miracast": "<unavailable>", "livestream_quality": "high",
		"boot_time": "2026-09-28T01:02:03+10:00", "firmware": "2026.03.13", "hostname": "airserver-1", "device_name": "<unavailable>", "cloud_organization": "None", "timezone": "<unavailable>",
	}
	for key, want := range expected {
		if got := read(key); got != want {
			t.Errorf("%s: got %q, want %q", key, got, want)
		}
	}
	for _, raw := range []string{"", "yesterday", "2026-09-28 01:02:03", "1700000000"} {
		if timestamp(raw) != nil {
			t.Errorf("unparseable boot time %q must be unavailable, not a broken timestamp sensor", raw)
		}
	}
}

func TestReadTypeMismatchOnlyAffectsThatEntity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"device_serial":"s","livestreaming_enabled":false,"livestreaming_rtsp":true,"airplay":7,"device_hostname":"host","api_key":"secret"}`)
	}))
	defer server.Close()
	api := NewAPI(DeviceConfig{Host: server.URL})
	defer api.Close()
	state, err := api.Read(context.Background())
	if err != nil || state.AirPlay != nil || state.Hostname == nil || *state.Hostname != "host" || state.RTSP == nil {
		t.Fatalf("mismatched optional field broke the read: %+v %v", state, err)
	}
}

func TestSelectsSensorsAndPowerButtons(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, c)
	d := newDevice(t, 1)
	d.modify(func() { d.state.Miracast = nil }) // Older firmware without this field.
	stop := startBridge(t, c, filepath.Join(t.TempDir(), "registry.json"), d)
	defer stop()
	waitOnline(t, o, d)
	id := deviceID(d.state.Serial)
	for key, want := range map[string]string{"airplay": "everyone", "googlecast": "off", "livestream_quality": "medium", "boot_time": "2026-09-28T01:02:03+10:00",
		"firmware": "test", "hostname": "airserver-1", "device_name": "Room 1", "cloud_organization": "None", "timezone": "Australia/Sydney"} {
		if got := o.value(topic(id, key+"/state")); got != want {
			t.Fatalf("%s: got %q, want %q", key, got, want)
		}
		if o.value(topic(id, key+"/availability")) != "online" {
			t.Fatalf("%s not available", key)
		}
	}
	if o.value(topic(id, "miracast/availability")) != "offline" || o.value(topic(id, "miracast/state")) != "" || o.value(topic(id, "availability")) != "online" {
		t.Fatal("missing select field must make only that entity unavailable")
	}
	if d.count() != 0 {
		t.Fatal("startup wrote settings")
	}

	o.publish(t, topic(id, "airplay/set"), "prompt", false)
	eventually(t, func() bool { return o.value(topic(id, "airplay/state")) == "prompt" })
	o.publish(t, topic(id, "livestream_quality/set"), "high", false)
	eventually(t, func() bool { return o.value(topic(id, "livestream_quality/state")) == "high" })
	o.publish(t, topic(id, "airplay/set"), "Everyone", false) // Wrong case is not an option.
	o.publish(t, topic(id, "miracast/set"), "off", false)     // Unsupported on this firmware; write is still attempted but unconfirmed.
	o.publish(t, topic(id, "hostname/set"), "renamed", false) // Sensors are read-only.
	o.publish(t, topic(id, "boot_time/set"), "PRESS", false)
	eventually(t, func() bool { return o.offlineCount(topic(id, "availability")) > 0 })
	waitOnline(t, o, d)
	d.mu.Lock()
	writes := append([]string(nil), d.writes...)
	d.mu.Unlock()
	if strings.Join(writes, ",") != `{"airplay":"prompt"},{"livestreaming_quality":"high"},{"miracast":"off"}` {
		t.Fatalf("unexpected writes: %v", writes)
	}

	d.modify(func() {
		d.state.GoogleCast = stringValue("prompt")
		d.state.BootTime = stringValue("2026-09-29T00:00:00Z")
	})
	eventually(t, func() bool {
		return o.value(topic(id, "googlecast/state")) == "prompt" && o.value(topic(id, "boot_time/state")) == "2026-09-29T00:00:00Z"
	})
	if d.count() != 3 {
		t.Fatal("external change was written back")
	}

	// Reboot and power off are sent once and never read back while the device is going down.
	d.modify(func() { d.state.AirPlay = stringValue("weird-value") })
	eventually(t, func() bool { return o.value(topic(id, "airplay/availability")) == "offline" })
	before := o.offlineCount(topic(id, "availability"))
	o.publish(t, topic(id, "reboot/set"), "PRESS", false)
	eventually(t, func() bool { return d.count() == 4 })
	o.publish(t, topic(id, "power_off/set"), "PRESS", false)
	eventually(t, func() bool { return d.count() == 5 })
	d.mu.Lock()
	writes = append([]string(nil), d.writes[3:]...)
	d.mu.Unlock()
	if strings.Join(writes, ",") != "POST /api/v1/system/reboot,POST /api/v1/system/powerOff" {
		t.Fatalf("unexpected writes: %v", writes)
	}
	time.Sleep(300 * time.Millisecond)
	if o.offlineCount(topic(id, "availability")) != before || o.value(topic(id, "availability")) != "online" {
		t.Fatal("reboot/power off must not report a failed command while the device is still up")
	}
	d.modify(func() { d.status = 503 })
	eventually(t, func() bool { return o.value(topic(id, "availability")) == "offline" })
	d.modify(func() { d.status = 200 })
	waitOnline(t, o, d)
	if d.count() != 5 {
		t.Fatal("power command replayed after outage")
	}
}

func TestRemovedDeviceClearsEveryEntity(t *testing.T) {
	broker, c := startBroker(t, "127.0.0.1:0")
	defer broker.Close()
	o := observe(t, c)
	a, b := newDevice(t, 1), newDevice(t, 2)
	path := filepath.Join(t.TempDir(), "registry.json")
	stop := startBridge(t, c, path, a, b)
	waitOnline(t, o, a, b)
	stop()
	id := deviceID(a.state.Serial)
	stop = startBridge(t, c, path, b)
	defer stop()
	waitOnline(t, o, b)
	eventually(t, func() bool {
		return o.value(discoveryTopic(id, "timezone")) == "" && o.value(discoveryTopic(id, "power_off")) == ""
	})
	for _, ctl := range controls {
		if o.value(discoveryTopic(id, ctl.key)) != "" || o.value(topic(id, ctl.key+"/state")) != "" {
			t.Fatalf("%s survived device removal", ctl.key)
		}
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(o.value(discoveryTopic(deviceID(b.state.Serial), "hostname"))), &config); err != nil {
		t.Fatal(err)
	}
}
