// Package mail delivers event notifications through a company SMTP relay.
//
// Internal relays commonly accept mail on port 25 with no credentials and no
// TLS, so authentication and encryption are optional and the transport adapts
// to whatever the server advertises. Nothing here blocks a request: sending
// happens in the background and every attempt is recorded so an administrator
// can see what left the building.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var (
	ErrDisabled = errors.New("mail is disabled")
	ErrInvalid  = errors.New("invalid mail configuration")
)

// Event names. Each one has a mail.notify_* switch (see eventSettings), and the
// name is what the delivery log and the audit trail record.
const (
	EventApprovalRequested = "approval.requested"
	EventApprovalDecided   = "approval.decided"
	EventVoiceAssigned     = "voice.assigned"
	EventContractRenewal   = "contract.renewal_due"
	EventTest              = "test"
)

type Config struct {
	Enabled     bool          `json:"enabled"`
	Host        string        `json:"host"`
	Port        int           `json:"port"`
	Username    string        `json:"-"`
	Password    string        `json:"-"`
	FromAddress string        `json:"fromAddress"`
	FromName    string        `json:"fromName"`
	Security    string        `json:"security"`
	SkipVerify  bool          `json:"skipVerify"`
	BaseURL     string        `json:"baseUrl"`
	Timeout     time.Duration `json:"-"`
	Events      map[string]bool
}

// Address is the RFC 5322 From header value.
func (c Config) Address() string {
	from := strings.TrimSpace(c.FromAddress)
	if name := strings.TrimSpace(c.FromName); name != "" {
		return fmt.Sprintf("%s <%s>", name, from)
	}
	return from
}

func (c Config) endpoint() string { return net.JoinHostPort(c.Host, fmt.Sprint(c.Port)) }

// Allows reports whether an event should be delivered. Unknown events are sent,
// so adding a notification never requires a settings change first.
func (c Config) Allows(event string) bool {
	if enabled, known := c.Events[event]; known {
		return enabled
	}
	return true
}

// Validate reports why the configuration cannot send. It is what the settings
// screen and the delivery log show when mail is on but incomplete.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("%w: mail.smtp_host is required", ErrInvalid)
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: mail.smtp_port must be between 1 and 65535", ErrInvalid)
	}
	if !strings.Contains(c.FromAddress, "@") {
		return fmt.Errorf("%w: mail.from_address must be an email address", ErrInvalid)
	}
	switch c.Security {
	case "auto", "none", "starttls", "tls":
	default:
		return fmt.Errorf("%w: mail.security must be auto, none, starttls, or tls", ErrInvalid)
	}
	return nil
}

type Message struct {
	To      string
	Subject string
	Body    string
}

// Deliver opens a connection and sends one message. It is exported so the
// settings screen can prove the relay works before anything depends on it.
func Deliver(ctx context.Context, config Config, message Message) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(message.To) == "" {
		return fmt.Errorf("%w: recipient is required", ErrInvalid)
	}
	client, err := dial(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := startSession(client, config); err != nil {
		return err
	}
	if err := client.Mail(strings.TrimSpace(config.FromAddress)); err != nil {
		return fmt.Errorf("MAIL FROM 실패: %w", err)
	}
	if err := client.Rcpt(strings.TrimSpace(message.To)); err != nil {
		return fmt.Errorf("RCPT TO 실패: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA 실패: %w", err)
	}
	if _, err := writer.Write([]byte(compose(config, message, time.Now()))); err != nil {
		return fmt.Errorf("본문 전송 실패: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("본문 종료 실패: %w", err)
	}
	return client.Quit()
}

func dial(ctx context.Context, config Config) (*smtp.Client, error) {
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	dialer := &net.Dialer{Timeout: timeout}
	var connection net.Conn
	var err error
	if config.Security == "tls" {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: config.tlsConfig()}
		connection, err = tlsDialer.DialContext(ctx, "tcp", config.endpoint())
		if err != nil {
			return nil, fmt.Errorf("SMTP TLS 연결 실패: %w", err)
		}
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", config.endpoint())
		if err != nil {
			return nil, fmt.Errorf("SMTP 연결 실패: %w", err)
		}
	}
	// The whole session, not just the dial, has to end: a relay that accepts the
	// connection and then goes quiet would otherwise hold the goroutine forever.
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(timeout))
	}
	client, err := smtp.NewClient(connection, config.Host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SMTP 세션 시작 실패: %w", err)
	}
	return client, nil
}

// startSession upgrades and authenticates only as far as the relay allows, so
// an unauthenticated internal relay works with the same settings as a hosted
// provider that demands both.
func startSession(client *smtp.Client, config Config) error {
	if err := client.Hello(helloName(config)); err != nil {
		return fmt.Errorf("EHLO 실패: %w", err)
	}
	if config.Security == "starttls" || config.Security == "auto" {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(config.tlsConfig()); err != nil {
				return fmt.Errorf("STARTTLS 실패: %w", err)
			}
		} else if config.Security == "starttls" {
			return fmt.Errorf("%w: 서버가 STARTTLS를 지원하지 않습니다", ErrInvalid)
		}
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil
	}
	supported, mechanisms := client.Extension("AUTH")
	if !supported {
		return fmt.Errorf("%w: 서버가 인증을 지원하지 않습니다. 사용자 이름을 비우고 사용하세요", ErrInvalid)
	}
	// The password leaves in the clear with PLAIN and LOGIN alike, so both
	// follow one rule (ADMIN_GUIDE 3.6): only over TLS, unless the administrator
	// chose security=none on purpose. CRAM-MD5 never sends the password itself,
	// so it is the way out when a plaintext relay happens to offer it.
	_, encrypted := client.TLSConnectionState()
	plaintextAllowed := encrypted || config.Security == "none"
	offered := strings.ToUpper(mechanisms)
	var auth smtp.Auth
	switch {
	case strings.Contains(offered, "CRAM-MD5") && !plaintextAllowed:
		auth = smtp.CRAMMD5Auth(config.Username, config.Password)
	case strings.Contains(offered, "PLAIN"), strings.Contains(offered, "LOGIN"):
		if !plaintextAllowed {
			return fmt.Errorf("%w: %s", ErrInvalid, errUnencrypted)
		}
		// Each mechanism re-checks the same rule against what the client saw,
		// so neither can be reused elsewhere without it.
		if strings.Contains(offered, "PLAIN") {
			auth = plainAuth{username: config.Username, password: config.Password, plaintext: config.Security == "none"}
		} else {
			auth = loginAuth{username: config.Username, password: config.Password, plaintext: config.Security == "none"}
		}
	default:
		auth = smtp.CRAMMD5Auth(config.Username, config.Password)
	}
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("SMTP 인증 실패: %w", err)
	}
	return nil
}

// errUnencrypted is the policy in one sentence, reused by every mechanism.
var errUnencrypted = errors.New("암호화되지 않은 연결에서는 자격증명을 보내지 않습니다. 릴레이가 STARTTLS 를 알리게 하거나, 평문 인증이 허용되는 릴레이라면 mail.security=none 을 명시하세요")

func (c Config) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipVerify} //nolint:gosec // opt-in for internal relays with private certificates
}

// helloName keeps the EHLO name to the sender domain, which relays that check
// the greeting are happier with than a container hostname.
func helloName(config Config) string {
	if index := strings.LastIndex(config.FromAddress, "@"); index >= 0 && index+1 < len(config.FromAddress) {
		return config.FromAddress[index+1:]
	}
	return "localhost"
}

// plainAuth is PLAIN (RFC 4616) with Relio's own plaintext rule. The standard
// library's PlainAuth hard-codes a localhost exception that ignores
// security=none on any other host, so it cannot express the documented policy.
type plainAuth struct {
	username, password string
	plaintext          bool
}

func (a plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !a.plaintext {
		return "", nil, errUnencrypted
	}
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		return nil, fmt.Errorf("예상하지 못한 PLAIN 요청: %s", fromServer)
	}
	return nil, nil
}

// loginAuth implements the LOGIN mechanism that several corporate relays use
// instead of PLAIN. The standard library only ships PLAIN and CRAM-MD5.
type loginAuth struct {
	username, password string
	plaintext          bool
}

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !a.plaintext {
		return "", nil, errUnencrypted
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(string(fromServer), ": ")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("알 수 없는 LOGIN 요청: %s", fromServer)
}
