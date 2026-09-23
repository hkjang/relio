package httpx

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBodyLimitsAndErrors(t *testing.T) {
	const limit = 2 << 20
	const valid = `{"name":"customer"}`
	const invalidMessage = "JSON 요청 형식이 올바르지 않습니다."
	const trailingMessage = "요청에는 JSON 객체 하나만 허용됩니다."
	tests := []struct {
		name, body    string
		status        int
		code, message string
	}{
		{"small object", valid, 200, "", ""},
		{"below limit", valid + strings.Repeat(" ", limit-len(valid)-1), 200, "", ""},
		{"exact limit", valid + strings.Repeat(" ", limit-len(valid)), 200, "", ""},
		{"oversized first value", `{"name":"` + strings.Repeat("x", limit) + `"}`, 413, "request_too_large", "요청 본문이 너무 큽니다."},
		{"oversized trailing whitespace", valid + strings.Repeat(" ", limit-len(valid)+1), 413, "request_too_large", "요청 본문이 너무 큽니다."},
		{"malformed JSON", `{"name":`, 400, "invalid_json", invalidMessage},
		{"unknown field", `{"unknown":1}`, 400, "invalid_json", invalidMessage},
		{"two values", valid + `{}`, 400, "invalid_json", trailingMessage},
		{"malformed trailing data", valid + `!`, 400, "invalid_json", trailingMessage},
	}
	for _, tt := range tests {
		for _, unknownLength := range []bool{false, true} {
			mode := "known length"
			if unknownLength {
				mode = "stream"
			}
			t.Run(tt.name+"/"+mode, func(t *testing.T) {
				var body io.Reader = strings.NewReader(tt.body)
				if unknownLength {
					body = io.NopCloser(body)
				}
				r := httptest.NewRequest(http.MethodPost, "/", body)
				if unknownLength && r.ContentLength != -1 {
					t.Fatalf("stream ContentLength = %d", r.ContentLength)
				}
				r = r.WithContext(WithRequestID(r.Context(), "decode-request"))
				w := httptest.NewRecorder()
				var target struct {
					Name string `json:"name"`
				}
				ok := DecodeJSON(w, r, &target)
				if ok != (tt.status == 200) {
					t.Errorf("DecodeJSON = %v, want %v", ok, tt.status == 200)
				}
				if w.Code != tt.status {
					t.Errorf("status = %d, want %d", w.Code, tt.status)
				}
				if tt.status == 200 {
					if target.Name != "customer" {
						t.Errorf("decoded name = %q", target.Name)
					}
					if w.Body.Len() != 0 {
						t.Error("successful decode wrote a response")
					}
					return
				}
				var envelope errorEnvelope
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.Error.Code != tt.code || envelope.Error.Message != tt.message || envelope.Error.RequestID != "decode-request" {
					t.Errorf("error = %+v, want code %q, message %q and preserved requestId", envelope.Error, tt.code, tt.message)
				}
			})
		}
	}
}
