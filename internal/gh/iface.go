package gh

import "context"

// PRClient is the subset of Client used by the watcher and TUI. Having an
// interface here lets tests substitute a fake without shelling out to gh.
type PRClient interface {
	ListOpenPRs(ctx context.Context, repo string) ([]PullRequest, error)
	GetPR(ctx context.Context, repo string, number int) (*PullRequest, error)
	UpdateBranchRebase(ctx context.Context, prNodeID string) error
}

// Ensure *Client satisfies PRClient at compile time.
var _ PRClient = (*Client)(nil)
