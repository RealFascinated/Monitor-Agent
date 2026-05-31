package update

import (
	"testing"
	"time"
)

func TestLatestAgentRelease(t *testing.T) {
	t.Parallel()

	published := func(ts string) *time.Time {
		t.Helper()
		parsed, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			t.Fatalf("parse time: %v", err)
		}
		return &parsed
	}

	releases := []release{
		{TagName: "agent/v2.0.5", PublishedAt: published("2026-01-01T00:00:00Z")},
		{TagName: "agent/v2.0.10", PublishedAt: published("2026-01-02T00:00:00Z")},
		{TagName: "agent/v2.0.7", PublishedAt: published("2026-05-01T00:00:00Z")},
		{TagName: "v1.0.0", PublishedAt: published("2026-06-01T00:00:00Z")},
		{TagName: "agent/v2.1.0", Draft: true, PublishedAt: published("2026-06-02T00:00:00Z")},
		{TagName: "agent/v2.0.0-beta", Prerelease: true, PublishedAt: published("2026-06-03T00:00:00Z")},
	}

	got, err := latestAgentRelease(releases)
	if err != nil {
		t.Fatalf("latestAgentRelease() error = %v", err)
	}
	if got.TagName != "agent/v2.0.10" {
		t.Fatalf("latest tag = %q, want agent/v2.0.10", got.TagName)
	}
}

func TestLatestAgentReleaseTieBreaksByPublishDate(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	releases := []release{
		{TagName: "agent/v2.0.7", PublishedAt: &older},
		{TagName: "agent/v2.0.7", PublishedAt: &newer},
	}

	got, err := latestAgentRelease(releases)
	if err != nil {
		t.Fatalf("latestAgentRelease() error = %v", err)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(newer) {
		t.Fatalf("latest publish date = %v, want %v", got.PublishedAt, newer)
	}
}
