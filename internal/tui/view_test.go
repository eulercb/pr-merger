package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/eulercb/pr-merger/internal/gh"
)

func TestPRNumberColumnWidth(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		prs  []gh.PullRequest
		want int
	}{
		{"empty queue has min width", nil, 6},
		{"two-digit numbers use min width", mkPRs(42), 6},
		{"four-digit numbers still min width", mkPRs(9999), 6},
		{"five-digit numbers expand", mkPRs(12345), 7},
		{"six-digit numbers expand further", mkPRs(123456), 8},
		{"max across queue wins", mkPRs(3, 12345, 7), 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, prNumberColumnWidth(tc.prs))
		})
	}
}

func mkPRs(nums ...int) []gh.PullRequest {
	out := make([]gh.PullRequest, len(nums))
	for i, n := range nums {
		out[i] = gh.PullRequest{Number: n}
	}
	return out
}

// TestRenderPRLine_FiveDigitNumber guards against the regression Copilot
// flagged: with 5+ digit PR numbers, the old hard-coded "#%-4d" format
// shifted the title column and broke alignment.
func TestRenderPRLine_FiveDigitNumber(t *testing.T) {
	t.Parallel()
	pr := gh.PullRequest{Number: 12345, Title: "x"}
	line := renderPRLine(pr, 7, 40, false, false)
	assert.Contains(t, line, "#12345", "number must render at its natural width")
}
