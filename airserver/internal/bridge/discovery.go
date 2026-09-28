package bridge

import "strings"

const root = "airserver_connect"
const bridgeAvailability = root + "/bridge/availability"

var controls = []string{"livestream", "rtsp", "end_session"}

func deviceID(serial string) string  { return "airserver_" + hash(strings.TrimSpace(serial))[:24] }
func topic(id, suffix string) string { return root + "/" + id + "/" + suffix }
func discoveryTopic(id, control string) string {
	domain := "switch"
	if control == "end_session" {
		domain = "button"
	}
	return "homeassistant/" + domain + "/" + id + "/" + control + "/config"
}

func discovery(id, name string, state State) map[string]map[string]any {
	device := map[string]any{"identifiers": []string{id}, "name": name, "manufacturer": "AirServer"}
	if state.Model != "" {
		device["model"] = state.Model
	}
	if state.Version != "" {
		device["sw_version"] = state.Version
	}
	names := map[string]string{"livestream": "Livestream", "rtsp": "RTSP output", "end_session": "End session"}
	icons := map[string]string{"livestream": "mdi:broadcast", "rtsp": "mdi:video-wireless", "end_session": "mdi:cast-off"}
	result := make(map[string]map[string]any)
	for _, control := range controls {
		config := map[string]any{
			"name": names[control], "unique_id": id + "_" + control, "device": device, "icon": icons[control],
			"command_topic": topic(id, control+"/set"), "qos": 0, "retain": false,
			"availability":      []map[string]string{{"topic": bridgeAvailability}, {"topic": topic(id, "availability")}},
			"availability_mode": "all",
		}
		if control == "end_session" {
			config["payload_press"] = "PRESS"
		} else {
			config["state_topic"], config["payload_on"], config["payload_off"], config["optimistic"] = topic(id, control+"/state"), "ON", "OFF", false
			config["availability"] = append(config["availability"].([]map[string]string), map[string]string{"topic": topic(id, control+"/availability")})
		}
		result[discoveryTopic(id, control)] = config
	}
	return result
}
