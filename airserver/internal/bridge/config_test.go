package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func boolPtr(b bool) *bool { return &b }
func optionsJSON(host string) []byte {
	data, _ := json.Marshal(map[string]any{"devices": []map[string]any{{"name": "Room A", "host": host, "api_key": "secret"}}})
	return data
}

func TestOptions(t *testing.T) {
	o, err := ParseOptions(optionsJSON("airserver.local"))
	if err != nil {
		t.Fatal(err)
	}
	if o.PollInterval != 5 || !o.Devices[0].verifyTLS() || o.Devices[0].Host != "https://airserver.local" {
		t.Fatalf("unexpected defaults: %v", o)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", o, o.Devices[0]), "secret") {
		t.Fatal("credentials exposed by formatting")
	}
	for _, host := range []string{"http://device", "https://user:secret@device", "https://device/api", "https://device?key=secret", "https://device#fragment", "https://device:0", "https://device:99999", "https://device:abc", "bad host", ""} {
		t.Run(host, func(t *testing.T) {
			if _, err := ParseOptions(optionsJSON(host)); err == nil {
				t.Fatal("invalid host accepted")
			} else if strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leaked")
			}
		})
	}
	for _, raw := range []string{`{}`, `{"devices":[]}`, `{"devices":[{"name":"x","host":"x","api_key":"k"}],"poll_interval":1}`, `{"devices":[{"name":"x","host":"x","api_key":"k","verify_ssl":"false"}]}`, `{"devices":[{"name":"x","host":"x","api_key":"k"},{"name":"y","host":"https://X:443/","api_key":"k"}]}`} {
		if _, err := ParseOptions([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid options %s", raw)
		}
	}
	raw := []byte(`{"devices":[{"name":"x","host":"[::1]:8443","api_key":"k","verify_ssl":false}]}`)
	o, err = ParseOptions(raw)
	if err != nil || o.Devices[0].verifyTLS() || o.Devices[0].Host != "https://[::1]:8443" {
		t.Fatalf("IPv6/opt-out: %v %v", o, err)
	}
}

func TestDiscoveryIdentityAndIndependentEntities(t *testing.T) {
	id := deviceID("serial-1")
	if id != "airserver_"+hash("serial-1")[:24] {
		t.Fatal("identity compatibility changed")
	}
	a, b := discovery(id, "Before", State{}), discovery(id, "After", State{})
	if len(a) != 3 || len(b) != 3 {
		t.Fatal("expected exactly three entities")
	}
	for name, c := range a {
		if b[name]["unique_id"] != c["unique_id"] {
			t.Fatal("rename changed identity")
		}
		if c["retain"] != false || c["qos"] != 0 || c["availability_mode"] != "all" {
			t.Fatal("unsafe command/availability configuration")
		}
	}
	if id == deviceID("serial-2") {
		t.Fatal("device identities collide")
	}
}

func TestRegistryConcurrentAndCredentialFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.json")
	r, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.set(hash(fmt.Sprint(i)), deviceID(fmt.Sprint(i))); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	again, err := LoadRegistry(path)
	if err != nil || len(again.snapshot()) != 10 {
		t.Fatalf("lost registry writes: %v", err)
	}
	if err := r.set(hash("0"), ""); err != nil {
		t.Fatal(err)
	}
	again, err = LoadRegistry(path)
	if err != nil || len(again.snapshot()) != 9 {
		t.Fatal("removal did not persist")
	}
	if err := os.WriteFile(path, []byte(`{"invalid":"../../topic"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(path); err == nil {
		t.Fatal("invalid registry accepted")
	}
}
