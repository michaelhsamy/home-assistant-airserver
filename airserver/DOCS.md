# AirServer Connect app

## Requirements

- Home Assistant OS on aarch64 or amd64.
- The **Mosquitto broker** app and the **MQTT integration** with discovery
  enabled (default prefix `homeassistant`). Broker credentials come from
  Supervisor; external brokers are not supported.
- AirServer Connect devices reachable over HTTPS, with the API enabled under
  **Device Management → Security**. Firmware must support
  `GET/PATCH /api/v1/system` with `device_serial`, `livestreaming_enabled`, and
  `livestreaming_rtsp`, plus `POST /api/v1/system/endSession`.

## Install

1. Add `https://github.com/michaelhsamy/home-assistant-airserver` under
   **Settings → Apps → Install app → Repositories**.
2. Install **AirServer Connect**. The image builds locally on first install.
3. Open **Configuration**, add your devices, and save. Use **Edit in YAML** if
   the visual editor does not show the device list.
4. Start the app and enable **Start on boot**. Each device appears under
   **Settings → Devices & services → MQTT**.

## Configuration

```yaml
devices:
  - name: Meeting room
    host: https://192-168-1-101.int.airserver.com
    api_key: REPLACE_WITH_API_KEY
  - name: Lounge
    host: 192.168.1.102
    api_key: REPLACE_WITH_API_KEY
    verify_ssl: false
    state_source: services
poll_interval: 5
```

| Option | Description |
| --- | --- |
| `name` | Friendly name. Entity identities use the device serial, so renaming is safe. |
| `host` | Hostname, IPv4, or bracketed IPv6 address, optionally with a port. HTTPS is always used. No API path. |
| `api_key` | The device's API key. |
| `verify_ssl` | Default `true`. Set `false` only for a device with an untrusted self-signed certificate. |
| `state_source` | `api` (default) reads switch state from the REST API. `services` probes the listeners instead; see below. |
| `poll_interval` | Seconds between status checks, 2–300. Default 5. |

Restart the app after changing configuration. Use a unique address per
physical AirServer.

## State source: services

Some firmware (observed on Connect 2, 2026.03.13) accepts switch commands but
keeps reporting stale streaming state over REST. `state_source: services` works
around this by opening and closing the WebSocket at `/live/ws/` for Livestream
and connecting to TCP port **1554** for RTSP. Commands and device identity still
use the API. Probes respect `verify_ssl`.

| Observed services | Livestream | RTSP |
| --- | --- | --- |
| WebSocket accepts; port 1554 accepts | ON | ON |
| WebSocket accepts; port 1554 refuses | ON | OFF |
| WebSocket returns HTTP 502 | OFF | Unavailable |
| Timeout, TLS failure, or other probe error | Unavailable | Unavailable |

**Enable Livestream before changing RTSP in this mode.** Port 1554 is closed
while Livestream is off, so the RTSP setting cannot be observed and RTSP
commands are rejected. After a command, the app allows up to five seconds for
the listener to settle before reporting a mismatch.

## Behavior

- Livestream writes only `livestreaming_enabled`; RTSP writes only
  `livestreaming_rtsp`; End session calls `/api/v1/system/endSession`.
- Switch state is read back after every command, never assumed. If the readback
  does not match, the app logs a warning and marks the device unavailable until
  the next successful poll. The command is not repeated.
- Requests time out after five seconds. Failed or timed-out commands are never
  retried or queued. A second command on a busy device is rejected and logged.
- Restarts of the app or broker restore discovery and read current state; they
  never write settings or end sessions. Retained commands are ignored.
- A failed device becomes unavailable; other devices keep working.
- Removing a device from configuration clears its MQTT discovery on the next
  start. The local registry stores only hashes, never API keys.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| No devices appear | Mosquitto is running, the MQTT integration is configured with discovery, and the app logs show each device connected. |
| HTTP 401/403 | API key and API permissions on that AirServer. |
| HTTP 404 or missing settings | The API is enabled and the firmware supports the required endpoints. |
| Certificate verification failed | Use the device's trusted hostname, or set `verify_ssl: false` for that device. |
| Device unreachable | Address, port, and network access from Home Assistant. |
| A switch snaps back | Look for an `accepted ... but its API still reports ...` warning and try `state_source: services`. |
| RTSP unavailable in services mode | Enable Livestream first. |

## References

- [AirServer API enablement](https://support.airserver.com/support/solutions/articles/43000732311-how-to-enable-the-airserver-connect-api)
- [AirServer system API](https://download.airserver.com/doc/connect-api/system/)
- [Home Assistant MQTT discovery](https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery)
