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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matches(tt.policy, snapshot); got != tt.want {
				t.Fatalf("matches()=%v want %v", got, tt.want)
			}
		})
	}
}
