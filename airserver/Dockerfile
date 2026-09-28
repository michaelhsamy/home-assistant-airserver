FROM golang:1.27.1-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG BUILD_ARCH
RUN case "${BUILD_ARCH}" in \
      aarch64) target_arch=arm64 ;; \
      amd64) target_arch=amd64 ;; \
      *) target_arch="$(go env GOARCH)" ;; \
    esac && \
    CGO_ENABLED=0 GOOS=linux GOARCH="${target_arch}" \
      go build -trimpath -ldflags="-s -w" -o /airserver-connect ./cmd/airserver-connect

FROM scratch

ARG BUILD_VERSION=0.1.1
ARG BUILD_ARCH
LABEL io.hass.name="AirServer Connect" \
      io.hass.description="AirServer Connect controls over MQTT" \
      io.hass.type="app" \
      io.hass.version="${BUILD_VERSION}" \
      io.hass.arch="${BUILD_ARCH}"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /airserver-connect /airserver-connect
ENTRYPOINT ["/airserver-connect"]
