package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type DeviceConfig struct {
	Name        string `json:"name"`
	Host        string `json:"host"`
	APIKey      string `json:"api_key"`
	VerifySSL   *bool  `json:"verify_ssl,omitempty"`
	StateSource string `json:"state_source,omitempty"`
}

// Avoid credentials in accidental formatted logs.
func (d DeviceConfig) String() string   { return d.Name }
func (d DeviceConfig) GoString() string { return d.Name }
func (d DeviceConfig) verifyTLS() bool  { return d.VerifySSL == nil || *d.VerifySSL }
func (d DeviceConfig) cacheKey() string { return hash(d.Host) }
func hash(s string) string              { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

type Options struct {
	Devices      []DeviceConfig `json:"devices"`
	PollInterval int            `json:"poll_interval"`
}

func ParseOptions(data []byte) (Options, error) {
	o := Options{PollInterval: 5}
	if json.Unmarshal(data, &o) != nil {
		return o, errors.New("invalid app configuration JSON")
	}
	if len(o.Devices) == 0 {
		return o, errors.New("add at least one AirServer in Configuration > devices")
	}
	if o.PollInterval < 2 || o.PollInterval > 300 {
		return o, errors.New("poll_interval must be between 2 and 300 seconds")
	}
	hosts := make(map[string]bool)
	for i := range o.Devices {
		d := &o.Devices[i]
		d.Name, d.APIKey = strings.TrimSpace(d.Name), strings.TrimSpace(d.APIKey)
		if d.StateSource != "" && d.StateSource != "api" && d.StateSource != "services" {
			return o, fmt.Errorf("device %d: state_source must be api or services", i+1)
		}
		if d.Name == "" || d.APIKey == "" {
			return o, fmt.Errorf("device %d: name and api_key are required", i+1)
		}
		host, err := normalizeHost(d.Host)
		if err != nil {
			return o, fmt.Errorf("device %d: %w", i+1, err)
		}
		if hosts[host] {
			return o, fmt.Errorf("device %d: this host is already configured", i+1)
		}
		d.Host, hosts[host] = host, true
	}
	return o, nil
}

func normalizeHost(host string) (string, error) {
	invalid := errors.New("host must be an HTTPS hostname or IP, optionally with port")
	host = strings.TrimSpace(host)
	if host == "" || strings.IndexFunc(host, unicode.IsSpace) >= 0 {
		return "", invalid
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", invalid
	}
	hostname := strings.ToLower(u.Hostname())
	if strings.Contains(hostname, ":") && net.ParseIP(hostname) == nil {
		return "", invalid
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", invalid
		}
		if n == 443 {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	}
	if port != "" {
		hostname = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	return "https://" + hostname, nil
}

func (o Options) interval() time.Duration { return time.Duration(o.PollInterval) * time.Second }
