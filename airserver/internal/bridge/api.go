package bridge

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const requestTimeout = 5 * time.Second

type State struct {
	Serial     string `json:"device_serial"`
	Model      string `json:"device_model"`
	Version    string `json:"device_system_version"`
	Livestream *bool  `json:"livestreaming_enabled"`
	RTSP       *bool  `json:"livestreaming_rtsp"`
}

type API struct {
	config DeviceConfig
	client *http.Client
}

func NewAPI(config DeviceConfig) *API {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// This exception is scoped to this device and only enabled by its configuration.
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !config.verifyTLS()}
	return &API{config: config, client: &http.Client{
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
	// Decode only the fields we need; the response may also contain credentials.
	if json.Unmarshal(data, &state) != nil || state.Livestream == nil || state.RTSP == nil {
		return state, errors.New("firmware response is missing livestream/RTSP boolean settings or contains invalid JSON")
	}
	if strings.TrimSpace(state.Serial) == "" {
		return state, errors.New("firmware response is missing device_serial")
	}
	return state, nil
}

func validCommand(control, payload string) bool {
	return (control == "end_session" && payload == "PRESS") ||
		((control == "livestream" || control == "rtsp") && (payload == "ON" || payload == "OFF"))
}

func (a *API) Command(ctx context.Context, control, payload string) error {
	if !validCommand(control, payload) {
		return errors.New("invalid command")
	}
	method, path := http.MethodPatch, ""
	var body []byte
	if control == "end_session" {
		method, path = http.MethodPost, "/endSession"
	} else {
		field := "livestreaming_enabled"
		if control == "rtsp" {
			field = "livestreaming_rtsp"
		}
		body, _ = json.Marshal(map[string]bool{field: payload == "ON"})
	}
	// PATCH/POST are not retried. In particular, do not set an idempotency header.
	_, err := a.request(ctx, method, path, body)
	return err
}
