package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hkjang/relio/internal/crm"
	"github.com/hkjang/relio/internal/platform/httpx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// serviceErrorResult is what a REST client actually receives: the status line
// and the decoded envelope, plus the raw body so a test can assert that a
// schema name appears nowhere in it.
type serviceErrorResult struct {
	status  int
	code    string
	message string
	body    string
	logs    string
}

// runServiceError drives the production handler — the real (*Server).serviceError
// with a real ResponseWriter and Request — rather than a stand-in, so what the
// test reads is what a client reads.
func runServiceError(t *testing.T, err error) serviceErrorResult {
	t.Helper()
	logs := &bytes.Buffer{}
	s := &Server{Log: slog.New(slog.NewTextHandler(logs, nil))}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil)
	request = request.WithContext(httpx.WithRequestID(request.Context(), "req-42"))
	s.serviceError(recorder, request, err)
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"requestId"`
		} `json:"error"`
	}
	body := recorder.Body.String()
	if decodeErr := json.Unmarshal(recorder.Body.Bytes(), &envelope); decodeErr != nil {
		t.Fatalf("response is not the error envelope: %v (%s)", decodeErr, body)
	}
	if envelope.Error.RequestID != "req-42" {
		t.Fatalf("envelope lost the request id: %s", body)
	}
	return serviceErrorResult{status: recorder.Code, code: envelope.Error.Code, message: envelope.Error.Message, body: body, logs: logs.String()}
}

// TestServiceErrorClassifiesSQLStateLikeMCPToolErrors pins the REST verdict for
// a PostgreSQL error to the vocabulary internal/mcp/results.go sanitizeToolError
// already uses, so the two readers of one error no longer disagree. Every case
// carries a driver message with a relation, column or constraint name in it.
func TestServiceErrorClassifiesSQLStateLikeMCPToolErrors(t *testing.T) {
	for _, testCase := range []struct {
		name, sqlstate, driver string
		wantStatus             int
		wantCode, wantMessage  string
	}{
		{"invalid uuid", "22P02", `invalid input syntax for type uuid: "abc"`, http.StatusBadRequest, "invalid_request", "입력 값의 형식이 올바르지 않습니다. ID는 목록·검색 도구가 돌려준 UUID를 그대로 사용하세요."},
		{"invalid date", "22007", `invalid input syntax for type date: "2026-13-01"`, http.StatusBadRequest, "invalid_request", "날짜 형식이 올바르지 않습니다. YYYY-MM-DD 형식을 사용하세요."},
		{"date out of range", "22008", "date/time field value out of range", http.StatusBadRequest, "invalid_request", "날짜 형식이 올바르지 않습니다. YYYY-MM-DD 형식을 사용하세요."},
		{"numeric overflow", "22003", "numeric field overflow in column amount", http.StatusBadRequest, "invalid_request", "숫자가 허용 범위를 벗어났습니다."},
		{"not null violation", "23502", `null value in column "name" of relation "contacts" violates not-null constraint`, http.StatusBadRequest, "invalid_request", "필수 값이 비었거나 허용되지 않는 값입니다."},
		{"check violation", "23514", `new row for relation "contacts" violates check constraint "contacts_stage_check"`, http.StatusBadRequest, "invalid_request", "필수 값이 비었거나 허용되지 않는 값입니다."},
		{"unique violation", "23505", `duplicate key value violates unique constraint "contacts_email_key"`, http.StatusConflict, "conflict", "같은 값이 이미 등록되어 있습니다. 기존 데이터를 조회해 수정하세요."},
		{"foreign key violation", "23503", `update or delete on table "customers" violates foreign key constraint on table "contacts"`, http.StatusConflict, "conflict", "연결 대상이 없거나 다른 데이터가 참조하고 있어 처리할 수 없습니다."},
		{"serialization failure", "40001", "could not serialize access due to concurrent update", http.StatusConflict, "conflict", "동시에 처리된 다른 요청과 충돌했습니다. 잠시 후 다시 시도하세요."},
		{"deadlock", "40P01", "deadlock detected on relation contacts", http.StatusConflict, "conflict", "동시에 처리된 다른 요청과 충돌했습니다. 잠시 후 다시 시도하세요."},
		{"undefined table", "42P01", `relation "contacts" does not exist`, http.StatusInternalServerError, "internal_error", "서버 오류가 발생했습니다."},
		{"insufficient privilege", "42501", "permission denied for table contacts", http.StatusInternalServerError, "internal_error", "서버 오류가 발생했습니다."},
		{"too many connections", "53300", "sorry, too many clients already", http.StatusInternalServerError, "internal_error", "서버 오류가 발생했습니다."},
		{"internal error", "XX000", "internal error on relation contacts", http.StatusInternalServerError, "internal_error", "서버 오류가 발생했습니다."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pgErr := &pgconn.PgError{Code: testCase.sqlstate, Severity: "ERROR", Message: testCase.driver}
			result := runServiceError(t, fmt.Errorf("담당자를 저장할 수 없습니다: %w", pgErr))
			if result.status != testCase.wantStatus || result.code != testCase.wantCode {
				t.Fatalf("SQLSTATE %s: got %d %s, want %d %s", testCase.sqlstate, result.status, result.code, testCase.wantStatus, testCase.wantCode)
			}
			if result.message != testCase.wantMessage {
				t.Fatalf("SQLSTATE %s message: got %q, want %q", testCase.sqlstate, result.message, testCase.wantMessage)
			}
			if strings.Contains(result.body, testCase.driver) || strings.Contains(result.body, "SQLSTATE") || strings.Contains(result.body, testCase.sqlstate) {
				t.Fatalf("SQLSTATE %s leaked the driver message: %s", testCase.sqlstate, result.body)
			}
		})
	}
}

// TestServiceErrorHidesSchemaNamesAndLogsTheOriginal is the pair of errors that
// motivated the branch: today one answers 400 with the relation name and the
// other answers 403 with the table name. The operator keeps both, in the log.
func TestServiceErrorHidesSchemaNamesAndLogsTheOriginal(t *testing.T) {
	for _, testCase := range []struct{ name, sqlstate, driver, logged string }{
		// logged is a quote-free fragment of driver, because slog's text handler
		// escapes the quotes the driver puts around a relation name.
		{"undefined table", "42P01", `relation "contacts" does not exist`, "does not exist"},
		{"insufficient privilege", "42501", "permission denied for table contacts", "permission denied for table contacts"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pgErr := &pgconn.PgError{Code: testCase.sqlstate, Severity: "ERROR", Message: testCase.driver, TableName: "contacts", ConstraintName: "contacts_pkey"}
			result := runServiceError(t, fmt.Errorf("담당자 목록을 읽을 수 없습니다: %w", pgErr))
			if result.status != http.StatusInternalServerError {
				t.Fatalf("a server defect must be a 5xx, got %d (%s)", result.status, result.body)
			}
			for _, secret := range []string{"SQLSTATE", testCase.sqlstate, "contacts", "contacts_pkey", "relation", "permission denied"} {
				if strings.Contains(result.body, secret) {
					t.Fatalf("response body still carries %q: %s", secret, result.body)
				}
			}
			for _, kept := range []string{testCase.sqlstate, "req-42", testCase.logged, "contacts"} {
				if !strings.Contains(result.logs, kept) {
					t.Fatalf("log lost %q: %s", kept, result.logs)
				}
			}
		})
	}
}

// TestServiceErrorKeepsExistingClassification fixes the verdicts that already
// worked, because the SQLSTATE branch sits in front of the substring branches.
// It also fixes the reason they are unaffected: none of these errors carries a
// *pgconn.PgError in its chain.
func TestServiceErrorKeepsExistingClassification(t *testing.T) {
	for _, testCase := range []struct {
		name                  string
		err                   error
		wantStatus            int
		wantCode, wantMessage string
	}{
		{"no rows sentinel", fmt.Errorf("고객을 읽을 수 없습니다: %w", pgx.ErrNoRows), http.StatusNotFound, "not_found", "요청한 데이터를 찾을 수 없거나 접근 권한이 없습니다."},
		{"no rows message", errors.New("no rows in result set"), http.StatusNotFound, "not_found", "요청한 데이터를 찾을 수 없거나 접근 권한이 없습니다."},
		{"not found sentence", errors.New("pipeline not found"), http.StatusNotFound, "not_found", "pipeline not found"},
		{"customer code taken", fmt.Errorf("%w: 고객 코드 A1는 이미 다른 고객에 등록되어 있습니다", crm.ErrCustomerCodeTaken), http.StatusConflict, "conflict", "customer code is already registered: 고객 코드 A1는 이미 다른 고객에 등록되어 있습니다"},
		{"permission", errors.New("no permission for this record"), http.StatusForbidden, "forbidden", "no permission for this record"},
		{"access denied", errors.New("access denied"), http.StatusForbidden, "forbidden", "access denied"},
		{"designated approver", errors.New("only the designated approver may decide"), http.StatusForbidden, "forbidden", "only the designated approver may decide"},
		{"another user", errors.New("another user changed this record"), http.StatusConflict, "conflict", "another user changed this record"},
		{"pending", errors.New("a pending approval blocks this change"), http.StatusConflict, "conflict", "a pending approval blocks this change"},
		{"delete guarded", errors.New("Role는 사용자에서 사용 중이어서 삭제할 수 없습니다. 먼저 해당 데이터의 연결을 변경하세요"), http.StatusBadRequest, "invalid_request", "Role는 사용자에서 사용 중이어서 삭제할 수 없습니다. 먼저 해당 데이터의 연결을 변경하세요"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var pgErr *pgconn.PgError
			if errors.As(testCase.err, &pgErr) {
				t.Fatalf("this error must not carry a PgError, or the SQLSTATE branch would claim it: %v", testCase.err)
			}
			result := runServiceError(t, testCase.err)
			if result.status != testCase.wantStatus || result.code != testCase.wantCode || result.message != testCase.wantMessage {
				t.Fatalf("got %d %s %q, want %d %s %q", result.status, result.code, result.message, testCase.wantStatus, testCase.wantCode, testCase.wantMessage)
			}
		})
	}
}

// TestServiceErrorFoldsUnwrappedSQLStateText covers the errors that reach the
// handler as text — a service that formatted the driver message with %v rather
// than %w still must not put "SQLSTATE 42P01" in a response body.
func TestServiceErrorFoldsUnwrappedSQLStateText(t *testing.T) {
	result := runServiceError(t, errors.New(`ERROR: relation "contacts" does not exist (SQLSTATE 42P01)`))
	if result.status != http.StatusInternalServerError || result.code != "internal_error" {
		t.Fatalf("got %d %s, want 500 internal_error (%s)", result.status, result.code, result.body)
	}
	for _, secret := range []string{"SQLSTATE", "42P01", "contacts"} {
		if strings.Contains(result.body, secret) {
			t.Fatalf("response body still carries %q: %s", secret, result.body)
		}
	}
}
