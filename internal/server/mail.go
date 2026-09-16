package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/hkjang/relio/internal/audit"
	"github.com/hkjang/relio/internal/mail"
	"github.com/hkjang/relio/internal/platform/httpx"
)

// adminMailDeliveries lists what left the building: every attempt, sent or
// failed, with subject and recipient but never the body.
func (s *Server) adminMailDeliveries(w http.ResponseWriter, r *http.Request) {
	if err := requireAdmin(principal(r), false); err != nil {
		s.serviceError(w, r, err)
		return
	}
	page, err := s.Mail.Deliveries(r.Context(), mail.Filter{
		Status: r.URL.Query().Get("status"),
		Event:  r.URL.Query().Get("event"),
		Limit:  httpx.IntQuery(r, "limit", 50, 1, 200),
	})
	if err != nil {
		s.serviceError(w, r, err)
		return
	}
	httpx.JSON(w, 200, page)
}

// adminSendTestMail sends one real message with the saved settings and reports
// the outcome in place, because relay settings are rarely right the first
// time. Unlike event mail it waits for the relay, so the administrator sees
// the actual error rather than a queued row.
func (s *Server) adminSendTestMail(w http.ResponseWriter, r *http.Request) {
	p := principal(r)
	if err := requireAdmin(p, true); err != nil {
		s.serviceError(w, r, err)
		return
	}
	var in struct {
		Recipient string `json:"recipient"`
	}
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if s.Mail == nil {
		httpx.ErrorJSON(w, r, http.StatusServiceUnavailable, "mail_unavailable", "메일 서비스가 구성되지 않았습니다.", nil)
		return
	}
	recipient := strings.TrimSpace(in.Recipient)
	if recipient == "" {
		recipient = strings.TrimSpace(p.Email)
	}
	if !strings.Contains(recipient, "@") || strings.ContainsAny(recipient, " \r\n<>,") {
		httpx.ErrorJSON(w, r, http.StatusBadRequest, "invalid_recipient", "받는 사람 메일 주소를 입력하세요.", nil)
		return
	}
	err := s.Mail.SendNow(r.Context(), mail.TestMessage(), p.UserID, recipient)
	s.Audit.Record(r.Context(), audit.Event{ActorID: p.UserID, ActorName: p.Username, Channel: "ADMIN", Action: "MAIL_TEST_SEND", Resource: "mail", ResourceID: recipient,
		After: map[string]any{"recipient": recipient, "sent": err == nil, "error": errorText(err)}, IP: httpx.ClientIP(r), RequestID: httpx.RequestID(r.Context()), UserAgent: r.UserAgent()})
	if errors.Is(err, mail.ErrDisabled) {
		httpx.ErrorJSON(w, r, http.StatusConflict, "mail_disabled", "메일 알림이 꺼져 있습니다. mail.enabled 를 켜고 저장한 뒤 다시 시도하세요.", nil)
		return
	}
	if err != nil {
		httpx.ErrorJSON(w, r, http.StatusBadGateway, "mail_send_failed", err.Error(), map[string]any{"recipient": recipient})
		return
	}
	httpx.JSON(w, 200, map[string]any{"sent": true, "recipient": recipient})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
