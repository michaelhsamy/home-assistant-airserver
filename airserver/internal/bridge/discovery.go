package bridge

import (
	"slices"
	"strings"
	"time"
)

const root = "airserver_connect"
const bridgeAvailability = root + "/bridge/availability"

// A control is one Home Assistant entity per device. Its key names the MQTT
// topics and the unique_id suffix, so keys are part of the entity identity.
type control struct {
	key, domain, name, icon string
	field                   string   // API field for switches and selects, POST path for buttons
	options                 []string // permitted select values
	category, deviceClass   string
	value                   func(State) *string
}

func (c control) writable() bool {
	return c.domain == "switch" || c.domain == "select" || c.domain == "button"
}

// reading returns the MQTT state payload for this control, or nil when the
// device does not report a usable value and the entity must be unavailable.
func (c control) reading(state State) *string {
	if c.value == nil {
		return nil
	}
	v := c.value(state)
	if v == nil {
		return nil
	}
	if c.domain == "select" && !slices.Contains(c.options, *v) {
		return nil
	}
	if c.deviceClass == "timestamp" {
		return timestamp(*v)
	}
	if c.domain == "sensor" && *v == "" {
		none := "None" // Home Assistant's MQTT sensor payload for unknown.
		return &none
	}
	return v
}

func switchValue(read func(State) *bool) func(State) *string {
	return func(s State) *string {
		v := read(s)
		if v == nil {
			return nil
		}
		payload := "OFF"
		if *v {
			payload = "ON"
		}
		return &payload
	}
}

func stringValue(s string) *string { return &s }

// timestamp normalizes an RFC 3339 time for Home Assistant's timestamp device
// class, which rejects values without a UTC offset.
func timestamp(raw string) *string {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return stringValue(t.Format(time.RFC3339))
		}
	}
	return nil
}

var controls = []control{
	{key: "livestream", domain: "switch", name: "Livestream", icon: "mdi:broadcast", field: "livestreaming_enabled",
		value: switchValue(func(s State) *bool { return s.Livestream })},
	{key: "rtsp", domain: "switch", name: "RTSP output", icon: "mdi:video-wireless", field: "livestreaming_rtsp",
		value: switchValue(func(s State) *bool { return s.RTSP })},
	{key: "end_session", domain: "button", name: "End session", icon: "mdi:cast-off", field: "/endSession"},
	{key: "reboot", domain: "button", name: "Reboot", icon: "mdi:restart", field: "/reboot", category: "config", deviceClass: "restart"},
	{key: "power_off", domain: "button", name: "Power off", icon: "mdi:power", field: "/powerOff", category: "config"},
	{key: "airplay", domain: "select", name: "AirPlay", icon: "mdi:apple", field: "airplay",
		options: []string{"off", "everyone", "code", "password", "prompt"}, category: "config", value: func(s State) *string { return s.AirPlay }},
	{key: "googlecast", domain: "select", name: "Google Cast", icon: "mdi:cast", field: "googlecast",
		options: []string{"off", "everyone", "prompt"}, category: "config", value: func(s State) *string { return s.GoogleCast }},
	{key: "miracast", domain: "select", name: "Miracast", icon: "mdi:monitor-share", field: "miracast",
		options: []string{"off", "everyone", "pin8", "pin4", "prompt"}, category: "config", value: func(s State) *string { return s.Miracast }},
	{key: "livestream_quality", domain: "select", name: "Livestream quality", icon: "mdi:quality-high", field: "livestreaming_quality",
		options: []string{"low", "medium", "high"}, category: "config", value: func(s State) *string { return s.Quality }},
	{key: "boot_time", domain: "sensor", name: "Last boot", icon: "mdi:clock-start", category: "diagnostic", deviceClass: "timestamp",
		value: func(s State) *string { return s.BootTime }},
	{key: "firmware", domain: "sensor", name: "Firmware version", icon: "mdi:chip", category: "diagnostic",
		value: func(s State) *string { return stringValue(s.Version) }},
	{key: "hostname", domain: "sensor", name: "Hostname", icon: "mdi:server-network", category: "diagnostic",
		value: func(s State) *string { return s.Hostname }},
	{key: "device_name", domain: "sensor", name: "Device name", icon: "mdi:rename-box", category: "diagnostic",
		value: func(s State) *string { return s.DeviceName }},
	{key: "cloud_organization", domain: "sensor", name: "Cloud organization", icon: "mdi:cloud-outline", category: "diagnostic",
		value: func(s State) *string { return s.Organization }},
	{key: "timezone", domain: "sensor", name: "Time zone", icon: "mdi:map-clock", category: "diagnostic",
		value: func(s State) *string { return s.Timezone }},
}

func findControl(key string) (control, bool) {
	for _, c := range controls {
		if c.key == key {
			return c, true
		}
	}
	return control{}, false
}

func deviceID(serial string) string  { return "airserver_" + hash(strings.TrimSpace(serial))[:24] }
func topic(id, suffix string) string { return root + "/" + id + "/" + suffix }
func discoveryTopic(id, key string) string {
	c, _ := findControl(key)
	return "homeassistant/" + c.domain + "/" + id + "/" + key + "/config"
}

func discovery(id, name string, state State) map[string]map[string]any {
	device := map[string]any{"identifiers": []string{id}, "name": name, "manufacturer": "AirServer"}
	if state.Model != "" {
		device["model"] = state.Model
	}
	if state.Version != "" {
		device["sw_version"] = state.Version
	}
	result := make(map[string]map[string]any)
	for _, c := range controls {
		config := map[string]any{
			"name": c.name, "unique_id": id + "_" + c.key, "device": device, "icon": c.icon, "qos": 0,
			"availability":      []map[string]string{{"topic": bridgeAvailability}, {"topic": topic(id, "availability")}},
			"availability_mode": "all",
		}
		if c.category != "" {
			config["entity_category"] = c.category
		}
		if c.deviceClass != "" {
			config["device_class"] = c.deviceClass
		}
		if c.writable() {
			config["command_topic"], config["retain"] = topic(id, c.key+"/set"), false
		}
		switch c.domain {
		case "button":
			config["payload_press"] = "PRESS"
		case "switch":
			config["payload_on"], config["payload_off"], config["optimistic"] = "ON", "OFF", false
		case "select":
			config["options"], config["optimistic"] = c.options, false
		}
		if c.value != nil {
			config["state_topic"] = topic(id, c.key+"/state")
			config["availability"] = append(config["availability"].([]map[string]string), map[string]string{"topic": topic(id, c.key+"/availability")})
		}
		result[discoveryTopic(id, c.key)] = config
	}
	return result
}
