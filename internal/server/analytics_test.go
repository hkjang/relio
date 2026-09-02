package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/analytics"
)

func TestReportedDirectiveSurvivesAReportThatNamesNothing(t *testing.T) {
	cases := []struct {
		name, effective, violated, want string
	}{
		{"effective directive wins", "script-src-elem", "script-src 'self'", "script-src-elem"},
		{"violated directive keeps only its first token", "", "script-src 'self' https://cdn.example.com", "script-src"},
		{"neither field is present", "", "", ""},
		{"violated directive is only whitespace", "", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reportedDirective(c.effective, c.violated); got != c.want {
				t.Fatalf("reportedDirective(%q,%q)=%q, want %q", c.effective, c.violated, got, c.want)
			}
		})
	}
}

// /api/v1/csp-report is unauthenticated, so a report the browser is entitled to
// send must never take the handler down: both directive fields are optional and
// reading the first word of an absent one panicked with an index out of range.
func TestCSPReportWithoutADirectiveIsAcceptedNotAPanic(t *testing.T) {
	s := &Server{Analytics: &analytics.Service{}}
	body := `{"csp-report":{"blocked-uri":"https://cdn.example.com/a.js","document-uri":"https://relio.example/app"}}`
	w := httptest.NewRecorder()
	s.cspReport(w, httptest.NewRequest("POST", "/api/v1/csp-report", strings.NewReader(body)))
	if w.Code != 204 {
		t.Fatalf("status=%d, want 204", w.Code)
	}
}
