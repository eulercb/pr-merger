package gh

import "time"

// PullRequest is the subset of PR fields we care about.
// Field names / tags match `gh pr list --json` and `gh pr view --json`.
type PullRequest struct {
	ID               string           `json:"id"`
	Number           int              `json:"number"`
	Title            string           `json:"title"`
	URL              string           `json:"url"`
	IsDraft          bool             `json:"isDraft"`
	Mergeable        string           `json:"mergeable"`        // MERGEABLE | CONFLICTING | UNKNOWN
	MergeStateStatus string           `json:"mergeStateStatus"` // CLEAN | BEHIND | BLOCKED | DIRTY | HAS_HOOKS | UNKNOWN | UNSTABLE
	BaseRefName      string           `json:"baseRefName"`
	HeadRefName      string           `json:"headRefName"`
	HeadRefOid       string           `json:"headRefOid"`
	Author           Actor            `json:"author"`
	Labels           []Label          `json:"labels"`
	AutoMergeRequest *AutoMergeReq    `json:"autoMergeRequest"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
	StatusCheckRoll  []StatusCheck    `json:"statusCheckRollup,omitempty"`
	ReviewDecision   string           `json:"reviewDecision,omitempty"`
}

type Actor struct {
	Login string `json:"login"`
}

type Label struct {
	Name string `json:"name"`
}

type AutoMergeReq struct {
	EnabledBy   Actor  `json:"enabledBy"`
	MergeMethod string `json:"mergeMethod"`
}

// StatusCheck is a unified view of a commit status or check-run as returned
// by `gh pr view --json statusCheckRollup`.
type StatusCheck struct {
	TypeName    string `json:"__typename"`
	Name        string `json:"name,omitempty"`
	Context     string `json:"context,omitempty"`
	Status      string `json:"status,omitempty"`      // QUEUED | IN_PROGRESS | COMPLETED
	Conclusion  string `json:"conclusion,omitempty"`  // SUCCESS | FAILURE | NEUTRAL | CANCELLED | TIMED_OUT | SKIPPED | STALE | STARTUP_FAILURE
	State       string `json:"state,omitempty"`       // for CommitStatus: SUCCESS | FAILURE | PENDING | ERROR
	DetailsURL  string `json:"detailsUrl,omitempty"`
	TargetURL   string `json:"targetUrl,omitempty"`
	IsRequired  bool   `json:"isRequired,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// Display returns a human label for the check.
func (s StatusCheck) Display() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Context
}

// Passed reports whether the check completed successfully or was skipped.
func (s StatusCheck) Passed() bool {
	if s.TypeName == "CheckRun" {
		return s.Status == "COMPLETED" && isSuccessConclusion(s.Conclusion)
	}
	// CommitStatus
	return s.State == "SUCCESS"
}

// Failed reports whether the check finished in a non-success state. Any
// completed CheckRun conclusion that isn't an explicit success
// (SUCCESS/NEUTRAL/SKIPPED) counts as failed — including less common
// conclusions like ACTION_REQUIRED and STALE — so summaries don't silently
// drop blocking checks when GitHub introduces new conclusions.
func (s StatusCheck) Failed() bool {
	if s.TypeName == "CheckRun" {
		return s.Status == "COMPLETED" && !isSuccessConclusion(s.Conclusion)
	}
	switch s.State {
	case "FAILURE", "ERROR":
		return true
	}
	return false
}

// Pending reports whether the check is still running.
func (s StatusCheck) Pending() bool {
	if s.TypeName == "CheckRun" {
		return s.Status != "COMPLETED"
	}
	return s.State == "PENDING"
}

func isSuccessConclusion(c string) bool {
	switch c {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return false
}
