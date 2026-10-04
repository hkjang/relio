package mail

// One mail event switch is written down in three places by hand, and only one
// of them actually stops a mail:
//
//   - eventSettings in config.go is the gate. Config.Allows consults it, and an
//     event it does not know is sent (adding a notification must not require a
//     settings change first).
//   - migrations/ seeds the stored value, so a fresh install has a row the
//     administrator can see and flip.
//   - mailEvents in web/src/pages/AdminPages.tsx is the only way to flip it.
//
// Every direction between them is a silent failure. A gate with no toggle is a
// mail nobody can switch off. A toggle with no gate is a checkbox the
// administrator unticks while the mail keeps arriving. A gate with no seeded row
// falls back to the UI default rather than to a stored value, so the screen and
// the relay can disagree about whether the event is on.
//
// TestEverySwitchedEventFollowsTheKeyConvention next door reads only
// EventSettingKeys, so it stayed green with a seeded row deleted; these checks
// read migrations/ and the admin screen themselves.

import (
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hkjang/relio/migrations"
)

// adminScreen holds mailEvents, the checkbox list the administrator sees.
const adminScreen = "../../web/src/pages/AdminPages.tsx"

// Count guards, so a scan that stops matching fails loudly instead of
// comparing two empty sets. Raise them when an event is added.
const (
	knownSeededNotifySettings = 4
	knownAdminMailToggles     = 4
)

// mailSettingRow matches one seeded system_settings row of the mail namespace:
// ('mail','notify_voice_assigned','true','boolean').
var mailSettingRow = regexp.MustCompile(`\(\s*'mail'\s*,\s*'([A-Za-z0-9_]+)'\s*,\s*'([^']*)'`)

// mailToggleEntry matches one ['notify_...', label, who] entry of mailEvents.
var mailToggleEntry = regexp.MustCompile(`\[\s*'([A-Za-z0-9_]+)'\s*,`)

// seededMailSettings reads the mail namespace out of the migrations as they
// ship, keyed without the namespace prefix. Files are read in name order, which
// is the order PostgreSQL applies them, so a later file re-seeding a key wins.
func seededMailSettings(t *testing.T) map[string]string {
	t.Helper()
	names, err := fs.Glob(migrations.Files, "*.sql")
	if err != nil {
		t.Fatalf("migrations could not be listed: %v", err)
	}
	sort.Strings(names)
	values := map[string]string{}
	for _, name := range names {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("%s could not be read: %v", name, err)
		}
		for _, match := range mailSettingRow.FindAllStringSubmatch(string(body), -1) {
			values[match[1]] = match[2]
		}
	}
	return values
}

// adminMailToggles reads the keys mailEvents renders a checkbox for.
func adminMailToggles(t *testing.T) map[string]bool {
	t.Helper()
	body, err := os.ReadFile(adminScreen)
	if err != nil {
		t.Fatalf("%s could not be read: %v", adminScreen, err)
	}
	const marker = "const mailEvents"
	start := strings.Index(string(body), marker)
	if start < 0 {
		t.Fatalf("%s no longer declares %s; the admin mail switches moved and this scan reads nothing", adminScreen, marker)
	}
	line := string(body)[start:]
	if end := strings.IndexByte(line, '\n'); end >= 0 {
		line = line[:end]
	}
	toggles := map[string]bool{}
	for _, match := range mailToggleEntry.FindAllStringSubmatch(line, -1) {
		toggles[match[1]] = true
	}
	return toggles
}

// TestEverySwitchedEventIsSeededAndSwitchable walks the gate and demands that
// each of its keys exists in both of the other two places.
func TestEverySwitchedEventIsSeededAndSwitchable(t *testing.T) {
	seeded := seededMailSettings(t)
	toggles := adminMailToggles(t)
	for event, key := range EventSettingKeys() {
		bare := strings.TrimPrefix(key, "mail.")
		value, ok := seeded[bare]
		if !ok {
			t.Errorf("%s gates on %s but no migration seeds it; a fresh install has no row, so the switch the screen shows is its own default rather than a stored value", event, key)
		} else if value != "true" {
			// Config.Allows sends an event it has no entry for, and the admin
			// screen falls back to checked, so anything but true here makes a
			// fresh install disagree with both.
			t.Errorf("%s is seeded as %q; the default has to be true, which is what Config.Allows and the admin screen fall back to", key, value)
		}
		if !toggles[bare] {
			t.Errorf("%s gates %s but %s renders no checkbox for it, so the mail cannot be switched off from 관리자 콘솔 > 메일 알림", key, event, adminScreen)
		}
	}
	// Last, so a real mismatch above is named before this fires. Both scans
	// read text, and text they stop matching would compare two empty sets.
	notifySeeded := 0
	for key := range seeded {
		if strings.HasPrefix(key, "notify_") {
			notifySeeded++
		}
	}
	if notifySeeded < knownSeededNotifySettings {
		t.Errorf("read only %d seeded ('mail','notify_...') rows out of migrations/, want at least %d; either a switch was dropped or the migration scan no longer matches", notifySeeded, knownSeededNotifySettings)
	}
	if len(toggles) < knownAdminMailToggles {
		t.Errorf("read only %d mailEvents entries out of %s, want at least %d; either a switch was dropped or the admin screen scan no longer matches", len(toggles), adminScreen, knownAdminMailToggles)
	}
}

// TestEverySeededSwitchAndToggleGatesAnEvent is the other direction: a switch
// that gates nothing is worse than a missing one, because flipping it reports
// success and changes no mail.
func TestEverySeededSwitchAndToggleGatesAnEvent(t *testing.T) {
	gated := map[string]string{}
	for event, key := range EventSettingKeys() {
		gated[strings.TrimPrefix(key, "mail.")] = event
	}
	for key := range seededMailSettings(t) {
		if strings.HasPrefix(key, "notify_") && gated[key] == "" {
			t.Errorf("migrations seed mail.%s but no event in eventSettings gates on it; the stored switch changes no mail", key)
		}
	}
	for key := range adminMailToggles(t) {
		if gated[key] == "" {
			t.Errorf("%s renders a checkbox for mail.%s but no event in eventSettings gates on it; unticking it reports success and changes no mail", adminScreen, key)
		}
	}
}
