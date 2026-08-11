package voice

import (
	"strings"
	"testing"
	"time"
)

func TestTransitionsRequireAPathThroughHandling(t *testing.T) {
	// A freshly received complaint must not jump straight to closed: closing is
	// only reachable after a resolution has been recorded.
	if canTransition("RECEIVED", "CLOSED") {
		t.Fatal("intake must not close directly")
	}
	if canTransition("RECEIVED", "RESOLVED") {
		t.Fatal("intake must be reviewed or worked before it can be resolved")
	}
	for _, step := range [][2]string{
		{"RECEIVED", "IN_REVIEW"}, {"IN_REVIEW", "IN_PROGRESS"},
		{"IN_PROGRESS", "PENDING_CUSTOMER"}, {"PENDING_CUSTOMER", "RESOLVED"},
		{"RESOLVED", "CLOSED"},
	} {
		if !canTransition(step[0], step[1]) {
			t.Fatalf("%s -> %s must be allowed", step[0], step[1])
		}
	}
	// Reopening is always explicit, never a silent edit.
	for _, from := range []string{"RESOLVED", "CLOSED", "REJECTED"} {
		if !canTransition(from, "IN_PROGRESS") {
			t.Fatalf("%s must be reopenable", from)
		}
	}
	if canTransition("CLOSED", "RESOLVED") {
		t.Fatal("a closed record must be reopened before it can be resolved again")
	}
	// Saving without changing state must always be permitted.
	for status := range statuses {
		if !canTransition(status, status) {
			t.Fatalf("%s must allow an in-place edit", status)
		}
	}
}

func TestTerminalStatesMatchTheTransitionTable(t *testing.T) {
	for status := range terminal {
		if !statuses[status] {
			t.Fatalf("terminal status %q is not a valid status", status)
		}
	}
	for _, open := range []string{"RECEIVED", "IN_REVIEW", "IN_PROGRESS", "PENDING_CUSTOMER"} {
		if terminal[open] {
			t.Fatalf("%s must count as open work", open)
		}
	}
	if len(terminal) != 3 {
		t.Fatalf("expected three terminal states, found %d", len(terminal))
	}
}

func TestDecorateFlagsOnlyOpenBreaches(t *testing.T) {
	past := time.Now().Add(-3 * time.Hour)
	future := time.Now().Add(3 * time.Hour)

	overdue := Voice{Status: "IN_PROGRESS", OccurredAt: time.Now().Add(-48 * time.Hour), ResponseDueAt: &past, ResolutionDueAt: &past}
	decorate(&overdue)
	if !overdue.ResponseOverdue || !overdue.ResolutionOverdue {
		t.Fatalf("an open record past both deadlines must be flagged: %+v", overdue)
	}
	if overdue.OpenDays != 2 {
		t.Fatalf("expected 2 open days, got %d", overdue.OpenDays)
	}

	// A recorded first response clears the response breach.
	responded := overdue
	now := time.Now()
	responded.FirstRespondedAt = &now
	responded.ResponseOverdue, responded.ResolutionOverdue = false, false
	decorate(&responded)
	if responded.ResponseOverdue {
		t.Fatal("a record that was answered must not report a response breach")
	}

	// Closed work never reports a breach, even with deadlines in the past.
	closed := Voice{Status: "CLOSED", OccurredAt: past, ResponseDueAt: &past, ResolutionDueAt: &past, ResolvedAt: &now}
	decorate(&closed)
	if closed.ResponseOverdue || closed.ResolutionOverdue {
		t.Fatalf("a closed record must not report breaches: %+v", closed)
	}

	// Deadlines in the future are not breaches.
	healthy := Voice{Status: "IN_PROGRESS", OccurredAt: time.Now(), ResponseDueAt: &future, ResolutionDueAt: &future}
	decorate(&healthy)
	if healthy.ResponseOverdue || healthy.ResolutionOverdue {
		t.Fatal("a record inside its SLA must not be flagged")
	}
}

func TestSlaDeadlinesTightenWithSeverity(t *testing.T) {
	// No database is needed: with an empty category the defaults apply and only
	// the severity clamp is exercised.
	s := &Service{}
	from := time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)
	cases := map[string]struct{ response, resolution float64 }{
		"NORMAL":   {8, 72},
		"HIGH":     {4, 48},
		"CRITICAL": {2, 24},
	}
	for severity, want := range cases {
		response, resolution := s.slaDeadlines(nil, "", severity, from)
		if got := response.Sub(from).Hours(); got != want.response {
			t.Fatalf("%s response target was %.0fh, expected %.0fh", severity, got, want.response)
		}
		if got := resolution.Sub(from).Hours(); got != want.resolution {
			t.Fatalf("%s resolution target was %.0fh, expected %.0fh", severity, got, want.resolution)
		}
	}
}

func TestValidateRequiresACustomerAndKnownCodes(t *testing.T) {
	ok := Input{CustomerID: "c1", Title: "납기 지연", VoiceType: "COMPLAINT", Channel: "PHONE", Severity: "HIGH"}
	if err := validate(ok); err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string]Input{
		"no title":     {CustomerID: "c1", VoiceType: "COMPLAINT"},
		"no customer":  {Title: "x", VoiceType: "COMPLAINT"},
		"bad type":     {CustomerID: "c1", Title: "x", VoiceType: "RANT"},
		"bad channel":  {CustomerID: "c1", Title: "x", VoiceType: "COMPLAINT", Channel: "PIGEON"},
		"bad severity": {CustomerID: "c1", Title: "x", VoiceType: "COMPLAINT", Severity: "APOCALYPTIC"},
		"blank title":  {CustomerID: "c1", Title: "   ", VoiceType: "COMPLAINT"},
	} {
		if err := validate(in); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}

func TestValidateCategoryKeepsSLAOrdering(t *testing.T) {
	if err := ValidateCategory("DELIVERY_DELAY", "납기 지연", "COMPLAINT", 4, 48); err != nil {
		t.Fatal(err)
	}
	// Responding later than the resolution target is not a usable SLA.
	if err := ValidateCategory("X", "x", "COMPLAINT", 72, 8); err == nil {
		t.Fatal("responseHours above resolutionHours must be rejected")
	}
	if err := ValidateCategory("", "x", "COMPLAINT", 4, 8); err == nil {
		t.Fatal("a blank code must be rejected")
	}
	if err := ValidateCategory("X", "x", "GOSSIP", 4, 8); err == nil {
		t.Fatal("an unknown voiceType must be rejected")
	}
	if err := ValidateCategory("X", "x", "COMPLAINT", 0, 8); err == nil {
		t.Fatal("a zero response target must be rejected")
	}
}

func TestCoalesceAndNullableTreatBlankAsAbsent(t *testing.T) {
	if coalesce("", "  ", "resolved") != "resolved" {
		t.Fatal("coalesce must skip blank values")
	}
	if coalesce("", "  ") != "" {
		t.Fatal("all-blank input must produce an empty string")
	}
	if nullable("   ") != nil {
		t.Fatal("a blank optional reference must be stored as NULL")
	}
	if nullable(" abc ") != "abc" {
		t.Fatal("a provided reference must be trimmed")
	}
}

func TestVoiceTypeCoverageMatchesTheSchema(t *testing.T) {
	// These are the values the CHECK constraint in migration 008 accepts. A code
	// path that drifts from the schema would fail at insert time instead of here.
	schema := "COMPLAINT REQUEST INQUIRY DEFECT PRAISE CHURN_RISK"
	for kind := range voiceTypes {
		if !strings.Contains(schema, kind) {
			t.Fatalf("%q is not accepted by the database constraint", kind)
		}
	}
	if len(voiceTypes) != len(strings.Fields(schema)) {
		t.Fatalf("service knows %d voice types, schema allows %d", len(voiceTypes), len(strings.Fields(schema)))
	}
}
