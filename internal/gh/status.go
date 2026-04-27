package gh

// PRState is a coarse UI-friendly classification of a PR's current state.
// It compresses GitHub's `mergeStateStatus` plus check-roll signals into the
// small set of icons the TUI renders per PR.
type PRState int

const (
	// StateUnknown means we have no signal yet (e.g. not fetched).
	StateUnknown PRState = iota
	// StateDraft — the PR is marked as a draft.
	StateDraft
	// StateChecksPending — at least one required check is still running.
	StateChecksPending
	// StateChecksFailed — a required check finished in failure / error.
	StateChecksFailed
	// StateBehind — head branch is out of date; eligible for rebase.
	StateBehind
	// StateConflict — PR has merge conflicts (mergeStateStatus = DIRTY).
	StateConflict
	// StateBlocked — awaiting reviews / required statuses.
	StateBlocked
	// StateClean — all checks passed, ready for GitHub to auto-merge.
	StateClean
)

// Icon returns a single-rune glyph for the state. Chosen for terminals with
// basic Unicode support; no emoji dependency.
func (s PRState) Icon() string {
	switch s {
	case StateDraft:
		return "◌"
	case StateChecksPending:
		return "●"
	case StateChecksFailed:
		return "✗"
	case StateBehind:
		return "↻"
	case StateConflict:
		return "⚠"
	case StateBlocked:
		return "▲"
	case StateClean:
		return "✓"
	default:
		return "·"
	}
}

// Label returns a short human-readable label for the state.
func (s PRState) Label() string {
	switch s {
	case StateDraft:
		return "draft"
	case StateChecksPending:
		return "checks running"
	case StateChecksFailed:
		return "checks failed"
	case StateBehind:
		return "behind"
	case StateConflict:
		return "conflict"
	case StateBlocked:
		return "blocked"
	case StateClean:
		return "clean"
	default:
		return "unknown"
	}
}

// Classify reduces a full PullRequest to a single PRState. Precedence
// matters: draft beats everything else, conflicts beat behind, failed checks
// beat pending, etc. Tests pin the exact precedence.
func Classify(pr *PullRequest) PRState {
	if pr == nil {
		return StateUnknown
	}
	if pr.IsDraft {
		return StateDraft
	}
	if pr.Mergeable == "CONFLICTING" || pr.MergeStateStatus == "DIRTY" {
		return StateConflict
	}

	var pending, failed int
	for _, c := range pr.StatusCheckRoll {
		switch {
		case c.Failed():
			failed++
		case c.Pending():
			pending++
		}
	}
	if failed > 0 {
		return StateChecksFailed
	}

	switch pr.MergeStateStatus {
	case "BEHIND":
		return StateBehind
	case "CLEAN", "HAS_HOOKS":
		if pending > 0 {
			return StateChecksPending
		}
		return StateClean
	case "BLOCKED":
		if pending > 0 {
			return StateChecksPending
		}
		return StateBlocked
	case "UNSTABLE":
		if pending > 0 {
			return StateChecksPending
		}
		return StateChecksFailed
	}
	if pending > 0 {
		return StateChecksPending
	}
	return StateUnknown
}
