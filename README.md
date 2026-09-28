# AirServer Connect for Home Assistant

A Home Assistant **App**, written in Go and installed through an App repository.
It bridges the AirServer Connect API to MQTT discovery and supports multiple
independent devices.

Each AirServer exposes exactly:

- **Livestream** — on/off.
- **RTSP output** — on/off, independent of Livestream.
- **End session** — disconnects everyone on that device and rotates its Wi-Fi password.

## Installation

Add this repository through **Settings → Apps → Install app → Repositories**:

```text
https://github.com/michaelhsamy/home-assistant-airserver
```

Install **AirServer Connect**, configure the devices in its Configuration tab, and
start it. Requires the Mosquitto broker app and Home Assistant's MQTT integration.
Supports aarch64 and amd64. No HACS installation is needed.

See [setup instructions and the three-device example](airserver/DOCS.md).

## Development

Use Go 1.27.1 or later. The module lives in `airserver/` so Home Assistant can
build it directly from that app directory.

```sh
cd airserver
go test -race -timeout=2m ./...
go vet ./...
go build -o bin/airserver-connect ./cmd/airserver-connect
```

Go tests run isolated HTTPS AirServer simulators and an in-process MQTT broker; they
do not connect to real AirServers. They cover discovery, independent controls,
external state changes, device outages, command timeouts, restart behavior,
retained commands, discovery cleanup, configuration validation, and API failures.

Build the app image with Docker from the repository root:

```sh
docker build --build-arg BUILD_ARCH=amd64 -t airserver-connect:local airserver
```

Home Assistant provides `/data/options.json` and `SUPERVISOR_TOKEN` at runtime.
The image contains a static Go binary and trusted CA certificates. There is no
interpreter or shell in the runtime image. The bridge reads MQTT service
credentials from Supervisor, so a standalone container without Supervisor is
not a supported deployment mode.

Dependencies are recorded in `airserver/go.mod` and verified by `airserver/go.sum`.
After changing dependencies, run tests again. Cross-compile both supported Linux
architectures without Docker from `airserver/`:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/airserver-connect-amd64 ./cmd/airserver-connect
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/airserver-connect-arm64 ./cmd/airserver-connect
```
