package prutil

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
)

func TestTruncate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty string passthrough", "", 10, ""},
		{"n zero returns empty", "hello", 0, ""},
		{"n one returns ellipsis only", "hello", 1, "…"},
		{"fits exactly", "hello", 5, "hello"},
		{"truncates ascii", "hello world", 6, "hello…"},
		{"multibyte runes not cut mid-sequence", "héllo wörld", 6, "héllo…"},
		{"cjk wide chars counted as runes", "日本語テスト", 4, "日本語…"},
		{"n larger than input keeps input", "short", 100, "short"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, Truncate(tc.in, tc.n))
		})
	}
}

func TestMatches_EmptyFilterMatchesAll(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{
		BaseRefName: "main",
		Author:      gh.Actor{Login: "alice"},
		Labels:      []gh.Label{{Name: "ready"}},
	}
	assert.True(t, Matches(pr, config.Filter{}))
}

func TestMatches_BaseBranch(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{BaseRefName: "main"}
	assert.True(t, Matches(pr, config.Filter{Base: "main"}))
	assert.False(t, Matches(pr, config.Filter{Base: "release"}))
}

func TestMatches_AuthorsCaseInsensitive(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{Author: gh.Actor{Login: "Alice"}}
	assert.True(t, Matches(pr, config.Filter{Authors: []string{"alice"}}))
	assert.True(t, Matches(pr, config.Filter{Authors: []string{"bob", "ALICE"}}))
	assert.False(t, Matches(pr, config.Filter{Authors: []string{"bob"}}))
}

func TestMatches_LabelsAllRequired(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{Labels: []gh.Label{{Name: "ready"}, {Name: "automerge"}}}
	assert.True(t, Matches(pr, config.Filter{Labels: []string{"ready"}}))
	assert.True(t, Matches(pr, config.Filter{Labels: []string{"ready", "AUTOMERGE"}}))
	// One missing label => no match.
	assert.False(t, Matches(pr, config.Filter{Labels: []string{"ready", "blocked"}}))
}

func TestMatchesAny(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{
		BaseRefName: "main",
		Author:      gh.Actor{Login: "alice"},
	}
	filters := []config.Filter{
		{Base: "release"},          // no match
		{Authors: []string{"bob"}}, // no match
		{Base: "main"},             // match
	}
	require.True(t, MatchesAny(pr, filters))

	// None of the filters match.
	filters = []config.Filter{
		{Base: "release"},
		{Authors: []string{"bob"}},
	}
	require.False(t, MatchesAny(pr, filters))

	// Empty filter list never matches (caller must supply at least one).
	assert.False(t, MatchesAny(pr, nil))
}

// TestMatches_AllCriteriaAnded verifies that within a single filter every
// set criterion must match.
func TestMatches_AllCriteriaAnded(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{
		BaseRefName: "main",
		Author:      gh.Actor{Login: "alice"},
		Labels:      []gh.Label{{Name: "ready"}},
	}

	// Full match.
	f := config.Filter{
		Base:    "main",
		Authors: []string{"alice"},
		Labels:  []string{"ready"},
	}
	assert.True(t, Matches(pr, f))

	// Base matches but author doesn't.
	f.Authors = []string{"bob"}
	assert.False(t, Matches(pr, f))
}

// Guard against accidental panic on weird inputs.
func TestTruncate_DoesNotPanic(t *testing.T) {
	t.Parallel()
	for i := -2; i < 20; i++ {
		_ = Truncate(strings.Repeat("a", 10), i)
	}
}
