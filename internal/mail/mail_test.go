package mail

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRelay is a minimal SMTP server. It records the conversation so a test can
// assert what Relio actually said, including whether it tried to authenticate.
type fakeRelay struct {
	address    string
	offerAuth  bool
	rejectFrom bool
	mu         sync.Mutex
	commands   []string
	body       string
	listener   net.Listener
}

func startRelay(t *testing.T, offerAuth bool) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	relay := &fakeRelay{address: listener.Addr().String(), offerAuth: offerAuth, listener: listener}
	go relay.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (f *fakeRelay) host() string { host, _, _ := net.SplitHostPort(f.address); return host }
func (f *fakeRelay) port() int {
	_, port, _ := net.SplitHostPort(f.address)
	value := 0
	_, _ = fmt.Sscanf(port, "%d", &value)
	return value
}

func (f *fakeRelay) record(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, line)
}

func (f *fakeRelay) transcript() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...)
}

func (f *fakeRelay) message() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body
}

func (f *fakeRelay) serve() {
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(connection)
	}
}

func (f *fakeRelay) handle(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }
	write("220 relay.internal ESMTP relio-test")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.TrimSpace(line)
		f.record(command)
		upper := strings.ToUpper(command)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-relay.internal")
			if f.offerAuth {
				write("250-AUTH PLAIN LOGIN")
			}
			write("250 SIZE 35882577")
		case strings.HasPrefix(upper, "HELO"):
			write("250 relay.internal")
		case strings.HasPrefix(upper, "AUTH"):
			write("235 2.7.0 Authentication successful")
		case strings.HasPrefix(upper, "MAIL FROM"):
			if f.rejectFrom {
				write("550 5.7.1 Sender rejected")
				continue
			}
			write("250 2.1.0 Ok")
		case strings.HasPrefix(upper, "RCPT TO"):
			write("250 2.1.5 Ok")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			var body strings.Builder
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				body.WriteString(dataLine)
			}
			f.mu.Lock()
			f.body = body.String()
			f.mu.Unlock()
			write("250 2.0.0 Ok: queued")
		case upper == "QUIT":
			write("221 2.0.0 Bye")
			return
		default:
			write("250 2.0.0 Ok")
		}
	}
}

func relayConfig(relay *fakeRelay) Config {
	return Config{Enabled: true, Host: relay.host(), Port: relay.port(), FromAddress: "relio@corp.example",
		FromName: "Relio 알림", Security: "auto", Timeout: 3 * time.Second, Events: map[string]bool{}}
}

// An internal relay on port 25 that asks for nothing must work with no
// credentials and no TLS: that is the common case the defaults aim at.
func TestDeliverWithoutAuthentication(t *testing.T) {
	relay := startRelay(t, false)
	config := relayConfig(relay)
	err := Deliver(context.Background(), config, Message{To: "hong@corp.example", Subject: "승인 요청: 계약 '데모'", Body: "첫 줄\n.둘째 줄은 점으로 시작\n"})
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	transcript := strings.Join(relay.transcript(), "\n")
	if strings.Contains(transcript, "AUTH") {
		t.Fatalf("authenticated against a relay that offers no AUTH:\n%s", transcript)
	}
	if !strings.Contains(transcript, "EHLO corp.example") {
		t.Fatalf("EHLO should carry the sender domain:\n%s", transcript)
	}
	if !strings.Contains(transcript, "MAIL FROM:<relio@corp.example>") || !strings.Contains(transcript, "RCPT TO:<hong@corp.example>") {
		t.Fatalf("envelope missing:\n%s", transcript)
	}
	body := relay.message()
	for _, want := range []string{"Subject: =?utf-8?q?", "Content-Type: text/plain; charset=UTF-8", "Auto-Submitted: auto-generated", "X-Relio-Notification: 1", "From: =?utf-8?q?Relio_=EC=95=8C=EB=A6=BC?= <relio@corp.example>", "\r\n..둘째 줄은 점으로 시작\r\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("message lacks %q:\n%s", want, body)
		}
	}
}

func TestDeliverAuthenticatesWhenConfigured(t *testing.T) {
	relay := startRelay(t, true)
	config := relayConfig(relay)
	config.Username, config.Password = "relio", "secret"
	if err := Deliver(context.Background(), config, Message{To: "hong@corp.example", Subject: "x", Body: "y"}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if transcript := strings.Join(relay.transcript(), "\n"); !strings.Contains(transcript, "AUTH PLAIN") {
		t.Fatalf("expected PLAIN authentication:\n%s", transcript)
	}
}

func TestDeliverExplainsMissingAuthSupport(t *testing.T) {
	relay := startRelay(t, false)
	config := relayConfig(relay)
	config.Username, config.Password = "relio", "secret"
	err := Deliver(context.Background(), config, Message{To: "hong@corp.example", Subject: "x", Body: "y"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "사용자 이름을 비우고") {
		t.Fatalf("expected a hint to clear the username, got %v", err)
	}
}

func TestDeliverReportsRejection(t *testing.T) {
	relay := startRelay(t, false)
	relay.rejectFrom = true
	err := Deliver(context.Background(), relayConfig(relay), Message{To: "hong@corp.example", Subject: "x", Body: "y"})
	if err == nil || !strings.Contains(err.Error(), "MAIL FROM") {
		t.Fatalf("expected the rejected step to be named, got %v", err)
	}
}

func TestDeliverFailsFastWhenTheRelayIsDown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	host, port, _ := net.SplitHostPort(address)
	config := Config{Enabled: true, Host: host, FromAddress: "relio@corp.example", Security: "auto", Timeout: time.Second}
	_, _ = fmt.Sscanf(port, "%d", &config.Port)
	started := time.Now()
	err = Deliver(context.Background(), config, Message{To: "hong@corp.example", Subject: "x", Body: "y"})
	if err == nil {
		t.Fatal("expected a connection error")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("a dead relay took %s to report", time.Since(started))
	}
}

func TestConfigValidationAndDefaults(t *testing.T) {
	config := FromValues(map[string]any{})
	if config.Enabled || config.Port != 25 || config.Security != "auto" || config.Timeout != 10*time.Second || config.FromName != "Relio" {
		t.Fatalf("defaults should be off, port 25, auto, 10s, Relio: %+v", config)
	}
	if err := config.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "mail.smtp_host") {
		t.Fatalf("an empty host must be named as the reason, got %v", err)
	}
	config = FromValues(map[string]any{"enabled": true, "smtp_host": "relay.internal", "smtp_port": float64(465), "username": "u", "password": "p",
		"notify_approval_requested": false, "timeout_seconds": json.Number("30"), "skip_tls_verify": "true"})
	if !config.Enabled || config.Port != 465 || config.Security != "tls" || config.Timeout != 30*time.Second || !config.SkipVerify {
		t.Fatalf("stored values were not read: %+v", config)
	}
	if config.FromAddress != "relio@relay.internal" {
		t.Fatalf("from address should default to the relay domain, got %q", config.FromAddress)
	}
	if config.Allows(EventApprovalRequested) || !config.Allows(EventApprovalDecided) || !config.Allows("something.new") {
		t.Fatalf("event switches: %+v", config.Events)
	}
	if err := config.Validate(); err != nil {
		t.Fatalf("a host and a from address are enough: %v", err)
	}
	bad := config
	bad.Security = "ssl"
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown security mode must be rejected, got %v", err)
	}
}

// The settings API never returns the password, and neither may the config
// value the server hands around: the credentials are not JSON at all.
func TestConfigJSONOmitsCredentials(t *testing.T) {
	raw, err := json.Marshal(Config{Host: "relay.internal", Username: "relio", Password: "hunter2", FromAddress: "relio@corp.example"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "relio\"") && strings.Contains(string(raw), "username") {
		t.Fatalf("credentials leaked into JSON: %s", raw)
	}
}

func TestEverySwitchedEventHasASeededSetting(t *testing.T) {
	keys := EventSettingKeys()
	for _, event := range []string{EventApprovalRequested, EventApprovalDecided, EventVoiceAssigned, EventContractRenewal} {
		if _, ok := keys[event]; !ok {
			t.Errorf("%s has no mail.notify_* switch", event)
		}
	}
	for event, key := range keys {
		if !strings.HasPrefix(key, "mail.notify_") {
			t.Errorf("%s switch %q does not follow the mail.notify_<event> convention", event, key)
		}
	}
}

func TestNotificationRendersLinkAndFooter(t *testing.T) {
	notification := ApprovalRequested("홍길동", "CONTRACT", "데모전자 유지보수", "c1", "고액 계약", "빠른 검토 부탁드립니다")
	body := notification.Render(Config{BaseURL: "https://crm.corp.example/"})
	for _, want := range []string{"홍길동 님이 계약 '데모전자 유지보수'의 승인을 요청했습니다.", "승인 정책: 고액 계약", "> 빠른 검토 부탁드립니다", "바로 열기: https://crm.corp.example/app/approvals", "자동으로 발송되었습니다"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(ApprovalDecided("팀장", "OPPORTUNITY", "딜", "o1", "REJECTED", "").Render(Config{}), "바로 열기") {
		t.Fatal("without a base URL there is no link to render")
	}
	if subject := ApprovalDecided("팀장", "QUOTATION", "견적서", "q1", "APPROVED", "").Subject; subject != "[Relio] 승인: 견적 '견적서'" {
		t.Fatalf("subject: %q", subject)
	}
	due := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
	seoul, _ := time.LoadLocation("Asia/Seoul")
	assigned := VoiceAssigned("팀장", "VOC-2026-000012", "납기 지연 문의", "데모전자(주)", "HIGH", "v1", &due, seoul)
	if body := assigned.Render(Config{BaseURL: "https://crm"}); !strings.Contains(body, "응답 기한: 2026-09-16 12:00") || !strings.Contains(body, "https://crm/app/voices") {
		t.Fatalf("assignment body:\n%s", body)
	}
	digest := ContractsDueForRenewal([]RenewalContract{
		{ID: "a", Number: "C-1", Title: "유지보수 A", Customer: "데모전자", EndDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), DaysLeft: 76},
		{ID: "b", Number: "C-2", Title: "유지보수 B", Customer: "데모전자", EndDate: time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC), DaysLeft: 90},
	})
	if digest.Subject != "[Relio] 갱신 준비가 필요한 계약 2건" || digest.EntityID != "" {
		t.Fatalf("digest subject %q entity %q", digest.Subject, digest.EntityID)
	}
	if body := digest.Render(Config{}); !strings.Contains(body, "- 유지보수 A · 데모전자 (C-1) · 종료 2026-12-01 · D-76") || !strings.Contains(body, "- 유지보수 B") {
		t.Fatalf("digest body:\n%s", body)
	}
	if single := ContractsDueForRenewal(digest2()); single.Subject != "[Relio] 계약 갱신 준비: 유지보수 A (D-76)" || single.EntityID != "a" {
		t.Fatalf("single-contract digest: %q %q", single.Subject, single.EntityID)
	}
}

func digest2() []RenewalContract {
	return []RenewalContract{{ID: "a", Number: "C-1", Title: "유지보수 A", Customer: "데모전자", EndDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), DaysLeft: 76}}
}
