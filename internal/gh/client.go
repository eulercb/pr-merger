package gh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Client invokes the `gh` CLI to talk to GitHub.
type Client struct {
	// Bin is the path to the gh binary (defaults to "gh").
	Bin string
}

func New() *Client { return &Client{Bin: "gh"} }

func (c *Client) bin() string {
	if c.Bin == "" {
		return "gh"
	}
	return c.Bin
}

// run executes `gh <args...>` and returns stdout.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// prListFields are the JSON fields to request when listing PRs.
// Keep this consistent with the fields consumed on the PullRequest struct.
var prListFields = strings.Join([]string{
	"number", "title", "url", "isDraft", "mergeable", "mergeStateStatus",
	"baseRefName", "headRefName", "headRefOid", "author", "labels",
	"autoMergeRequest", "createdAt", "updatedAt", "reviewDecision",
}, ",")

// prViewFields include per-PR details plus the status-check rollup.
var prViewFields = prListFields + ",id,statusCheckRollup"

// ListOpenPRs returns all open PRs in repo (owner/name). Auto-merge state
// is available on the result; callers filter by it.
func (c *Client) ListOpenPRs(ctx context.Context, repo string) ([]PullRequest, error) {
	out, err := c.run(ctx,
		"pr", "list",
		"--repo", repo,
		"--state", "open",
		"--limit", "200",
		"--json", prListFields,
	)
	if err != nil {
		return nil, err
	}
	var prs []PullRequest
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("decode pr list: %w", err)
	}
	return prs, nil
}

// GetPR fetches a single PR with status-check rollup included.
func (c *Client) GetPR(ctx context.Context, repo string, number int) (*PullRequest, error) {
	out, err := c.run(ctx,
		"pr", "view", fmt.Sprint(number),
		"--repo", repo,
		"--json", prViewFields,
	)
	if err != nil {
		return nil, err
	}
	var pr PullRequest
	if err := json.Unmarshal(out, &pr); err != nil {
		return nil, fmt.Errorf("decode pr view: %w", err)
	}
	return &pr, nil
}

// UpdateBranchRebase rebases the PR head branch onto its base, the same
// action as clicking "Update with rebase" in the GitHub UI. It uses the
// GraphQL updatePullRequestBranch mutation with updateMethod: REBASE, which
// requires the PR node ID.
func (c *Client) UpdateBranchRebase(ctx context.Context, prNodeID string) error {
	mutation := `mutation($id: ID!) {
  updatePullRequestBranch(input: { pullRequestId: $id, updateMethod: REBASE }) {
    pullRequest { number headRefOid }
  }
}`
	_, err := c.run(ctx,
		"api", "graphql",
		"-f", "query="+mutation,
		"-F", "id="+prNodeID,
	)
	return err
}

// EnableAutoMerge turns on auto-merge for a PR with the given merge method.
// Method is one of: merge, squash, rebase.
func (c *Client) EnableAutoMerge(ctx context.Context, repo string, number int, method string) error {
	flag := "--merge"
	switch strings.ToLower(method) {
	case "squash":
		flag = "--squash"
	case "rebase":
		flag = "--rebase"
	}
	_, err := c.run(ctx,
		"pr", "merge", fmt.Sprint(number),
		"--repo", repo,
		"--auto",
		flag,
	)
	return err
}
