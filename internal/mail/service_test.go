package mail

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSettings struct {
	mail   map[string]any
	system map[string]any
	err    error
}

func (f fakeSettings) Values(_ context.Context, namespace string) (map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	if namespace == "system" {
		return f.system, nil
	}
	return f.mail, nil
}

type fakeDirectory map[string]string

func (d fakeDirectory) LookupEmails(_ context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	for _, id := range ids {
		if email, ok := d[strings.ToLower(id)]; ok {
			out[strings.ToLower(id)] = email
		}
	}
	return out, nil
}

// harness is a service with a recording transport and no database.
type harness struct {
	service    *Service
	mu         sync.Mutex
	sent       []Message
	deliveries []Delivery
	fail       error
}

func newHarness(settings fakeSettings, directory Directory) *harness {
	h := &harness{}
	h.service = NewService(nil, settings, directory, slog.New(slog.DiscardHandler))
	h.service.SetSender(func(_ context.Context, _ Config, message Message) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.fail != nil {
			return h.fail
		}
		h.sent = append(h.sent, message)
		return nil
	})
	h.service.observe = func(delivery Delivery) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.deliveries = append(h.deliveries, delivery)
	}
	return h
}

func (h *harness) messages() []Message {
	h.service.wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Message(nil), h.sent...)
}

func (h *harness) log() []Delivery {
	h.service.wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Delivery(nil), h.deliveries...)
}

func enabled(extra map[string]any) fakeSettings {
	values := map[string]any{"enabled": true, "smtp_host": "relay.internal", "from_address": "relio@corp.example"}
	for key, value := range extra {
		values[key] = value
	}
	return fakeSettings{mail: values, system: map[string]any{"service_url": "https://crm.corp.example"}}
}

var people = fakeDirectory{"u-approver": "approver@corp.example", "u-requester": "requester@corp.example", "u-noaddress": ""}

// Off is the default and off means nothing leaves — not even a log row.
func TestDisabledSendsNothing(t *testing.T) {
	h := newHarness(fakeSettings{mail: map[string]any{"smtp_host": "relay.internal"}}, people)
	h.service.Notify(context.Background(), TestMessage(), "u-requester", []string{"u-approver"})
	if got := h.messages(); len(got) != 0 {
		t.Fatalf("disabled mail sent %d messages", len(got))
	}
	if got := h.log(); len(got) != 0 {
		t.Fatalf("disabled mail recorded %d deliveries", len(got))
	}
	if err := h.service.SendNow(context.Background(), TestMessage(), "u-requester", "x@corp.example"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("the test button must say mail is disabled, got %v", err)
	}
}

// Enabled but incomplete: nothing is sent, and the log says why.
func TestIncompleteConfigurationIsRecordedNotSent(t *testing.T) {
	h := newHarness(fakeSettings{mail: map[string]any{"enabled": true}}, people)
	h.service.Notify(context.Background(), ApprovalRequested("홍길동", "CONTRACT", "데모", "c1", "정책", ""), "u-requester", []string{"u-approver"})
	if got := h.messages(); len(got) != 0 {
		t.Fatalf("sent %d messages without a host", len(got))
	}
	log := h.log()
	if len(log) != 1 || log[0].Status != "failed" || !strings.Contains(log[0].ErrorMessage, "mail.smtp_host") {
		t.Fatalf("expected one failed delivery naming the missing host, got %+v", log)
	}
}

// The request never waits on the relay: Notify returns while the transport is
// still blocked, and a relay that is down makes a failed row, not an error.
func TestNotifyNeverBlocksTheCaller(t *testing.T) {
	h := newHarness(enabled(nil), people)
	release := make(chan struct{})
	h.service.SetSender(func(ctx context.Context, _ Config, _ Message) error {
		select {
		case <-release:
			return errors.New("SMTP 연결 실패: connection refused")
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	started := time.Now()
	h.service.Notify(context.Background(), TestMessage(), "u-requester", []string{"u-approver"})
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Notify waited %s on the transport", elapsed)
	}
	close(release)
	log := h.log()
	if len(log) != 1 || log[0].Status != "failed" || log[0].Attempts != 2 || !strings.Contains(log[0].ErrorMessage, "connection refused") {
		t.Fatalf("a dead relay should leave one failed row after two attempts, got %+v", log)
	}
}

// Cancelling the request must not cancel the mail: the delivery runs on a
// context detached from the handler that started it.
func TestDeliveryOutlivesTheRequest(t *testing.T) {
	h := newHarness(enabled(nil), people)
	ctx, cancel := context.WithCancel(context.Background())
	h.service.Notify(ctx, TestMessage(), "u-requester", []string{"u-approver"})
	cancel()
	if got := h.messages(); len(got) != 1 {
		t.Fatalf("expected the mail to be sent after the request ended, got %d", len(got))
	}
}

// Nobody is told about their own action, duplicates collapse, and recipients
// without an address are skipped without a row.
func TestActorIsExcludedAndRecipientsDeduplicated(t *testing.T) {
	h := newHarness(enabled(nil), people)
	h.service.Notify(context.Background(), TestMessage(), "u-approver", []string{"u-approver", "U-REQUESTER", "u-requester", "u-noaddress", "u-unknown", ""})
	got := h.messages()
	if len(got) != 1 || got[0].To != "requester@corp.example" {
		t.Fatalf("expected exactly the requester, got %+v", got)
	}
	log := h.log()
	if len(log) != 1 || log[0].ActorID != "u-approver" || log[0].Status != "sent" {
		t.Fatalf("expected one sent row carrying the actor, got %+v", log)
	}
	h2 := newHarness(enabled(nil), people)
	h2.service.Notify(context.Background(), TestMessage(), "u-approver", []string{"u-approver"})
	if got := h2.messages(); len(got) != 0 {
		t.Fatalf("the actor received mail about their own action: %+v", got)
	}
}

// Switching one event off stops that event only.
func TestEventSwitchStopsOnlyThatEvent(t *testing.T) {
	h := newHarness(enabled(map[string]any{"notify_approval_requested": false}), people)
	h.service.Notify(context.Background(), ApprovalRequested("홍길동", "CONTRACT", "데모", "c1", "정책", ""), "u-requester", []string{"u-approver"})
	h.service.Notify(context.Background(), ApprovalDecided("팀장", "CONTRACT", "데모", "c1", "APPROVED", ""), "u-approver", []string{"u-requester"})
	got := h.messages()
	if len(got) != 1 || !strings.HasPrefix(got[0].Subject, "[Relio] 승인:") {
		t.Fatalf("expected only the decision to be sent, got %+v", got)
	}
}

// Success and failure both land in the log, with subject and recipient and
// without the body.
func TestLogKeepsSuccessAndFailureWithoutTheBody(t *testing.T) {
	h := newHarness(enabled(nil), people)
	h.service.Notify(context.Background(), ApprovalRequested("홍길동", "CONTRACT", "데모전자 유지보수", "c1", "정책", "본문에만 있는 사유"), "u-requester", []string{"u-approver"})
	h.service.wait()
	h.mu.Lock()
	h.fail = errors.New("550 5.7.1 Sender rejected")
	h.mu.Unlock()
	h.service.Notify(context.Background(), ApprovalDecided("팀장", "CONTRACT", "데모전자 유지보수", "c1", "REJECTED", ""), "u-approver", []string{"u-requester"})
	log := h.log()
	if len(log) != 2 {
		t.Fatalf("expected two rows, got %+v", log)
	}
	statuses := map[string]Delivery{}
	for _, row := range log {
		statuses[row.Status] = row
	}
	sent, failed := statuses["sent"], statuses["failed"]
	if sent.Recipient != "approver@corp.example" || sent.Subject != "[Relio] 승인 요청: 계약 '데모전자 유지보수'" || sent.EntityType != "CONTRACT" || sent.EntityID != "c1" {
		t.Fatalf("sent row: %+v", sent)
	}
	if failed.Recipient != "requester@corp.example" || !strings.Contains(failed.ErrorMessage, "Sender rejected") {
		t.Fatalf("failed row: %+v", failed)
	}
	for _, row := range log {
		if strings.Contains(row.Subject, "본문에만") || strings.Contains(row.ErrorMessage, "본문에만") {
			t.Fatalf("the body reached the log: %+v", row)
		}
	}
}

// The renewal digest writes its ledger from dispatch's count, so that count
// must be deliveries the relay accepted — a failed attempt is logged but has
// notified nobody, and the next tick has to try again.
func TestDispatchCountsOnlyAcceptedDeliveries(t *testing.T) {
	h := newHarness(enabled(nil), people)
	digest := ContractsDueForRenewal(digest2())
	if sent := h.service.dispatch(context.Background(), digest, "", []string{"u-approver", "u-requester"}); sent != 2 {
		t.Fatalf("two working recipients should count as 2, got %d", sent)
	}
	h.mu.Lock()
	h.fail = errors.New("SMTP 연결 실패: connection refused")
	h.mu.Unlock()
	if sent := h.service.dispatch(context.Background(), digest, "", []string{"u-approver"}); sent != 0 {
		t.Fatalf("a relay that is down must count as nobody notified, got %d", sent)
	}
	log := h.log()
	if len(log) != 3 || log[2].Status != "failed" || log[2].Attempts != 2 {
		t.Fatalf("the failed attempt must still be in the log after two tries, got %+v", log)
	}
	if sent := h.service.dispatch(context.Background(), digest, "", []string{"u-noaddress"}); sent != 0 {
		t.Fatalf("a recipient without an address counts as nobody notified, got %d", sent)
	}
	incomplete := newHarness(fakeSettings{mail: map[string]any{"enabled": true}}, people)
	if sent := incomplete.service.dispatch(context.Background(), digest, "", []string{"u-approver"}); sent != 0 {
		t.Fatalf("an incomplete configuration records a failed row but notifies nobody, got %d", sent)
	}
}

// mail.base_url falls back to system.service_url, so links work without a
// second copy of the address; a settings read failure sends nothing.
func TestBaseURLFallsBackToServiceURL(t *testing.T) {
	config, err := ReadConfig(context.Background(), enabled(nil))
	if err != nil || config.BaseURL != "https://crm.corp.example" {
		t.Fatalf("expected the service URL, got %q (%v)", config.BaseURL, err)
	}
	config, _ = ReadConfig(context.Background(), enabled(map[string]any{"base_url": "https://mail-links.example/"}))
	if config.BaseURL != "https://mail-links.example/" {
		t.Fatalf("an explicit base_url must win, got %q", config.BaseURL)
	}
	h := newHarness(fakeSettings{err: errors.New("database is away")}, people)
	h.service.Notify(context.Background(), TestMessage(), "", []string{"u-approver"})
	if got := h.messages(); len(got) != 0 {
		t.Fatalf("sent without settings: %+v", got)
	}
}

// A nil service is a valid Notifier so domain packages never check wiring.
func TestNilServiceIsANoOp(t *testing.T) {
	var service *Service
	var notifier Notifier = service
	notifier.Notify(context.Background(), TestMessage(), "", []string{"u-approver"})
	if page, err := service.Deliveries(context.Background(), Filter{}); err != nil || len(page.Items) != 0 || page.Summary.Total != 0 {
		t.Fatalf("nil service deliveries: %+v %v", page, err)
	}
	if err := service.NotifyRenewals(context.Background()); err != nil {
		t.Fatalf("nil service renewals: %v", err)
	}
}
