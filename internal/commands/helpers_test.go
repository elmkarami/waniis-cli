package commands

import (
	"testing"
	"time"
)

func TestParseRepo(t *testing.T) {
	ok := map[string]string{
		"acme/api":                           "acme/api",
		"https://github.com/acme/api":        "acme/api",
		"https://github.com/acme/api.git":    "acme/api",
		"git@github.com:acme/my.repo.git":    "acme/my.repo",
		"ssh://git@github.com/acme/api.git/": "acme/api",
	}
	for in, want := range ok {
		if got, good := ParseRepo(in); !good || got != want {
			t.Errorf("%s: %q %v", in, got, good)
		}
	}
	for _, bad := range []string{"", "acme", "a/b/c", "https://gitlab.com/a/b", "acme/api.git", "a b/c"} {
		if _, good := ParseRepo(bad); good {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestAgo(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{30 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago", 90 * 24 * time.Hour: "2026-08-22"} {
		if got := ago(now, now.Add(-d)); got != want {
			t.Errorf("%v: %q want %q", d, got, want)
		}
	}
	if ago(now, time.Time{}) != "-" {
		t.Error("zero time")
	}
}

func TestInterpretationLine(t *testing.T) {
	if got := interpretationLine(nil); got != "Searching: all sessions" {
		t.Error(got)
	}
}
