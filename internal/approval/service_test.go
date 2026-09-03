package approval

import "testing"

func TestPolicyMatching(t *testing.T) {
	snapshot := map[string]any{"expected_amount": float64(500_000_000), "status": "OPEN"}
	tests := []struct {
		name   string
		policy Policy
		want   bool
	}{
		{"no condition", Policy{}, true},
		{"greater equal", Policy{ConditionField: "expected_amount", ConditionOperator: "GTE", ConditionValue: 500_000_000.0}, true},
		{"greater false", Policy{ConditionField: "expected_amount", ConditionOperator: "GT", ConditionValue: 500_000_000.0}, false},
		{"string equal", Policy{ConditionField: "status", ConditionOperator: "EQ", ConditionValue: "open"}, true},
		{"missing field", Policy{ConditionField: "discount_percent", ConditionOperator: "GTE", ConditionValue: 20}, false},
		{"numeric value written as text", Policy{ConditionField: "expected_amount", ConditionOperator: "GTE", ConditionValue: "400000000"}, true},
		{"operator is padded and lowercase", Policy{ConditionField: "expected_amount", ConditionOperator: " gte ", ConditionValue: 500_000_000.0}, true},
		{"empty operator means equal", Policy{ConditionField: "status", ConditionValue: "OPEN"}, true},

		// CONTAINS is a text test on both sides. Comparing the digits of an
		// amount as numbers made the policy never match, so the approval it
		// guards was skipped without a trace.
		{"contains on digits of an amount", Policy{ConditionField: "expected_amount", ConditionOperator: "CONTAINS", ConditionValue: "50"}, true},
		{"contains the whole amount", Policy{ConditionField: "expected_amount", ConditionOperator: "CONTAINS", ConditionValue: 500_000_000.0}, true},
		{"contains digits the amount does not have", Policy{ConditionField: "expected_amount", ConditionOperator: "CONTAINS", ConditionValue: "37"}, false},
		// A large float must not reach the comparison as "5e+08".
		{"contains an exponent form", Policy{ConditionField: "expected_amount", ConditionOperator: "CONTAINS", ConditionValue: "e+08"}, false},
		{"contains ignores case", Policy{ConditionField: "status", ConditionOperator: "CONTAINS", ConditionValue: "pe"}, true},

		// An ordering operator on text used to answer "equal", so a GT policy
		// fired on the one value it should have excluded.
		{"greater than on equal text", Policy{ConditionField: "status", ConditionOperator: "GT", ConditionValue: "OPEN"}, false},
		{"greater than on later text", Policy{ConditionField: "status", ConditionOperator: "GT", ConditionValue: "CLOSED"}, true},
		{"less than on text", Policy{ConditionField: "status", ConditionOperator: "LT", ConditionValue: "WON"}, true},
		{"greater equal on equal text", Policy{ConditionField: "status", ConditionOperator: "GTE", ConditionValue: "open"}, true},
		{"not equal on text ignores case", Policy{ConditionField: "status", ConditionOperator: "NE", ConditionValue: "open"}, false},
		{"not equal on different text", Policy{ConditionField: "status", ConditionOperator: "NE", ConditionValue: "WON"}, true},
		{"not equal on numbers", Policy{ConditionField: "expected_amount", ConditionOperator: "NE", ConditionValue: 10.0}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matches(tt.policy, snapshot); got != tt.want {
				t.Fatalf("matches()=%v want %v", got, tt.want)
			}
		})
	}
}

func TestConditionText(t *testing.T) {
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"a large amount keeps its digits", float64(500_000_000), "500000000"},
		{"a fraction keeps its decimals", 12.5, "12.5"},
		{"a whole number has no decimal point", float64(20), "20"},
		{"text is left alone", "OPEN", "OPEN"},
		{"an integer", 7, "7"},
		{"no value is empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conditionText(tt.value); got != tt.want {
				t.Fatalf("conditionText(%v)=%q want %q", tt.value, got, tt.want)
			}
		})
	}
}
