package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Bridge struct {
	options                     Options
	registry                    *Registry
	log                         *slog.Logger
	pollInterval, retryInterval time.Duration
	service                     func(context.Context, string) (brokerConfig, error)
}

func New(options Options, registry *Registry, logger *slog.Logger) *Bridge {
	client := &http.Client{Timeout: requestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Bridge{options: options, registry: registry, log: logger, pollInterval: options.interval(), retryInterval: 5 * time.Second,
		service: func(ctx context.Context, token string) (brokerConfig, error) {
			return mqttService(ctx, client, "http://supervisor/services/mqtt", token)
		},
	}
}

func (b *Bridge) Run(ctx context.Context, token string) error {
	for ctx.Err() == nil {
		credentials, err := b.service(ctx, token)
		if err == nil {
			err = b.runSession(ctx, credentials)
		}
		if ctx.Err() != nil {
			return nil
		}
		// Errors from our boundaries contain no credentials or raw server responses.
		b.log.Warn("MQTT session ended; retrying without queuing commands", "error", err)
		timer := time.NewTimer(b.retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}

type session struct {
	bridge   *Bridge
	ctx      context.Context
	cancel   context.CancelCauseFunc
	client   mqtt.Client
	workers  []*worker
	ownersMu sync.RWMutex
	owners   map[string]*worker
}

func (b *Bridge) runSession(parent context.Context, credentials brokerConfig) error {
	ctx, cancel := context.WithCancelCause(parent)
	s := &session{bridge: b, ctx: ctx, cancel: cancel, owners: make(map[string]*worker)}
	defer cancel(nil)
	cached := b.registry.snapshot()
	for _, config := range b.options.Devices {
		s.workers = append(s.workers, newWorker(s, config, cached[config.cacheKey()]))
	}
	defer func() {
		for _, w := range s.workers {
			w.api.Close()
		}
	}()
	s.client = mqtt.NewClient(clientOptions(credentials, s.handle, func(mqtt.Client, error) { cancel(errors.New("MQTT connection lost")) }))
	// Connect has its own bounded timeout. Always disconnect, including failed setup.
	defer s.client.Disconnect(100)
	if err := waitToken(ctx, s.client.Connect()); err != nil {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = waitToken(cleanup, s.client.Publish(bridgeAvailability, 1, true, "offline"))
	}()
	if err := s.publish(bridgeAvailability, "offline"); err != nil {
		return err
	}
	configured := make(map[string]bool)
	for _, config := range b.options.Devices {
		configured[config.cacheKey()] = true
	}
	for key, id := range cached {
		if err := s.publish(topic(id, "availability"), "offline"); err != nil {
			return err
		}
		if !configured[key] {
			if err := s.removeDevice(id); err != nil {
				return err
			}
			if err := b.registry.set(key, ""); err != nil {
				return err
			}
		}
	}
	if err := waitToken(ctx, s.client.SubscribeMultiple(map[string]byte{root + "/+/+/set": 0, "homeassistant/status": 0}, nil)); err != nil {
		return err
	}
	if err := s.publish(bridgeAvailability, "online"); err != nil {
		return err
	}
	b.log.Info("MQTT connected", "devices", len(s.workers))
	var wg sync.WaitGroup
	for _, w := range s.workers {
		wg.Add(1)
		go func() { defer wg.Done(); w.run() }()
	}
	<-ctx.Done()
	wg.Wait()
	return context.Cause(ctx)
}

func (s *session) publish(name string, payload any) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	err := waitToken(s.ctx, s.client.Publish(name, 1, true, payload))
	if err != nil {
		s.cancel(err)
	}
	return err
}

func (s *session) removeDevice(id string) error {
	if err := s.publish(topic(id, "availability"), "offline"); err != nil {
		return err
	}
	for _, control := range controls {
		for _, name := range []string{discoveryTopic(id, control), topic(id, control+"/state"), topic(id, control+"/set")} {
			if err := s.publish(name, []byte{}); err != nil {
				return err
			}
		}
	}
	return nil
}

// MQTT callbacks do no network I/O and never wait for a device worker.
func (s *session) handle(_ mqtt.Client, message mqtt.Message) {
	if s.ctx.Err() != nil {
		return
	}
	if message.Topic() == "homeassistant/status" && string(message.Payload()) == "online" {
		for _, w := range s.workers {
			w.forceDiscovery.Store(true)
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
		return
	}
	if message.Retained() {
		return
	}
	parts := strings.Split(message.Topic(), "/")
	if len(parts) != 4 || parts[0] != root || parts[3] != "set" {
		return
	}
	s.ownersMu.RLock()
	w := s.owners[parts[1]]
	s.ownersMu.RUnlock()
	if w != nil {
		w.submit(command{parts[2], string(message.Payload())})
	}
}

func (s *session) claim(w *worker, id string) error {
	s.ownersMu.Lock()
	defer s.ownersMu.Unlock()
	if owner := s.owners[id]; owner != nil && owner != w {
		return errors.New("same physical AirServer configured more than once")
	}
	if w.id != "" && w.id != id && s.owners[w.id] == w {
		delete(s.owners, w.id)
	}
	s.owners[id] = w
	return nil
}

func (s *session) announce(id, name string, state State) error {
	for name, config := range discovery(id, name, state) {
		payload, _ := json.Marshal(config)
		if err := s.publish(name, payload); err != nil {
			return err
		}
	}
	return nil
}
