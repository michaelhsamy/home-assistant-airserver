package bridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

// Registry persists only host hashes and device identities, compatible with v0.1.
type Registry struct {
	mu      sync.Mutex
	path    string
	entries map[string]string
}

func LoadRegistry(path string) (*Registry, error) {
	r := &Registry{path: path, entries: make(map[string]string)}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, errors.New("could not read device registry")
	}
	if json.Unmarshal(data, &r.entries) != nil || r.entries == nil {
		return nil, errors.New("invalid device registry")
	}
	keyPattern, idPattern := regexp.MustCompile(`^[0-9a-f]{64}$`), regexp.MustCompile(`^airserver_[0-9a-f]{24}$`)
	for key, id := range r.entries {
		if !keyPattern.MatchString(key) || !idPattern.MatchString(id) {
			return nil, errors.New("invalid device registry")
		}
	}
	return r, nil
}
func (r *Registry) snapshot() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string]string, len(r.entries))
	for k, v := range r.entries {
		result[k] = v
	}
	return result
}
func (r *Registry) set(key, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries[key] == id {
		return nil
	}
	next := make(map[string]string, len(r.entries))
	for k, v := range r.entries {
		next[k] = v
	}
	if id == "" {
		delete(next, key)
	} else {
		next[key] = id
	}
	data, _ := json.Marshal(next)
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return errors.New("could not create device registry directory")
	}
	if err := os.WriteFile(r.path+".tmp", data, 0600); err != nil {
		return errors.New("could not write device registry")
	}
	if err := os.Rename(r.path+".tmp", r.path); err != nil {
		return errors.New("could not replace device registry")
	}
	r.entries = next
	return nil
}
