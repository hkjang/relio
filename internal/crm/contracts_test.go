package crm

import (
	"testing"
	"time"
)

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
