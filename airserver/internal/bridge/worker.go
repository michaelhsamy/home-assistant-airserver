package bridge

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type command struct{ control, payload string }

// A worker owns its device state. Only admission flags are shared with MQTT callbacks.
type worker struct {
	session                      *session
	config                       DeviceConfig
	api                          *API
	id                           string
	commands                     chan command
	wake                         chan struct{}
	admission                    sync.Mutex
	online, busy, forceDiscovery atomic.Bool
	rtspKnown                    atomic.Bool
	announced                    bool
	model, version, lastError    string
}

func newWorker(s *session, config DeviceConfig, id string) *worker {
	return &worker{session: s, config: config, api: NewAPI(config), id: id, commands: make(chan command, 1), wake: make(chan struct{}, 1)}
}

func (w *worker) submit(cmd command) {
	w.admission.Lock()
	defer w.admission.Unlock()
	if !validCommand(cmd.control, cmd.payload) {
		w.session.bridge.log.Warn("Ignored invalid command", "device", w.config.Name)
		return
	}
	if w.config.StateSource == "services" && cmd.control == "rtsp" && !w.rtspKnown.Load() {
		w.session.bridge.log.Warn("RTSP command rejected: enable Livestream before changing RTSP in service-check mode", "device", w.config.Name)
		return
	}
	if w.session.ctx.Err() != nil || !w.online.Load() || !w.busy.CompareAndSwap(false, true) {
		w.session.bridge.log.Warn("Command rejected: device unavailable or another command is running", "device", w.config.Name)
		return
	}
	select {
	case w.commands <- cmd:
	default:
		w.busy.Store(false)
	}
}

func (w *worker) run() {
	defer w.online.Store(false)
	w.poll()
	timer := time.NewTicker(w.session.bridge.pollInterval)
	defer timer.Stop()
	for {
		select {
		case <-w.session.ctx.Done():
			return
		case <-timer.C:
			w.poll()
		case <-w.wake:
			w.poll()
		case cmd := <-w.commands:
			if w.session.ctx.Err() == nil && w.online.Load() {
				err := w.execute(cmd)
				if err != nil {
					w.unavailable(fmt.Errorf("%s failed or could not be confirmed: %w; command not retried", cmd.control, err))
				}
			} else if w.session.ctx.Err() == nil {
				w.session.bridge.log.Warn("Command rejected: device unavailable", "device", w.config.Name)
			}
			w.busy.Store(false)
		}
	}
}

func (w *worker) execute(cmd command) error {
	if w.config.StateSource == "services" && cmd.control == "rtsp" && !w.rtspKnown.Load() {
		return fmt.Errorf("RTSP state is unavailable; enable Livestream before changing RTSP in service-check mode")
	}
	if err := w.api.Command(w.session.ctx, cmd.control, cmd.payload); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(w.session.ctx, requestTimeout)
	defer cancel()
	for {
		state, err := w.api.Read(ctx)
		if err != nil {
			return err
		}
		if cmd.control == "end_session" {
			return w.publishState(state)
		}
		reported := state.Livestream
		if cmd.control == "rtsp" {
			reported = state.RTSP
		}
		if reported != nil && *reported == (cmd.payload == "ON") {
			return w.publishState(state)
		}
		actual := "unavailable"
		if reported != nil {
			actual = "OFF"
			if *reported {
				actual = "ON"
			}
		}
		if w.config.StateSource != "services" {
			return fmt.Errorf("AirServer accepted %s=%s but its API still reports %s; check the device UI and firmware", cmd.control, cmd.payload, actual)
		}
		// A listener may take a moment to start/stop. Repeat only the reads,
		// and do not publish the previous service state while it is settling.
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("AirServer accepted %s=%s but the service check still reports %s", cmd.control, cmd.payload, actual)
		case <-timer.C:
		}
	}
}

func (w *worker) poll() {
	if err := w.refresh(); err != nil {
		w.unavailable(err)
	}
}

func (w *worker) unavailable(err error) {
	w.admission.Lock()
	w.online.Store(false)
	// A command accepted during a status read must not survive that read failing.
	select {
	case <-w.commands:
		w.busy.Store(false)
	default:
	}
	w.admission.Unlock()
	if w.session.ctx.Err() != nil {
		return
	}
	if err.Error() != w.lastError {
		w.session.bridge.log.Warn("Device unavailable", "device", w.config.Name, "error", err)
		w.lastError = err.Error()
	}
	if w.id != "" {
		_ = w.session.publish(topic(w.id, "availability"), "offline")
	}
}

func (w *worker) refresh() error {
	state, err := w.api.Read(w.session.ctx)
	if err != nil {
		return err
	}
	return w.publishState(state)
}

func (w *worker) publishState(state State) error {
	id := deviceID(state.Serial)
	if err := w.session.claim(w, id); err != nil {
		// Do not publish unavailability on another worker's identity.
		w.id = ""
		return err
	}
	if w.id != "" && w.id != id {
		if err := w.session.removeDevice(w.id); err != nil {
			return err
		}
		w.announced = false
	}
	w.id = id
	if err := w.session.bridge.registry.set(w.config.cacheKey(), id); err != nil {
		w.session.cancel(err)
		return err
	}
	if w.forceDiscovery.Swap(false) || !w.announced || w.model != state.Model || w.version != state.Version {
		if err := w.session.announce(id, w.config.Name, state); err != nil {
			return err
		}
		w.announced, w.model, w.version = true, state.Model, state.Version
	}
	for control, value := range map[string]*bool{"livestream": state.Livestream, "rtsp": state.RTSP} {
		if value == nil {
			if err := w.session.publish(topic(id, control+"/availability"), "offline"); err != nil {
				return err
			}
			continue
		}
		payload := "OFF"
		if *value {
			payload = "ON"
		}
		if err := w.session.publish(topic(id, control+"/state"), payload); err != nil {
			return err
		}
		if err := w.session.publish(topic(id, control+"/availability"), "online"); err != nil {
			return err
		}
	}
	w.rtspKnown.Store(state.RTSP != nil)
	if err := w.session.publish(topic(id, "availability"), "online"); err != nil {
		return err
	}
	if !w.online.Swap(true) {
		w.session.bridge.log.Info("Device connected", "device", w.config.Name)
	}
	w.lastError = ""
	return nil
}
