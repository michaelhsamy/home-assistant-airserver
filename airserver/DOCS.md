# AirServer Connect app

## Requirements

- Home Assistant OS on aarch64 or amd64.
- The **Mosquitto broker** app and the **MQTT integration** with discovery
  enabled (default prefix `homeassistant`). Broker credentials come from
  Supervisor; external brokers are not supported.
- AirServer Connect devices reachable over HTTPS, with the API enabled under
  **Device Management → Security**. Firmware must support
  `GET/PATCH /api/v1/system` with `device_serial`, `livestreaming_enabled`, and
  `livestreaming_rtsp`, plus `POST /api/v1/system/endSession`. Other entities
  use additional fields and endpoints listed below; a device that does not
  report a field shows that entity as unavailable.

## Install

1. Add `https://github.com/michaelhsamy/home-assistant-airserver` under
   **Settings → Apps → Install app → Repositories**.
2. Install **AirServer Connect**. The image builds locally on first install.
3. Open **Configuration**, add your devices, and save. Use **Edit in YAML** if
   the visual editor does not show the device list.
4. Start the app and enable **Start on boot**. Each device appears under
   **Settings → Devices & services → MQTT**.

## Entities

Each device gets these entities under **Settings → Devices & services → MQTT**.
Controls (switches, selects, End session) are primary entities; Reboot, Power
off, and the selects are also listed under the device's **Configuration**
section, and the sensors under **Diagnostic**.

| Entity | Type | AirServer field or endpoint |
| --- | --- | --- |
| Livestream | Switch | `livestreaming_enabled` |
| RTSP output | Switch | `livestreaming_rtsp` |
| End session | Button | `POST /api/v1/system/endSession` |
| Reboot | Button | `POST /api/v1/system/reboot` |
| Power off | Button | `POST /api/v1/system/powerOff` |
| AirPlay | Select: off, everyone, code, password, prompt | `airplay` |
| Google Cast | Select: off, everyone, prompt | `googlecast` |
| Miracast | Select: off, everyone, pin8, pin4, prompt | `miracast` |
| Livestream quality | Select: low, medium, high | `livestreaming_quality` |
| Last boot | Timestamp sensor | `device_system_boot_time` |
| Firmware version | Sensor | `device_system_version` |
| Hostname | Sensor | `device_hostname` |
| Device name | Sensor | `device_name` |
| Cloud organization | Sensor | `cloud_organization` |
| Time zone | Sensor | `device_timezone` |

**End session disconnects all users and rotates the Wi-Fi password. Reboot
and Power off interrupt every session on that AirServer.** After Reboot or
Power off the device becomes unavailable on the next status check and recovers
by itself once it is reachable again; a powered-off device stays unavailable
until it is switched on.

Selecting **password** for AirPlay requires an AirPlay password already set on
the device; AirServer rejects the change otherwise (HTTP 400). Values the
device reports that are not in the list above, and boot times that are not
RFC 3339 timestamps, make only that entity unavailable and are logged once.

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

- Every command writes exactly one setting or calls one endpoint. Switches and
  selects PATCH their single field; sensors are never written.
- Switch and select state is read back after every command, never assumed. If
  the readback does not match, the app logs a warning and marks the device
  unavailable until the next successful poll. The command is not repeated.
  Reboot and Power off are the exception: they are sent once and not read back,
  because the device is shutting down.
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
| One entity unavailable while the device is online | The firmware does not report that field, or reports a value outside the documented options. See the `Entity unavailable` warning in the log. |
| Select change rejected with HTTP 400 | AirServer validates related settings together, for example AirPlay `password` needs an AirPlay password. Change it in the device UI first. |

## References

- [AirServer API enablement](https://support.airserver.com/support/solutions/articles/43000732311-how-to-enable-the-airserver-connect-api)
- [AirServer system API](https://download.airserver.com/doc/connect-api/system/)
- [Home Assistant MQTT discovery](https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery)
