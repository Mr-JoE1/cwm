package desktopapp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	uuidWork    = "11111111-aaaa-4bbb-8ccc-000000000001"
	uuidDefault = "22222222-aaaa-4bbb-8ccc-000000000002"
	uuidStray   = "33333333-aaaa-4bbb-8ccc-000000000003"
	// secret stands in for the encrypted oauth:tokenCache* blobs: no
	// result, message or struct may ever contain it.
	secret = "djEwSECRETBLOBdoNotRead"
)

var names = map[string]string{uuidWork: "work", uuidDefault: "default"}

// profile writes a fixture profile at root: config.json with uuid (and
// the oauth blobs), a cookie store and Local State, all at mtime.
func profile(t *testing.T, root, uuid string, mtime time.Time) {
	t.Helper()
	cfg := map[string]any{
		"oauth:tokenCache":     secret,
		"oauth:tokenCacheV2":   secret + "V2",
		"locale":               "en-US",
		"lastKnownAccountUuid": uuid,
	}
	if uuid == "" {
		delete(cfg, "lastKnownAccountUuid")
	}
	data, _ := json.Marshal(cfg)
	write(t, filepath.Join(root, "config.json"), string(data), mtime)
	write(t, filepath.Join(root, "Network", "Cookies"), secret, mtime)
	write(t, filepath.Join(root, "Local State"), `{"os_crypt":{"encrypted_key":"`+secret+`"}}`, mtime)
}

func write(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func winEnv(t *testing.T) (Env, string, string) {
	t.Helper()
	base := t.TempDir()
	local, roaming := filepath.Join(base, "Local"), filepath.Join(base, "Roaming")
	env := Env{GOOS: "windows", Home: base, Getenv: func(k string) string {
		switch k {
		case "LOCALAPPDATA":
			return local
		case "APPDATA":
			return roaming
		}
		return ""
	}}
	return env, local, roaming
}

var (
	old = time.Now().Add(-48 * time.Hour)
	mid = time.Now().Add(-24 * time.Hour)
	now = time.Now().Add(-time.Hour)
)

// TestAppConfigOneField pins the decoder struct to the one plain field:
// adding a field here would decode more of config.json than D121 allows.
func TestAppConfigOneField(t *testing.T) {
	typ := reflect.TypeOf(appConfig{})
	if typ.NumField() != 1 {
		t.Fatalf("appConfig has %d fields, want exactly 1 (lastKnownAccountUuid)", typ.NumField())
	}
	if tag := typ.Field(0).Tag.Get("json"); tag != "lastKnownAccountUuid" {
		t.Fatalf("appConfig field tag = %q", tag)
	}
	if typ.Field(0).Type.Kind() != reflect.String {
		t.Fatal("appConfig field must be a string")
	}
}

// TestDetectNeverHoldsOAuth: the oauth blobs, cookies and Local State are
// in the fixture, and nothing Detect returns (exported or not) holds them.
func TestDetectNeverHoldsOAuth(t *testing.T) {
	env, _, roaming := winEnv(t)
	root := filepath.Join(roaming, "Claude")
	profile(t, root, uuidWork, now)
	st := Detect(env, names)
	if st.State != StateAccount || st.Account != "work" || st.UUID() != uuidWork {
		t.Fatalf("Detect = %+v", st)
	}
	js, _ := json.Marshal(st)
	all := fmt.Sprintf("%#v %s %s", st, js, st.Line())
	if strings.Contains(all, "SECRET") {
		t.Fatalf("an oauth/cookie value leaked: %s", all)
	}
	if strings.Contains(string(js), uuidWork) || strings.Contains(st.Line(), uuidWork) {
		t.Fatalf("uuid exposed: %s / %s", js, st.Line())
	}
}

func TestAbandonedRootSkipped(t *testing.T) {
	env, local, roaming := winEnv(t)
	classic := filepath.Join(roaming, "Claude")
	store := filepath.Join(local, "Claude-Data", "claude_pubhash")
	// The abandoned classic root is the newest, but carries the marker.
	profile(t, classic, uuidDefault, now)
	write(t, filepath.Join(classic, ".user-data-left.20260901"), "", now)
	profile(t, store, uuidWork, old)
	root, _ := LiveRoot(env)
	if root != store {
		t.Fatalf("LiveRoot = %s, want the Claude-Data root %s", root, store)
	}
	if st := Detect(env, names); st.Account != "work" {
		t.Fatalf("Detect = %+v", st)
	}
	// Only abandoned roots left: still better than nothing.
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
	if root, _ := LiveRoot(env); root != classic {
		t.Fatalf("LiveRoot with only an abandoned root = %q", root)
	}
}

func TestStoreAndClassicNewestWins(t *testing.T) {
	env, local, roaming := winEnv(t)
	classic := filepath.Join(roaming, "Claude")
	pkg := filepath.Join(local, "Packages", "Claude_pubhash", "LocalCache", "Roaming", "Claude")
	profile(t, classic, uuidDefault, old)
	profile(t, pkg, uuidWork, mid)
	if got := Candidates(env); len(got) != 2 || got[0] != classic || got[1] != pkg {
		t.Fatalf("Candidates = %v", got)
	}
	if root, running := LiveRoot(env); root != pkg || running {
		t.Fatalf("LiveRoot = %s %v, want the newer Store root", root, running)
	}
	// Touch the classic config.json: now it is the newest.
	if err := os.Chtimes(filepath.Join(classic, "config.json"), now, now); err != nil {
		t.Fatal(err)
	}
	if root, _ := LiveRoot(env); root != classic {
		t.Fatalf("LiveRoot = %s, want the touched classic root", root)
	}
}

func TestLockedCookiesWin(t *testing.T) {
	env, local, roaming := winEnv(t)
	classic := filepath.Join(roaming, "Claude")
	store := filepath.Join(local, "Claude-Data", "claude_pubhash")
	profile(t, classic, uuidDefault, now)
	profile(t, store, uuidWork, old)
	env.Locked = func(p string) bool { return strings.HasPrefix(p, store) }
	root, running := LiveRoot(env)
	if root != store || !running {
		t.Fatalf("LiveRoot = %s %v, want the locked root", root, running)
	}
	if st := Detect(env, names); !st.Running || !strings.Contains(st.Line(), "running") {
		t.Fatalf("Detect = %+v", st)
	}
}

func TestStates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(root string)
		want  State
		who   string
		line  string
	}{
		{"none", func(string) {}, StateNone, "nobody", "not installed"},
		{"signed out", func(r string) { profile(t, r, "", now) }, StateSignedOut, "nobody", "signed out"},
		{"never signed in", func(r string) { write(t, filepath.Join(r, "Local State"), "{}", now) }, StateSignedOut, "nobody", "signed out"},
		{"account", func(r string) { profile(t, r, uuidDefault, now) }, StateAccount, "default", "signed in as account default"},
		{"other", func(r string) { profile(t, r, uuidStray, now) }, StateOther, "another account", "an account cwm doesn't know"},
		{"unreadable", func(r string) { write(t, filepath.Join(r, "config.json"), "{not json", now) }, StateUnknown, "another account", "unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, _, roaming := winEnv(t)
			tc.setup(filepath.Join(roaming, "Claude"))
			st := Detect(env, names)
			if st.State != tc.want || st.Who() != tc.who || !strings.Contains(st.Line(), tc.line) {
				t.Fatalf("Detect = %+v who=%q line=%q", st, st.Who(), st.Line())
			}
		})
	}
}

func TestListsNamedNeverUUID(t *testing.T) {
	env, _, roaming := winEnv(t)
	root := filepath.Join(roaming, "Claude")
	profile(t, root, uuidWork, now)
	base := filepath.Join(root, SessionsDir)
	write(t, filepath.Join(base, uuidWork, "org1", "local_a.json"), "{}", now)
	write(t, filepath.Join(base, uuidWork, "org1", "local_b.json"), "{}", now)
	write(t, filepath.Join(base, uuidWork, "org1", "deleted_c"), "", now)
	write(t, filepath.Join(base, uuidDefault, "org1", "local_c.json"), "{}", now)
	write(t, filepath.Join(base, uuidStray, "org9", "local_d.json"), "{}", now)
	st := Detect(env, names)
	got := st.CountsText()
	for _, w := range []string{"work 2", "default 1", "another account 1"} {
		if !strings.Contains(got, w) {
			t.Fatalf("CountsText = %q, want %q", got, w)
		}
	}
	if strings.Contains(st.Line(), "-aaaa-") {
		t.Fatalf("Line shows a uuid: %s", st.Line())
	}
}

func TestDarwinRoot(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Library", "Application Support", "Claude")
	profile(t, root, uuidWork, now)
	st := Detect(Env{GOOS: "darwin", Home: home}, names)
	if st.Root != root || st.Account != "work" {
		t.Fatalf("Detect = %+v", st)
	}
}
