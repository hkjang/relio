package crm

import (
	"encoding/base64"
	"strings"
	"testing"
)

// Every cursor this server hands out has to come back as the same offset.
func TestCursorRoundTripsThroughTheOffsetItEncodes(t *testing.T) {
	for _, offset := range []int{0, 1, 50, 200, 12345} {
		got, err := parseCursor(nextCursor(offset))
		if err != nil {
			t.Fatalf("cursor for offset %d was rejected: %v", offset, err)
		}
		if got != offset {
			t.Fatalf("cursor for offset %d decoded as %d", offset, got)
		}
	}
}

func TestAbsentCursorMeansTheFirstPage(t *testing.T) {
	for _, cursor := range []string{"", "   "} {
		got, err := parseCursor(cursor)
		if err != nil {
			t.Fatalf("a blank cursor must mean the first page, not an error: %v", err)
		}
		if got != 0 {
			t.Fatalf("a blank cursor decoded as offset %d", got)
		}
	}
}

// A cursor this server never issued used to decode as offset 0. The caller then
// received page one again together with a nextCursor promising more, which is a
// listing that never ends. It has to be reported instead.
func TestACursorThisServerDidNotIssueIsRefusedInsteadOfRestartingTheListing(t *testing.T) {
	encode := func(raw string) string { return base64.RawURLEncoding.EncodeToString([]byte(raw)) }
	for _, cursor := range []string{
		"not base64 at all!!",
		encode("offset"),
		encode("limit:50"),
		encode("offset:-1"),
		encode("offset:abc"),
		encode("offset:50:extra"),
		encode(":50"),
	} {
		got, err := parseCursor(cursor)
		if err == nil {
			t.Fatalf("cursor %q was accepted as offset %d instead of being refused", cursor, got)
		}
		if got != 0 {
			t.Fatalf("a refused cursor must not also report an offset, got %d", got)
		}
	}
}

// serviceError maps a service error onto an HTTP status by looking for words in
// its message, so this wording must not read as anything other than a bad
// request.
func TestTheInvalidCursorMessageIsNotMistakenForAnotherFailure(t *testing.T) {
	message := errInvalidCursor.Error()
	for _, trap := range []string{"not found", "no rows", "permission", "access denied", "designated approver", "another user", "already", "pending"} {
		if strings.Contains(message, trap) {
			t.Fatalf("the invalid cursor message contains %q, which serviceError reads as a different status: %q", trap, message)
		}
	}
	if !strings.Contains(message, "nextCursor") {
		t.Fatalf("the message must name the value the caller should send instead: %q", message)
	}
}
