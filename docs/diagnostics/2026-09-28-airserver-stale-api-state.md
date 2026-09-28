# AirServer streaming state does not update through the REST API

Observed on 2026-09-28 on an AirServer Connect 2 (`AirServer-Connect-Q2`),
firmware `2026.03.13`, with Home Assistant `2026.9.3`. Device addresses, serial
numbers and credentials are omitted.

## Reproduction and observations

1. Home Assistant sent `ON` to the Livestream command topic and `OFF` to the
   RTSP command topic. Subsequent state publications continued to report
   Livestream `OFF` and RTSP `ON` every five seconds.
2. The device's freshly reloaded management UI showed Livestream ON and RTSP
   OFF. Its serial number matched the identity used by the app.
3. A direct authenticated `GET /api/v1/system`, independently of Home
   Assistant and the bridge, returned:

   ```json
   {"livestreaming_enabled": false, "livestreaming_rtsp": true}
   ```

   A different query string and `Cache-Control: no-cache` did not change this.
4. A direct `PATCH /api/v1/system` with
   `{"livestreaming_rtsp":false}` returned HTTP 204, but immediate, one-second
   and five-second readbacks still reported RTSP true.
5. The device was restarted once, with the owner's approval. Its boot-time
   field changed, confirming the restart. After reconnecting, the management
   UI, REST API and Home Assistant all showed both settings OFF.
6. Turning Livestream ON through Home Assistant changed the management UI to
   ON. The REST API still reported false across three reads spaced three
   seconds apart. Home Assistant's switch returned to OFF.
7. Direct Livestream OFF and ON commands each returned HTTP 204. For each
   command, a long-poll request to `GET /api/v1/events` was started beforehand
   using the system response's ETag in `If-None-Match`. Neither request returned
   an event within the four-second diagnostic timeout. Subsequent system reads
   still reported both settings false and the ETag had not changed. This test
   does not rule out events arriving after the diagnostic timeout.

The final requested device settings were Livestream ON and RTSP OFF.

## Expected behavior

After a successful PATCH and any necessary processing delay, the system
endpoint should reflect the settings shown in the management UI, and the
events endpoint should notify clients of the changes.

The vendor documents the system endpoint as the current device configuration
and documents `system.changed` events for configuration updates:

- [System API](https://download.airserver.com/doc/connect-api/system/)
- [Events API](https://download.airserver.com/doc/connect-api/events/)

Inspection of this device's management JavaScript showed that its UI reads
`liveStreamEnabled` and `liveStreamRtspEnabled` from the separate
`/configtree/display` WebSocket subscription. The UI and REST API therefore
provide two independently observable views of the settings.

## Conclusion and bridge change

Commands reach the device and change its UI, while REST readback becomes stale.
The mismatch reproduces without Home Assistant or the MQTT bridge, and returns
after a device restart. The internal firmware cause remains undetermined.

The bridge also lacked a comparison between the requested value and readback.
The local change adds that comparison, reports a mismatch, and uses the existing
unavailable/recovery behavior without repeating the write. Regression tests cover
RTSP OFF and Livestream ON receiving HTTP success with unchanged API state.

This diagnostic improvement does not correct the stale device API or claim that
the command failed to affect the device.

## Independently verified service checks

Further tests inspected the livestream player's JavaScript and checked its
`/live/ws/` WebSocket alongside TCP port 1554. Each switch combination was
confirmed in the device management UI:

| Livestream setting | RTSP setting | WebSocket response | TCP 1554 |
| --- | --- | --- | --- |
| OFF | OFF | HTTP 502 | Connection refused |
| OFF | ON | HTTP 502 | Connection refused |
| ON | ON | HTTP 101 upgrade | Open |
| ON | OFF | HTTP 101 upgrade | Connection refused |

The `/live` page returned HTTP 200 with identical content in all four cases.
The device was restored to Livestream ON and RTSP OFF after testing.

An opt-in `state_source: services` workaround now uses these probes. It avoids
the stale REST streaming fields while retaining authenticated API commands and
device identity. RTSP is unavailable when Livestream is off; commands for RTSP
are rejected in that condition because its saved setting cannot be observed.
The probes indicate service availability, not saved settings or media quality.
This remains local code and has not been deployed to Home Assistant.
