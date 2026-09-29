package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
)

const requestTimeout = 5 * time.Second

// State holds the subset of GET /api/v1/system that the bridge exposes. The
// response also contains credentials, which are never decoded.
type State struct {
	Serial       string  `json:"device_serial"`
	Model        string  `json:"device_model"`
	Version      string  `json:"device_system_version"`
	Livestream   *bool   `json:"livestreaming_enabled"`
	RTSP         *bool   `json:"livestreaming_rtsp"`
	AirPlay      *string `json:"airplay"`
	GoogleCast   *string `json:"googlecast"`
	Miracast     *string `json:"miracast"`
	Quality      *string `json:"livestreaming_quality"`
	BootTime     *string `json:"device_system_boot_time"`
	Hostname     *string `json:"device_hostname"`
	DeviceName   *string `json:"device_name"`
	Organization *string `json:"cloud_organization"`
	Timezone     *string `json:"device_timezone"`
}

type API struct {
	config      DeviceConfig
	client      *http.Client
	rtspAddress string
}

func NewAPI(config DeviceConfig) *API {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// This exception is scoped to this device and only enabled by its configuration.
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !config.verifyTLS()}
	host, _ := url.Parse(config.Host)
	rtspAddress := ""
	if host != nil {
		rtspAddress = net.JoinHostPort(host.Hostname(), "1554")
	}
	return &API{config: config, rtspAddress: rtspAddress, client: &http.Client{
		Timeout: requestTimeout, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}
func (a *API) Close() { a.client.CloseIdleConnections() }

func (a *API) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.config.Host+"/api/v1/system"+path, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid device request")
	}
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(req)
	if err != nil {
		var cert *tls.CertificateVerificationError
		if errors.As(err, &cert) {
			return nil, errors.New("certificate verification failed; check hostname/certificate or verify_ssl")
		}
		return nil, errors.New("device unreachable or request timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 204 {
		hint := map[int]string{400: "device rejected the requested setting", 401: "check the API key", 403: "check API permissions", 404: "check API enablement and firmware support"}[response.StatusCode]
		if hint == "" {
			hint = "request failed"
		}
		return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, hint)
	}
	if method != http.MethodGet {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, errors.New("could not read device state")
	}
	return data, nil
}

func (a *API) Read(ctx context.Context) (State, error) {
	var state State
	data, err := a.request(ctx, http.MethodGet, "", nil)
	if err != nil {
		return state, err
	}
	state, err = decodeState(data)
	if err != nil {
		return state, err
	}
	if strings.TrimSpace(state.Serial) == "" {
		return state, errors.New("firmware response is missing device_serial")
	}
	if a.config.StateSource == "services" {
		// Service mode deliberately ignores the firmware's streaming fields,
		// including missing or invalid values.
		state.Livestream, state.RTSP, err = a.readServices(ctx)
	} else if state.Livestream == nil || state.RTSP == nil {
		return state, errors.New("firmware response is missing livestream/RTSP boolean settings")
	}
	return state, err
}

// decodeState reads only State's tagged fields from a JSON object. A field
// whose value has the wrong type stays unset so that only its entity becomes
// unavailable, rather than the whole device.
func decodeState(data []byte) (State, error) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || raw == nil {
		return State{}, errors.New("firmware response is not a JSON object")
	}
	var state State
	fields, values := reflect.TypeOf(state), reflect.ValueOf(&state).Elem()
	for i := range fields.NumField() {
		encoded, ok := raw[fields.Field(i).Tag.Get("json")]
		if !ok {
			continue
		}
		target := reflect.New(fields.Field(i).Type)
		if json.Unmarshal(encoded, target.Interface()) == nil {
			values.Field(i).Set(target.Elem())
		}
	}
	return state, nil
}

func validCommand(key, payload string) bool {
	c, ok := findControl(key)
	if !ok || !c.writable() {
		return false
	}
	switch c.domain {
	case "button":
		return payload == "PRESS"
	case "switch":
		return payload == "ON" || payload == "OFF"
	default:
		return slices.Contains(c.options, payload)
	}
}

func (a *API) Command(ctx context.Context, key, payload string) error {
	if !validCommand(key, payload) {
		return errors.New("invalid command")
	}
	c, _ := findControl(key)
	method, path := http.MethodPatch, ""
	var body []byte
	switch c.domain {
	case "button":
		method, path = http.MethodPost, c.field
	case "switch":
		body, _ = json.Marshal(map[string]bool{c.field: payload == "ON"})
	default:
		body, _ = json.Marshal(map[string]string{c.field: payload})
	}
	// PATCH/POST are not retried. In particular, do not set an idempotency header.
	_, err := a.request(ctx, method, path, body)
	return err
}
