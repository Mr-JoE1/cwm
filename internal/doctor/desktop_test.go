package doctor

import (
	"strings"
	"testing"

	"github.com/Mr-JoE1/cwm/internal/config"
	"github.com/Mr-JoE1/cwm/internal/desktopapp"
)

// TestCheckDesktop (D125): one line per state, accounts by name, never a
// uuid; Linux shows it only when the app has a profile there.
func TestCheckDesktop(t *testing.T) {
	old, oldOS := desktopDetect, hostOS
	t.Cleanup(func() { desktopDetect, hostOS = old, oldOS })
	for _, tc := range []struct {
		st   desktopapp.Status
		goos string
		want string
	}{
		{desktopapp.Status{State: desktopapp.StateAccount, Account: "work", Root: "/p",
			Lists: []desktopapp.Count{{Name: "work", Entries: 12, Signed: true}, {Name: "default", Entries: 3}, {Entries: 1}}},
			"windows", "desktop app: signed in as account work · Code-tab entries: work 12, default 3, another account 1"},
		{desktopapp.Status{State: desktopapp.StateOther, Root: "/p"}, "darwin", "desktop app: signed in to an account cwm doesn't know"},
		{desktopapp.Status{State: desktopapp.StateSignedOut, Root: "/p"}, "linux", "desktop app: signed out"},
		{desktopapp.Status{State: desktopapp.StateNone}, "windows", "desktop app: not installed"},
		{desktopapp.Status{State: desktopapp.StateNone}, "linux", ""},
	} {
		desktopDetect = func(*config.Config) desktopapp.Status { return tc.st }
		hostOS = tc.goos
		var r checkList
		checkDesktop(&r, nil)
		got := ""
		if len(r) == 1 {
			got = r[0].Detail
			if r[0].Status != StatusOK || r[0].Name != "desktop-app" {
				t.Errorf("%+v", r[0])
			}
		}
		if tc.want == "" && got != "" || tc.want != "" && !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s/%s: %q, want %q", tc.st.State, tc.goos, got, tc.want)
		}
	}
}
