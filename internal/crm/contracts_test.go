package crm

import (
	"strings"
	"testing"
	"time"
)

// TestExpiringDaysStaysAFilterAboveTheCap covers the argument get_expiring_contracts
// hands to Contracts. Over-range used to become 0, and 0 is how the query says
// "do not narrow by end date", so an agent asking for contracts expiring within
// 5000 days was answered with every contract in scope — including the ones with
// no end date at all.
func TestExpiringDaysStaysAFilterAboveTheCap(t *testing.T) {
	for days, want := range map[int]int{5000: 3650, 3651: 3650, 3650: 3650, 90: 90, 0: 0, -1: 0} {
		if got := boundExpiringDays(days); got != want {
			t.Errorf("boundExpiringDays(%d) = %d, want %d", days, got, want)
		}
	}
}

func TestBuildMonthlyScheduleClampsEndOfMonth(t *testing.T) {
	start := time.Date(2024, time.January, 31, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, time.April, 30, 0, 0, 0, 0, time.UTC)
	dates, err := buildScheduleDates(&start, &end, "MONTHLY")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2024-01-31", "2024-02-29", "2024-03-31", "2024-04-30"}
	if len(dates) != len(want) {
		t.Fatalf("schedule length = %d, want %d", len(dates), len(want))
	}
	for i, date := range dates {
		if got := date.Format("2006-01-02"); got != want[i] {
			t.Fatalf("schedule[%d] = %s, want %s", i, got, want[i])
		}
	}
}

func TestScheduleDatesCoverAnOrdinaryContractPeriod(t *testing.T) {
	// The limit exists for typos, not for real contracts: three years of monthly
	// recognition must still activate.
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.December, 31, 0, 0, 0, 0, time.UTC)
	dates, err := buildScheduleDates(&start, &end, "MONTHLY")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != 36 {
		t.Fatalf("schedule length = %d, want 36", len(dates))
	}
}

func TestScheduleDatesRefuseAPeriodBeyondTheEntryLimit(t *testing.T) {
	// A mistyped end date used to be accepted in full: every month between the
	// two dates became one INSERT inside the activation transaction, and one row
	// in a schedule listing that has no limit of its own.
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name         string
		end          time.Time
		scheduleType string
	}{
		{"monthly to the year 9999", time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC), "MONTHLY"},
		{"quarterly to the year 9999", time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC), "QUARTERLY"},
		{"annual to the year 9999", time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC), "ANNUAL"},
		{"one month past the limit", time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, maxScheduleEntries, 0), "MONTHLY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dates, err := buildScheduleDates(&start, &tc.end, tc.scheduleType)
			if err == nil {
				t.Fatalf("a period producing %d entries was accepted", len(dates))
			}
			if dates != nil {
				t.Fatal("a rejected period must not hand back any dates")
			}
			// serviceError classifies by substring, so the refusal has to stay a
			// 400 rather than turn into a 404, 403 or 409.
			for _, phrase := range []string{"not found", "permission", "access denied", "already", "pending", "another user"} {
				if strings.Contains(err.Error(), phrase) {
					t.Fatalf("message %q contains %q and would be misclassified", err, phrase)
				}
			}
		})
	}
}

func TestScheduleDatesAllowExactlyTheEntryLimit(t *testing.T) {
	start := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, maxScheduleEntries-1, 0)
	dates, err := buildScheduleDates(&start, &end, "MONTHLY")
	if err != nil {
		t.Fatal(err)
	}
	if len(dates) != maxScheduleEntries {
		t.Fatalf("schedule length = %d, want %d", len(dates), maxScheduleEntries)
	}
}

func TestSplitScheduleAmountPreservesContractTotal(t *testing.T) {
	amounts := splitScheduleAmount(100, 3)
	if len(amounts) != 3 || amounts[0] != 33.34 || amounts[1] != 33.33 || amounts[2] != 33.33 {
		t.Fatalf("unexpected split: %#v", amounts)
	}
	var total float64
	for _, amount := range amounts {
		total += amount
	}
	if total != 100 {
		t.Fatalf("split total = %.2f, want 100.00", total)
	}
}

func TestCurrencyValidationRequiresKRWParity(t *testing.T) {
	if err := validateCurrency("USD", 1350.25); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrency("KRW", 1.1); err == nil {
		t.Fatal("KRW must not accept a non-parity exchange rate")
	}
	if err := validateCurrency("usd", 1350); err == nil {
		t.Fatal("lowercase ISO code must be rejected")
	}
}
