package intelligence

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/hkjang/relio/internal/crm"
)

func TestThresholdNumber(t *testing.T) {
	values := map[string]any{"float": 14.0, "number": json.Number("30")}
	if got := thresholdNumber(values, "float", 1); got != 14 {
		t.Fatalf("float threshold = %v, want 14", got)
	}
	if got := thresholdNumber(values, "number", 1); got != 30 {
		t.Fatalf("json.Number threshold = %v, want 30", got)
	}
	if got := thresholdNumber(values, "missing", 7); got != 7 {
		t.Fatalf("fallback threshold = %v, want 7", got)
	}
}

func TestAsDateSupportsAPIAndDatabaseFormats(t *testing.T) {
	for _, value := range []string{"2026-08-09", "2026-08-09T10:30:00Z"} {
		got, ok := asDate(value)
		if !ok || got.Year() != 2026 || got.Month() != time.August || got.Day() != 9 {
			t.Fatalf("asDate(%q) = %v, %v", value, got, ok)
		}
	}
	if _, ok := asDate("not-a-date"); ok {
		t.Fatal("invalid date must not parse")
	}
}

func TestOpportunityFieldAndPresent(t *testing.T) {
	closeDate := time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC)
	opp := crm.Opportunity{
		Name:              "Enterprise Renewal",
		ExpectedAmount:    500_000_000,
		ExpectedCloseDate: &closeDate,
		CustomFields:      map[string]any{"procurementConfirmed": true},
	}
	if got := opportunityField(opp, "expectedAmount"); got != 500_000_000.0 {
		t.Fatalf("expectedAmount = %v", got)
	}
	if !present(opportunityField(opp, "expectedCloseDate")) {
		t.Fatal("expectedCloseDate should be present")
	}
	if !present(opportunityField(opp, "procurementConfirmed")) {
		t.Fatal("custom field should be present")
	}
	if present("") || present(false) || present(nil) {
		t.Fatal("empty values must not be present")
	}
}

func TestValuesEqual(t *testing.T) {
	if !valuesEqual(map[string]any{"amount": 100.0}, map[string]any{"amount": 100.0}) {
		t.Fatal("equivalent JSON values should be equal")
	}
	if valuesEqual("COMMIT", "PIPELINE") {
		t.Fatal("different values should not be equal")
	}
}
