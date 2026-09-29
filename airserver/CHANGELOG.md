# Changelog

## 0.2.0

- Add **Reboot** and **Power off** buttons. They are sent once and not read
  back; the device becomes unavailable on the next status check and recovers
  when reachable again.
- Add **AirPlay**, **Google Cast**, **Miracast**, and **Livestream quality**
  selects. Each writes one field and is confirmed by readback like the switches.
- Add diagnostic sensors: last boot (timestamp), firmware version, hostname,
  device name, cloud organization, and time zone.
- A field the firmware does not report, or reports with an unexpected value,
  makes only that entity unavailable instead of the whole device. Livestream
  and RTSP remain required.
- Existing entity identities and MQTT topics are unchanged.

## 0.1.2

- Update Go dependencies (golang.org/x/net, golang.org/x/sync).
- Add staticcheck, govulncheck, and Dependabot to CI.
- Read the app version from config.yaml only.
- Trim documentation.

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
