package server

import "testing"

func TestSeverityRankOrdersTheQueue(t *testing.T) {
	// The queue is sorted by this rank, so a warning must never outrank a
	// critical breach.
	if severityRank["CRITICAL"] >= severityRank["HIGH"] {
		t.Fatal("CRITICAL must sort before HIGH")
	}
	if severityRank["HIGH"] >= severityRank["WARNING"] {
		t.Fatal("HIGH must sort before WARNING")
	}
	if severityRank["WARNING"] >= severityRank["INFO"] {
		t.Fatal("WARNING must sort before INFO")
	}
}

func TestItoaMatchesTheSubtitleFormatting(t *testing.T) {
	for value, want := range map[int]string{0: "0", -5: "0", 1: "1", 9: "9", 10: "10", 45: "45", 120: "120"} {
		if got := itoa(value); got != want {
			t.Fatalf("itoa(%d) produced %q, expected %q", value, got, want)
		}
	}
}
