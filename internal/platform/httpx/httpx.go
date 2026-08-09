package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func ErrorJSON(w http.ResponseWriter, r *http.Request, status int, code, message string, details map[string]any) {
	JSON(w, status, errorEnvelope{Error: Error{Code: code, Message: message, Details: details, RequestID: RequestID(r.Context())}})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			ErrorJSON(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "요청 본문이 너무 큽니다.", nil)
			return false
		}
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_json", "JSON 요청 형식이 올바르지 않습니다.", map[string]any{"cause": err.Error()})
		return false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		ErrorJSON(w, r, http.StatusBadRequest, "invalid_json", "요청에는 JSON 객체 하나만 허용됩니다.", nil)
		return false
	}
	return true
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func Bearer(r *http.Request) string {
	v := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(v) < 8 || !strings.EqualFold(v[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(v[7:])
}

func IntQuery(r *http.Request, key string, fallback, min, max int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	var n int
	if _, err := fmt.Sscan(v, &n); err != nil || n < min || n > max {
		return fallback
	}
	return n
}
