# Changelog

## 0.1.1

- Add optional `state_source: services` per device to work around stale REST
  streaming state using the Livestream WebSocket and RTSP TCP port 1554.
- Mark RTSP unavailable while Livestream is off, because the closed listener
  cannot reveal the saved RTSP setting. Keep existing entity identities.
- Allow service listeners up to five seconds to settle after commands without
  repeating writes or publishing the previous state during confirmation.
- Detect switch commands that AirServer accepts without returning the requested
  state. Log the mismatch and mark the device unavailable until the next
  successful poll, without retrying the command.

## 0.1.0

- Go implementation with a static binary and a minimal container image.
- Install as a Home Assistant App from a GitHub repository.
- Configure multiple AirServer Connect devices independently.
- Expose Livestream and RTSP output switches and an End session button for each device through MQTT discovery.
- Confirm switch states from AirServer and recover independently from device outages.
- Restore discovery after Home Assistant or MQTT restarts without replaying commands.
