package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// structuredObject shapes a tool's return value for structuredContent, which
// the specification defines as a JSON object. Twenty-four list tools returned a
// bare array there, and every client built on the official SDK validates the
// field: the call failed with "expected record" before the model saw a byte.
// A list becomes {items, count}, matching the paged tools, and a nil list
// becomes an empty one rather than null.
func structuredObject(v any) (map[string]any, []byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, fmt.Errorf("도구 결과를 JSON으로 만들 수 없습니다: %w", err)
	}
	trimmed := bytes.TrimSpace(raw)
	switch {
	case len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")):
		// A nil slice from an empty query. Nothing returns a nil single record
		// without an error, so an empty list is the honest reading.
		return map[string]any{"items": []any{}, "count": 0}, []byte("[]"), nil
	case trimmed[0] == '{':
		out := map[string]any{}
		if err := json.Unmarshal(trimmed, &out); err != nil {
			return nil, nil, err
		}
		return out, raw, nil
	case trimmed[0] == '[':
		items := []any{}
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, nil, err
		}
		return map[string]any{"items": items, "count": len(items)}, raw, nil
	default:
		var value any
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, nil, err
		}
		return map[string]any{"value": value}, raw, nil
	}
}

// sanitizeToolError turns an error into a sentence that is safe and useful to
// show a model. Database errors used to reach the client verbatim — "ERROR:
// invalid input syntax for type uuid (SQLSTATE 22P02)" — which exposes schema
// detail and gives the model nothing it can act on. The original is logged
// with the request id so an operator can still find it.
//
// The same SQLSTATE table is in internal/server/server.go pgErrorVerdict, which
// is how the REST path reads the same error. The sentences below are shared
// with it word for word; change one and change the other. The two stay separate
// because the return contracts differ — a string here, status+code+message
// there — and internal/server imports this package, not the other way round.
func sanitizeToolError(err error, requestID string) string {
	message := err.Error()
	var arg *argumentError
	if errors.As(err, &arg) {
		return arg.message
	}
	if errors.Is(err, pgx.ErrNoRows) || message == "no rows in result set" || strings.HasSuffix(message, " not found") {
		return "요청한 데이터를 찾을 수 없거나 접근 권한이 없습니다."
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) || strings.Contains(message, "SQLSTATE") {
		code := ""
		if pgErr != nil {
			code = pgErr.Code
		}
		slog.Warn("mcp tool database error", "error", message, "sqlstate", code, "requestId", requestID)
		switch code {
		case "22P02":
			return "입력 값의 형식이 올바르지 않습니다. ID는 목록·검색 도구가 돌려준 UUID를 그대로 사용하세요."
		case "22007", "22008":
			return "날짜 형식이 올바르지 않습니다. YYYY-MM-DD 형식을 사용하세요."
		case "22003":
			return "숫자가 허용 범위를 벗어났습니다."
		case "23505":
			return "같은 값이 이미 등록되어 있습니다. 기존 데이터를 조회해 수정하세요."
		case "23503":
			return "연결 대상이 없거나 다른 데이터가 참조하고 있어 처리할 수 없습니다."
		case "23502", "23514":
			return "필수 값이 비었거나 허용되지 않는 값입니다."
		case "40001", "40P01":
			return "동시에 처리된 다른 요청과 충돌했습니다. 잠시 후 다시 시도하세요."
		}
		return "데이터 처리 중 오류가 발생했습니다. 요청 ID: " + requestID
	}
	return message
}
