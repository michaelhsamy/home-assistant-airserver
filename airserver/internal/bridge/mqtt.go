package bridge

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type brokerConfig struct {
	host               string
	port               int
	username, password string
	tls                bool
}

func mqttService(ctx context.Context, client *http.Client, endpoint, token string) (brokerConfig, error) {
	var config brokerConfig
	failure := errors.New("MQTT service unavailable; configure the Mosquitto app")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return config, failure
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return config, failure
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return config, failure
	}
	var result struct {
		Result string `json:"result"`
		Data   struct {
			Host     string          `json:"host"`
			Port     json.RawMessage `json:"port"`
			Username string          `json:"username"`
			Password string          `json:"password"`
			SSL      bool            `json:"ssl"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result) != nil || result.Result != "ok" || result.Data.Host == "" {
		return config, failure
	}
	var port string
	if json.Unmarshal(result.Data.Port, &port) != nil {
		port = string(result.Data.Port)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return config, failure
	}
	return brokerConfig{result.Data.Host, n, result.Data.Username, result.Data.Password, result.Data.SSL}, nil
}

func clientOptions(config brokerConfig, onMessage mqtt.MessageHandler, onLost mqtt.ConnectionLostHandler) *mqtt.ClientOptions {
	scheme := "tcp"
	if config.tls {
		scheme = "ssl"
	}
	options := mqtt.NewClientOptions().AddBroker(scheme+"://"+net.JoinHostPort(config.host, strconv.Itoa(config.port))).
		SetClientID("airserver-connect-bridge").SetUsername(config.username).SetPassword(config.password).
		SetProtocolVersion(4).SetCleanSession(true).SetAutoReconnect(false).SetConnectRetry(false).
		SetResumeSubs(false).SetKeepAlive(15*time.Second).SetPingTimeout(5*time.Second).
		SetConnectTimeout(5*time.Second).SetWriteTimeout(5*time.Second).
		SetDefaultPublishHandler(onMessage).SetConnectionLostHandler(onLost).
		SetWill(bridgeAvailability, "offline", 1, true)
	if config.tls {
		options.SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
	}
	return options
}

func waitToken(ctx context.Context, token mqtt.Token) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("MQTT operation timed out")
	case <-token.Done():
		if token.Error() != nil {
			return errors.New("MQTT operation failed")
		}
		return nil
	}
}
