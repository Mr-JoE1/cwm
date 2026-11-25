package syncengine

import (
	"encoding/json"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/syncthing"
)

func TestApplyPrivacyKeepsOtherOptions(t *testing.T) {
	f := newFakeSyncthing(t)
	var cfg syncthing.Config
	if err := json.Unmarshal([]byte(`{"devices":[],"folders":[],"options":{"listenAddresses":["default"],"globalAnnounceEnabled":true,"relaysEnabled":true},"gui":{"enabled":true}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	f.setConfig(cfg)
	cl := f.client()
	changed, err := ApplyPrivacy(cl, true)
	if err != nil || !changed {
		t.Fatalf("ApplyPrivacy(true) = %v, %v", changed, err)
	}
	got := f.config()
	for k, want := range map[string]string{"globalAnnounceEnabled": "false", "relaysEnabled": "false", "localAnnounceEnabled": "true", "listenAddresses": `["default"]`} {
		if v, _ := got.Option(k); string(v) != want {
			t.Errorf("option %s = %s, want %s", k, v, want)
		}
	}
	b, _ := json.Marshal(got)
	var all map[string]json.RawMessage
	_ = json.Unmarshal(b, &all)
	if _, ok := all["gui"]; !ok {
		t.Error("unknown top-level config lost")
	}
	if p, err := PrivacyState(cl); err != nil || !p {
		t.Fatalf("PrivacyState = %v, %v", p, err)
	}
	puts := f.putCount()
	if changed, err := ApplyPrivacy(cl, true); err != nil || changed || f.putCount() != puts {
		t.Fatalf("second ApplyPrivacy changed=%v err=%v", changed, err)
	}
	if changed, err := ApplyPrivacy(cl, false); err != nil || !changed {
		t.Fatalf("ApplyPrivacy(false) = %v, %v", changed, err)
	}
	if p, _ := PrivacyState(cl); p {
		t.Fatal("still private after turning it off")
	}
}

func TestPairAtStaticAddress(t *testing.T) {
	f := newFakeSyncthing(t)
	cl := f.client()
	ws := t.TempDir()
	addrs := []string{"tcp://192.0.2.10:22000"}
	if _, err := PairAt(cl, fakePeerID, "laptop", ws, addrs); err != nil {
		t.Fatal(err)
	}
	d := findDevice(ptr(f.config()), fakePeerID)
	if d == nil || len(d.Addresses) != 1 || d.Addresses[0] != addrs[0] {
		t.Fatalf("peer = %+v", d)
	}
	// Re-pair with the same address is a no-op; a new one replaces it.
	if changed, err := PairAt(cl, fakePeerID, "laptop", ws, addrs); err != nil || changed {
		t.Fatalf("repeat PairAt changed=%v err=%v", changed, err)
	}
	if changed, err := PairAt(cl, fakePeerID, "", ws, []string{"quic://host.example:22000"}); err != nil || !changed {
		t.Fatalf("new address changed=%v err=%v", changed, err)
	}
	// Plain Pair leaves addresses alone.
	if changed, err := Pair(cl, fakePeerID, "", ws); err != nil || changed {
		t.Fatalf("Pair changed=%v err=%v", changed, err)
	}
	for _, bad := range []string{"host:22000", "http://x:1", "tcp://", "tcp://a b"} {
		if _, err := PairAt(cl, fakePeerID, "", ws, []string{bad}); err == nil {
			t.Errorf("address %q accepted", bad)
		}
	}
}

func ptr(c syncthing.Config) *syncthing.Config { return &c }
