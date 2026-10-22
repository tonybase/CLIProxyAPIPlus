package authstatus

import (
	"strings"
	"testing"
	"time"
)

func TestIsLoginRequiredMessage(t *testing.T) {
	cases := map[string]bool{
		"token refresh failed (status 400): {\"error\":\"invalid_grant\"}": true,
		"token refresh failed (status 400): invalid_client":                true,
		"refresh failed: status 401":                                       true,
		"token refresh failed (status 500): upstream unavailable":          false,
		"refresh request failed: dial tcp: i/o timeout":                    false,
	}
	for msg, want := range cases {
		if got := IsLoginRequiredMessage(msg); got != want {
			t.Errorf("IsLoginRequiredMessage(%q) = %v, want %v", msg, got, want)
		}
	}
}

func TestMarkAndClearLoginRequired(t *testing.T) {
	metadata := map[string]any{"type": "kiro"}
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	if !MarkLoginRequired(metadata, "invalid_grant", now) {
		t.Fatal("first mark should change metadata")
	}
	if MarkLoginRequired(metadata, "invalid_grant", now.Add(time.Hour)) {
		t.Fatal("same reason should not change metadata")
	}
	required, reason, at := LoginRequired(metadata)
	if !required || reason != "invalid_grant" || at != "2026-10-22T09:00:00Z" {
		t.Fatalf("LoginRequired = %v %q %q", required, reason, at)
	}
	if !ClearLoginRequired(metadata) || ClearLoginRequired(metadata) {
		t.Fatal("ClearLoginRequired should report a change exactly once")
	}
	if len(metadata) != 1 {
		t.Fatalf("clear left keys behind: %#v", metadata)
	}
}

func TestMarkLoginRequiredTruncatesReason(t *testing.T) {
	metadata := map[string]any{}
	MarkLoginRequired(metadata, strings.Repeat("错", 1000), time.Now())
	_, reason, _ := LoginRequired(metadata)
	if got := len([]rune(reason)); got != maxReasonRunes {
		t.Fatalf("reason has %d runes, want %d", got, maxReasonRunes)
	}
}
