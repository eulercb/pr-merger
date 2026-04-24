// Package prutil holds helpers shared between the watcher loop and the
// status command: filter-matching against a [config.Filter] and rune-aware
// title truncation for log/table output.
package prutil

import (
	"strings"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
)

// MatchesAny reports whether pr satisfies at least one of the provided filters.
func MatchesAny(pr gh.PullRequest, filters []config.Filter) bool {
	for _, f := range filters {
		if Matches(pr, f) {
			return true
		}
	}
	return false
}

// Matches reports whether pr satisfies every non-empty criterion of f.
// An empty criterion (no Authors, no Labels, no Base) is treated as "any".
func Matches(pr gh.PullRequest, f config.Filter) bool {
	if f.Base != "" && pr.BaseRefName != f.Base {
		return false
	}
	if len(f.Authors) > 0 && !containsFold(f.Authors, pr.Author.Login) {
		return false
	}
	if len(f.Labels) > 0 {
		labels := make([]string, 0, len(pr.Labels))
		for _, l := range pr.Labels {
			labels = append(labels, l.Name)
		}
		for _, want := range f.Labels {
			if !containsFold(labels, want) {
				return false
			}
		}
	}
	return true
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.EqualFold(h, needle) {
			return true
		}
	}
	return false
}

// Truncate shortens s to at most n runes, appending a single-rune ellipsis
// when truncation happens. Rune-aware so multi-byte characters (common in
// PR titles) aren't cut mid-sequence.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}
