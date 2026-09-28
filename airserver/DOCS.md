# AirServer Connect app

## Requirements

- Home Assistant OS on aarch64 or amd64, with access to the App store.
- The **Mosquitto broker app**, running, and Home Assistant's **MQTT integration**
  configured with discovery enabled (default discovery prefix: `homeassistant`).
  The app obtains broker credentials from Supervisor's MQTT service; you do not
  enter them again. An external broker without a Supervisor MQTT service is not
  supported in this version.
- AirServer Connect devices reachable from Home Assistant over HTTPS. Enable the
  API under **Device Management → Security** and copy each device's API key.
- Firmware implementing `GET/PATCH /api/v1/system`, the `device_serial`,
  `livestreaming_enabled`, and `livestreaming_rtsp` fields, and
  `POST /api/v1/system/endSession`. Use current firmware for Connect 1. The API's
  original introduction in firmware 2024.07.12 does **not** establish support for
  all of these later capabilities. Missing state fields are reported in the logs;
  an unsupported End session endpoint will report an HTTP error when pressed.
  The optional service-check mode reads streaming state from the listeners
  instead of the REST boolean fields, but still requires the API for device
  identity and commands.

## Install

1. Open **Settings → Apps → Install app → Repositories** (the repository menu
   location can vary with Home Assistant version).
2. Add `https://github.com/michaelhsamy/home-assistant-airserver`.
3. Refresh the store, select **AirServer Connect**, and install it. Home Assistant
   builds the image locally on first installation; this can take a few minutes.
4. Open **Configuration**, enter your devices, and save. Use **Edit in YAML** if
   the visual editor does not expose the device list.
5. Start the app and enable **Start on boot**. Check its logs for each device's
   connected message.
6. Open **Settings → Devices & services → MQTT**. Each AirServer appears as its own
   device with three entities, ready to add to dashboards or automations.

## Example configuration: three independent devices

```yaml
devices:
  - name: Meeting room
    host: https://192-168-1-101.int.airserver.com
    api_key: REPLACE_WITH_FIRST_DEVICE_API_KEY
    verify_ssl: true
  - name: Lounge
    host: https://192-168-1-102.int.airserver.com
    api_key: REPLACE_WITH_SECOND_DEVICE_API_KEY
    verify_ssl: true
  - name: Training room
    host: https://192-168-1-103.int.airserver.com
    api_key: REPLACE_WITH_THIRD_DEVICE_API_KEY
    verify_ssl: true
poll_interval: 5
```

Replace the example hostnames with your devices' actual addresses. AirServer
documents an IP-derived `*.int.airserver.com` hostname; use the hostname shown by
your device's web-management redirect, or a hostname with your own trusted
certificate. A hostname, IPv4 address, or bracketed IPv6 address is accepted,
optionally with a port. HTTPS is always used. Do not include an API path.

`verify_ssl` defaults to `true` if omitted. If you deliberately connect to a
device using an untrusted self-signed certificate, set it to `false` for **that
device only**. Traffic remains encrypted, but the server certificate is not
authenticated. MQTT certificate verification is unaffected.

Each API key belongs to its corresponding device. Keep keys in the app
configuration, not in this Git repository. Restart the app after configuration
changes. `poll_interval` is in seconds (2–300, default 5).

## State source: API or service checks

Each device accepts an optional `state_source`, defaulting to `api`. If its REST
API reports stale settings, select `services` in Configuration or add this to
that device's YAML entry, then restart the app:

```yaml
state_source: services
```

Service-check mode keeps the same entities and API commands. It determines
Livestream state by opening and immediately closing the secure WebSocket at
`/live/ws/`, and RTSP state by connecting to TCP port **1554** on the configured
device's hostname/IP. It requires no Uptime Kuma integration, livestream
password, or media playback. HTTPS/WebSocket checks respect `verify_ssl`.

| Observed services | Livestream switch | RTSP switch |
| --- | --- | --- |
| Livestream WebSocket accepts a connection; RTSP port accepts a connection | ON | ON |
| Livestream WebSocket accepts a connection; RTSP port refuses the connection | ON | OFF |
| Livestream backend is stopped (WebSocket HTTP 502) | OFF | Unavailable |
| Timeout, TLS failure, unsupported endpoint, or other probe error | Unavailable | Unavailable |

**Enable Livestream before changing RTSP in this mode.** The device closes port
1554 when Livestream is off even if the saved RTSP setting is enabled. The app
therefore cannot infer or confirm RTSP's setting in that condition, and rejects
RTSP commands until Livestream is available. The End session button remains
available while the device is connected.

These states describe service availability, not guaranteed saved settings or
playable video. A failed livestream backend can also return HTTP 502; a failed
RTSP listener can refuse connections. The checks were verified on Connect 2
firmware **2026.03.13**. The livestream WebSocket is used by AirServer's browser
player and is not part of its documented REST API, so other firmware may behave
differently. An ordinary HTTP check of `/` or `/live` cannot determine whether
Livestream is enabled: both pages remain accessible with it disabled.

After a command, service-check mode allows up to five seconds of read-only
checks for the listener to start or stop. It does not publish the previous state
during that confirmation period and never repeats the command.

## Controls and behavior

| Entity | API command |
| --- | --- |
| Livestream | Writes only `livestreaming_enabled` |
| RTSP output | Writes only `livestreaming_rtsp` |
| End session | Calls `/api/v1/system/endSession` for this device |

**End session disconnects all currently connected users on the selected AirServer
and rotates its Wi-Fi password.** It is an immediate button action. If you want
a dashboard confirmation, configure one on your dashboard button.

In the default API mode, the switches read the corresponding independent
configuration settings. Enabling RTSP does not enable Livestream automatically.
The app does not claim that an enabled setting
means video is actively flowing; actual streaming behavior depends on AirServer.
There is no video playback, collective control, or additional sensor entity.

- Changes made in AirServer's own interface are reflected on the next poll,
  provided the selected state source reports them correctly.
- Switch states are checked using the selected state source after a command,
  never assumed.
- If AirServer accepts a switch command but the readback does not match, the app
  logs the requested and reported values and marks the device unavailable until
  the next successful poll. It does not repeat the command. A successful HTTP
  response alone does not confirm that a setting took effect.
- A failed device becomes unavailable; the other devices continue working.
- A request times out after five seconds. Failed commands are never retried or
  queued for reconnection. If another command is already running on that device,
  additional commands are rejected and logged. A command may briefly wait for
  an in-progress status read, then is sent only if that device remains available.
- A timed-out request may already have taken effect on the device. The next
  successful poll reconciles state; End session is never repeated automatically.
- App and broker restarts restore discovery and read current settings. They do
  not write settings or end sessions. Commands use a clean MQTT session, QoS 0,
  and no retain flag; retained commands received at subscription are ignored.
- Availability requires both the app and device to be online. An app crash or
  network break is detected through MQTT's last will (not necessarily instantly).
- Entity identities use AirServer serial numbers, so friendly-name or address
  changes preserve them. Use a unique device address per physical AirServer.
- Removing a device from configuration removes its retained MQTT discovery on
  the next app start. The local registry stores only hashes, never API keys or
  full API responses. After uninstalling the whole app, MQTT entities may remain
  unavailable until removed manually from Home Assistant.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| No devices appear | Start Mosquitto, configure the MQTT integration, enable discovery with the default prefix, then check this app's logs. |
| MQTT unavailable | The broker must expose Supervisor's MQTT service; restart the broker and this app. |
| HTTP 401/403 | Check the API key and API permissions on that AirServer. |
| HTTP 404 or missing settings | Enable the API and check that firmware supports the required endpoints/fields. |
| Certificate verification failed | Use the correct trusted hostname/certificate, or explicitly disable verification for that device's self-signed certificate. |
| Device unreachable | Check the address, port, routing/firewall access from Home Assistant, and whether AirServer is on. |
| A button had no visible result | Check the app logs for rejected/failed commands. MQTT buttons have no confirmed result state. |
| A switch snaps back or disagrees with AirServer's UI | In API mode, look for an `accepted ... but its API still reports ...` warning. Try `state_source: services` for stale streaming fields, subject to the service-check limitations above. |
| RTSP is unavailable in service-check mode | Enable Livestream first; its disabled state prevents the app from observing RTSP's saved setting. |

## References

- [AirServer API enablement](https://support.airserver.com/support/solutions/articles/43000732311-how-to-enable-the-airserver-connect-api)
- [AirServer system API](https://download.airserver.com/doc/connect-api/system/)
- [AirServer TLS](https://download.airserver.com/doc/connect-api/tls/)
- [Home Assistant MQTT discovery](https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery)
