# AirServer Connect

Control multiple AirServer Connect devices independently from Home Assistant.
Each device exposes a **Livestream** switch, an **RTSP output** switch, and an
**End session** button using MQTT discovery.

**End session disconnects all users on that AirServer and rotates its Wi-Fi
password.** It does not affect the other AirServers.

Configure the device addresses and API keys in the app's Configuration tab.
For firmware with stale streaming state, optional `state_source: services`
checks the actual listeners. RTSP is unavailable while Livestream is off.
See the Documentation tab for setup, requirements, and troubleshooting.
