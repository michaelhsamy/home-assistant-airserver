# AirServer Connect for Home Assistant

A Home Assistant app that bridges AirServer Connect devices to MQTT discovery.
Each device gets **Livestream** and **RTSP output** switches; **End session**,
**Reboot**, and **Power off** buttons; **AirPlay**, **Google Cast**,
**Miracast**, and **Livestream quality** selects; and diagnostic sensors for
last boot, firmware, hostname, device name, cloud organization, and time zone.

## Installation

Add this repository under **Settings → Apps → Install app → Repositories**:

```text
https://github.com/michaelhsamy/home-assistant-airserver
```

Install **AirServer Connect**, configure your devices, and start it. Requires the
Mosquitto broker app and the MQTT integration. See [DOCS.md](airserver/DOCS.md).

## Development

Requires Go 1.27.1. From `airserver/`:

```sh
go test -race -timeout=2m ./...
docker build --build-arg BUILD_ARCH=amd64 --build-arg BUILD_VERSION=local -t airserver-connect:local .
```

Tests use an in-process MQTT broker and simulated devices. The app reads
`/data/options.json` and `SUPERVISOR_TOKEN` at runtime, so it only runs under
Home Assistant Supervisor.
