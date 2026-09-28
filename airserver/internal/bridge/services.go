package bridge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"

	"github.com/gorilla/websocket"
)

// readServices checks the actual listeners without authenticating to a stream
// or negotiating media. These are service states, not saved configuration flags.
func (a *API) readServices(ctx context.Context) (*bool, *bool, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	endpoint, err := url.Parse(a.config.Host)
	if err != nil {
		return nil, nil, errors.New("invalid livestream service address")
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = "/live/ws/"
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		TLSClientConfig:  a.client.Transport.(*http.Transport).TLSClientConfig,
		HandshakeTimeout: requestTimeout,
	}
	connection, response, err := dialer.DialContext(ctx, endpoint.String(), nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		// On the tested firmware, nginx returns 502 when the livestream
		// backend is stopped. A timeout or unsupported endpoint is unknown.
		if response != nil && response.StatusCode == http.StatusBadGateway {
			live := false
			return &live, nil, nil
		}
		if response != nil {
			return nil, nil, fmt.Errorf("livestream service probe returned HTTP %d; check firmware support for /live/ws/", response.StatusCode)
		}
		return nil, nil, errors.New("livestream service probe failed; check connectivity and TLS configuration")
	}
	connection.Close()
	live := true
	rtsp := false
	tcp, err := (&net.Dialer{}).DialContext(ctx, "tcp", a.rtspAddress)
	if err == nil {
		tcp.Close()
		rtsp = true
	} else if !errors.Is(err, syscall.ECONNREFUSED) {
		return nil, nil, errors.New("RTSP service probe failed; port 1554 is unreachable or timed out")
	}
	return &live, &rtsp, nil
}
