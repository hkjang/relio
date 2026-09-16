package mail

import (
	"fmt"
	"mime"
	"strings"
	"time"
)

// compose builds a MIME message. Korean subjects and bodies are encoded so
// relays and clients that predate UTF-8 headers still show them correctly.
func compose(config Config, message Message, now time.Time) string {
	var builder strings.Builder
	builder.WriteString("From: " + encodeAddress(config.Address()) + "\r\n")
	builder.WriteString("To: " + strings.TrimSpace(message.To) + "\r\n")
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n")
	builder.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	builder.WriteString("Auto-Submitted: auto-generated\r\n")
	builder.WriteString("X-Relio-Notification: 1\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(normalizeBody(message.Body))
	return builder.String()
}

func encodeAddress(address string) string {
	open := strings.LastIndex(address, "<")
	if open <= 0 {
		return address
	}
	return mime.QEncoding.Encode("utf-8", strings.TrimSpace(address[:open])) + " " + address[open:]
}

// normalizeBody uses CRLF line endings. Dot-stuffing is not done here: the
// writer smtp.Client.Data returns already escapes a leading dot, and doing it
// twice leaves a stray dot in the delivered text.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	if !strings.HasSuffix(body, "\r\n") {
		body += "\r\n"
	}
	return body
}

// Notification is the content of one event mail before recipients are resolved.
type Notification struct {
	Event      string
	Subject    string
	Lines      []string
	EntityType string
	EntityID   string
	// Link is the in-app path the mail points at, resolved against
	// mail.base_url (or system.service_url) when it is relative.
	Link string
}

// Render turns a notification into the message body, appending the link and a
// footer that says why the mail arrived.
func (n Notification) Render(config Config) string {
	lines := append([]string{}, n.Lines...)
	if link := n.absoluteLink(config); link != "" {
		lines = append(lines, "", "바로 열기: "+link)
	}
	lines = append(lines, "", "—", "이 메일은 Relio 메일 알림 설정에 따라 자동으로 발송되었습니다. 관리자가 관리자 콘솔 > 메일 알림에서 이벤트별로 끌 수 있습니다.")
	return strings.Join(lines, "\n")
}

func (n Notification) absoluteLink(config Config) string {
	base := strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if n.Link == "" {
		return ""
	}
	if strings.HasPrefix(n.Link, "http://") || strings.HasPrefix(n.Link, "https://") {
		return n.Link
	}
	if base == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(n.Link, "/")
}

// EntityLabel is the Korean name of an approval entity type as the screens
// show it, so a subject says "영업기회" rather than "OPPORTUNITY".
func EntityLabel(entityType string) string {
	switch strings.ToUpper(strings.TrimSpace(entityType)) {
	case "OPPORTUNITY":
		return "영업기회"
	case "QUOTATION":
		return "견적"
	case "CONTRACT":
		return "계약"
	case "CUSTOMER":
		return "고객"
	}
	return strings.TrimSpace(entityType)
}

// ApprovalRequested tells the approver that somebody is waiting on them.
func ApprovalRequested(requester, entityType, entityTitle, entityID, policy, reason string) Notification {
	label := EntityLabel(entityType)
	lines := []string{
		fmt.Sprintf("%s 님이 %s '%s'의 승인을 요청했습니다.", requester, label, entityTitle),
		fmt.Sprintf("승인 정책: %s", policy),
	}
	if strings.TrimSpace(reason) != "" {
		lines = append(lines, "", quote(reason))
	}
	lines = append(lines, "", "승인하거나 반려하기 전까지 요청자는 다음 단계로 나아갈 수 없습니다.")
	return Notification{
		Event:      EventApprovalRequested,
		Subject:    fmt.Sprintf("[Relio] 승인 요청: %s '%s'", label, entityTitle),
		EntityType: strings.ToUpper(entityType),
		EntityID:   entityID,
		Lines:      lines,
		Link:       "/app/approvals",
	}
}

// ApprovalDecided tells the requester what the approver decided.
func ApprovalDecided(approver, entityType, entityTitle, entityID, decision, comment string) Notification {
	label := EntityLabel(entityType)
	result, subject := "반려되었습니다", "반려"
	if strings.EqualFold(decision, "APPROVED") || strings.EqualFold(decision, "APPROVE") {
		result, subject = "승인되었습니다", "승인"
	}
	lines := []string{fmt.Sprintf("%s '%s'의 승인 요청이 %s 님에 의해 %s.", label, entityTitle, approver, result)}
	if strings.TrimSpace(comment) != "" {
		lines = append(lines, "", quote(comment))
	}
	return Notification{
		Event:      EventApprovalDecided,
		Subject:    fmt.Sprintf("[Relio] %s: %s '%s'", subject, label, entityTitle),
		EntityType: strings.ToUpper(entityType),
		EntityID:   entityID,
		Lines:      lines,
		Link:       "/app/approvals",
	}
}

// VoiceAssigned tells a new owner that a customer is waiting on them and the
// SLA clock is already running. The request body is not included: it can
// carry customer details, and the owner will open the record anyway.
func VoiceAssigned(actor, voiceNo, title, customer, severity, voiceID string, responseDue *time.Time, location *time.Location) Notification {
	lines := []string{
		fmt.Sprintf("%s 님이 고객 요청 %s '%s'의 담당자로 회원님을 지정했습니다.", actor, voiceNo, title),
		fmt.Sprintf("고객: %s · 심각도: %s", customer, severity),
	}
	if responseDue != nil {
		if location == nil {
			location = time.UTC
		}
		lines = append(lines, fmt.Sprintf("응답 기한: %s", responseDue.In(location).Format("2006-01-02 15:04")))
	}
	return Notification{
		Event:      EventVoiceAssigned,
		Subject:    fmt.Sprintf("[Relio] 고객 요청 배정: %s %s", voiceNo, title),
		EntityType: "CUSTOMER_VOICE",
		EntityID:   voiceID,
		Lines:      lines,
		// The list screen: a single record has no deep link of its own yet.
		Link: "/app/voices",
	}
}

// RenewalContract is one line of a renewal digest.
type RenewalContract struct {
	ID       string
	Number   string
	Title    string
	Customer string
	EndDate  time.Time
	DaysLeft int
}

// ContractsDueForRenewal bundles every contract that entered its renewal
// window for one owner into a single mail, so ten contracts on the same day
// are one message rather than ten.
func ContractsDueForRenewal(contracts []RenewalContract) Notification {
	subject := fmt.Sprintf("[Relio] 갱신 준비가 필요한 계약 %d건", len(contracts))
	if len(contracts) == 1 {
		subject = fmt.Sprintf("[Relio] 계약 갱신 준비: %s (D-%d)", contracts[0].Title, contracts[0].DaysLeft)
	}
	lines := []string{"담당 계약이 갱신 준비 기간에 들어왔지만 갱신 준비가 시작되지 않았습니다."}
	lines = append(lines, "")
	for _, contract := range contracts {
		lines = append(lines, fmt.Sprintf("- %s · %s (%s) · 종료 %s · D-%d", contract.Title, contract.Customer, contract.Number, contract.EndDate.Format("2006-01-02"), contract.DaysLeft))
	}
	lines = append(lines, "", "계약 화면에서 갱신 상태를 '계획됨' 이상으로 바꾸면 이 알림은 다시 오지 않습니다.")
	entityID := ""
	if len(contracts) == 1 {
		entityID = contracts[0].ID
	}
	return Notification{
		Event:      EventContractRenewal,
		Subject:    subject,
		EntityType: "CONTRACT",
		EntityID:   entityID,
		Lines:      lines,
		Link:       "/app/contracts",
	}
}

// TestMessage proves the relay works from the settings screen.
func TestMessage() Notification {
	return Notification{
		Event:   EventTest,
		Subject: "[Relio] SMTP 발송 테스트",
		Lines:   []string{"Relio 관리자 화면에서 보낸 테스트 메일입니다.", "이 메일을 받았다면 SMTP 설정이 정상입니다."},
	}
}

func quote(body string) string {
	trimmed := strings.TrimSpace(body)
	if len([]rune(trimmed)) > 500 {
		trimmed = string([]rune(trimmed)[:500]) + "…"
	}
	lines := strings.Split(trimmed, "\n")
	for index, line := range lines {
		lines[index] = "> " + line
	}
	return strings.Join(lines, "\n")
}
