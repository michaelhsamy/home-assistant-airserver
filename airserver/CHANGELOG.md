# Changelog

## 0.1.0

- Go implementation with a static binary and a minimal container image.
- Install as a Home Assistant App from a GitHub repository.
- Configure multiple AirServer Connect devices independently.
- Expose Livestream and RTSP output switches and an End session button for each device through MQTT discovery.
- Confirm switch states from AirServer and recover independently from device outages.
- Restore discovery after Home Assistant or MQTT restarts without replaying commands.
