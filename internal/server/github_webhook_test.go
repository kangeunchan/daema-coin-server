package server

import (
	"testing"
	"time"
)

func TestGitHubWebhookCommitTimesUseServerReceivedAt(t *testing.T) {
	receivedAt := time.Date(2026, time.June, 29, 4, 0, 0, 0, time.UTC)
	payloadTimestamp := time.Date(2000, time.January, 1, 12, 0, 0, 0, time.FixedZone("payload", 9*60*60))

	occurredAt, commitTimestamp := githubWebhookCommitTimes(receivedAt, payloadTimestamp)
	if !occurredAt.Equal(receivedAt) {
		t.Fatalf("occurredAt = %s, want server receivedAt %s", occurredAt, receivedAt)
	}
	if commitTimestamp == nil || !commitTimestamp.Equal(payloadTimestamp) {
		t.Fatalf("commitTimestamp = %v, want payload timestamp %s", commitTimestamp, payloadTimestamp)
	}
}

func TestGitHubWebhookCommitTimesAllowMissingPayloadTimestamp(t *testing.T) {
	receivedAt := time.Date(2026, time.June, 29, 4, 0, 0, 0, time.UTC)

	occurredAt, commitTimestamp := githubWebhookCommitTimes(receivedAt, time.Time{})
	if !occurredAt.Equal(receivedAt) {
		t.Fatalf("occurredAt = %s, want server receivedAt %s", occurredAt, receivedAt)
	}
	if commitTimestamp != nil {
		t.Fatalf("commitTimestamp = %v, want nil", commitTimestamp)
	}
}
