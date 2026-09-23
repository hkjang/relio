package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/platform/httpx"
)

func TestCreateCustomerRejectsOversizedJSONBeforeService(t *testing.T) {
	const limit = 2 << 20
	tests := []struct{ name, body string }{
		{"first value", `{"name":"` + strings.Repeat("x", limit) + `"}`},
		{"trailing whitespace", `{"name":"customer"}` + strings.Repeat(" ", limit)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No CRM service or DB is available: this must return at input decoding.
			s := &Server{}
			r := httptest.NewRequest(http.MethodPost, "/api/v1/customers", io.NopCloser(strings.NewReader(tt.body)))
			r = r.WithContext(httpx.WithRequestID(r.Context(), "customer-request"))
			w := httptest.NewRecorder()
			s.createCustomer(w, r)
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Errorf("status = %d, want 413", w.Code)
			}
			var envelope struct {
				Error httpx.Error `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Error.Code != "request_too_large" || envelope.Error.RequestID != "customer-request" {
				t.Errorf("error = %+v, want request_too_large and preserved requestId", envelope.Error)
			}
		})
	}
}
