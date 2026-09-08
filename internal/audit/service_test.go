package audit

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// TestNullableJSONLeavesAnAbsentPayloadNull keeps the one case that is meant to
// be NULL distinguishable: an event that carries no before or after at all.
func TestNullableJSONLeavesAnAbsentPayloadNull(t *testing.T) {
	if got := nullableJSON(nil); got != nil {
		t.Errorf("nullableJSON(nil) = %q, want nil so the column is NULL", got)
	}
}

// TestNullableJSONEncodesAnOrdinaryPayload is the common path: what a caller
// hands over has to arrive in the column unchanged.
func TestNullableJSONEncodesAnOrdinaryPayload(t *testing.T) {
	cases := map[string]any{
		`{"username":"admin"}`: map[string]any{"username": "admin"},
		`{"amount":1500}`:      map[string]any{"amount": 1500},
		`null`:                 (map[string]any)(nil),
		`[]`:                   []string{},
		`"done"`:               "done",
	}
	for want, in := range cases {
		if got := string(nullableJSON(in)); got != want {
			t.Errorf("nullableJSON(%#v) = %s, want %s", in, got, want)
		}
	}
}

// TestNullableJSONRecordsWhatItCouldNotEncode is the reason this function has a
// test. The error used to be dropped, so a payload the encoder refuses — a
// PostgreSQL numeric that scanned as NaN, a cycle, a type json cannot represent
// — was written as SQL NULL. The row then said a record changed and no longer
// said how, and read exactly like an event that never carried a payload.
func TestNullableJSONRecordsWhatItCouldNotEncode(t *testing.T) {
	type cyclic struct {
		Name string
		Self *cyclic
	}
	loop := &cyclic{Name: "opportunity"}
	loop.Self = loop

	refused := map[string]any{
		"NaN amount":     map[string]any{"amount": math.NaN()},
		"infinite score": map[string]any{"score": math.Inf(1)},
		"cycle":          loop,
		"channel":        map[string]any{"ch": make(chan int)},
		"func":           map[string]any{"fn": func() {}},
	}
	for name, in := range refused {
		got := nullableJSON(in)
		if got == nil {
			t.Errorf("nullableJSON(%s) = nil, which the insert writes as NULL", name)
			continue
		}
		if !json.Valid(got) {
			t.Errorf("nullableJSON(%s) = %s, which a jsonb column rejects", name, got)
			continue
		}
		if !strings.Contains(string(got), "encodeError") {
			t.Errorf("nullableJSON(%s) = %s, want it to say the payload could not be encoded", name, got)
		}
	}
}

// TestNullableJSONAlwaysReturnsNullOrValidJSON states the contract the insert
// depends on. before_data, after_data and metadata are jsonb, so bytes that are
// not JSON fail the whole statement and lose the event rather than a column.
func TestNullableJSONAlwaysReturnsNullOrValidJSON(t *testing.T) {
	inputs := []any{
		nil,
		"",
		0,
		map[string]any{"a": 1},
		[]any{1, "two", nil},
		math.NaN(),
		map[string]any{"nested": map[string]any{"bad": math.Inf(-1)}},
		struct{ Ch chan int }{make(chan int)},
	}
	for _, in := range inputs {
		got := nullableJSON(in)
		if got == nil {
			continue
		}
		if !json.Valid(got) {
			t.Errorf("nullableJSON(%#v) = %q, which is not valid JSON", in, got)
		}
	}
}
