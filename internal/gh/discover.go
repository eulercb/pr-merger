package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// CurrentUser returns the authenticated user's login via `gh api user`.
func (c *Client) CurrentUser(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// RecentContributedRepos returns repositories the viewer has contributed PRs
// to, newest activity first. Used by the wizard to suggest a sensible default.
func (c *Client) RecentContributedRepos(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	query := `query($first: Int!) {
  viewer {
    pullRequests(first: $first, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes { repository { nameWithOwner } updatedAt }
    }
  }
}`
	out, err := c.run(ctx,
		"api", "graphql",
		"-f", "query="+query,
		"-F", fmt.Sprintf("first=%d", limit*3),
	)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Viewer struct {
				PullRequests struct {
					Nodes []struct {
						Repository struct {
							NameWithOwner string `json:"nameWithOwner"`
						} `json:"repository"`
						UpdatedAt string `json:"updatedAt"`
					} `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("decode contributed repos: %w", err)
	}
	return dedupeRepos(resp.Data.Viewer.PullRequests.Nodes, limit), nil
}

func dedupeRepos(nodes []struct {
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	UpdatedAt string `json:"updatedAt"`
}, limit int) []string {
	// Preserve newest-first order by keeping the first occurrence.
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].UpdatedAt > nodes[j].UpdatedAt
	})
	seen := map[string]bool{}
	out := make([]string, 0, limit)
	for _, n := range nodes {
		name := n.Repository.NameWithOwner
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
		if len(out) >= limit {
			break
		}
	}
	return out
}
