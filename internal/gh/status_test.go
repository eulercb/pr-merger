package gh

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPRState_IconAndLabel(t *testing.T) {
	t.Parallel()

	// Every state must return a non-empty icon and label so the UI never
	// renders blank cells for new states we add in the future.
	states := []PRState{
		StateUnknown,
		StateDraft,
		StateChecksPending,
		StateChecksFailed,
		StateBehind,
		StateConflict,
		StateBlocked,
		StateClean,
	}
	for _, s := range states {
		assert.NotEmpty(t, s.Icon(), "icon for state %d should not be empty", s)
		assert.NotEmpty(t, s.Label(), "label for state %d should not be empty", s)
	}
}

func TestClassify_Nil(t *testing.T) {
	t.Parallel()
	assert.Equal(t, StateUnknown, Classify(nil))
}

func TestClassify_DraftBeatsEverything(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		IsDraft:          true,
		Mergeable:        "CONFLICTING",
		MergeStateStatus: "DIRTY",
		StatusCheckRoll: []StatusCheck{
			{TypeName: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
		},
	}
	assert.Equal(t, StateDraft, Classify(pr))
}

func TestClassify_ConflictBeatsChecks(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		Mergeable:        "CONFLICTING",
		MergeStateStatus: "BEHIND",
		StatusCheckRoll: []StatusCheck{
			{TypeName: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
		},
	}
	assert.Equal(t, StateConflict, Classify(pr))
}

func TestClassify_FailedChecksBeatBehind(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		MergeStateStatus: "BEHIND",
		Mergeable:        "MERGEABLE",
		StatusCheckRoll: []StatusCheck{
			{TypeName: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
		},
	}
	assert.Equal(t, StateChecksFailed, Classify(pr))
}

func TestClassify_Behind(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		MergeStateStatus: "BEHIND",
		Mergeable:        "MERGEABLE",
	}
	assert.Equal(t, StateBehind, Classify(pr))
}

func TestClassify_Clean(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		MergeStateStatus: "CLEAN",
		Mergeable:        "MERGEABLE",
	}
	assert.Equal(t, StateClean, Classify(pr))
}

func TestClassify_CleanWithPendingChecksIsPending(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		MergeStateStatus: "CLEAN",
		Mergeable:        "MERGEABLE",
		StatusCheckRoll: []StatusCheck{
			{TypeName: "CheckRun", Status: "IN_PROGRESS"},
		},
	}
	assert.Equal(t, StateChecksPending, Classify(pr))
}

func TestClassify_Blocked(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{MergeStateStatus: "BLOCKED"}
	assert.Equal(t, StateBlocked, Classify(pr))
}

func TestClassify_UnstableTreatedAsFailed(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{MergeStateStatus: "UNSTABLE"}
	assert.Equal(t, StateChecksFailed, Classify(pr))
}

func TestClassify_UnknownStatePending(t *testing.T) {
	t.Parallel()
	pr := &PullRequest{
		StatusCheckRoll: []StatusCheck{
			{TypeName: "CheckRun", Status: "QUEUED"},
		},
	}
	assert.Equal(t, StateChecksPending, Classify(pr))
}

func TestStatusCheck_CheckRunConclusions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		conclusion string
		passed     bool
		failed     bool
	}{
		{"SUCCESS", true, false},
		{"NEUTRAL", true, false},
		{"SKIPPED", true, false},
		{"FAILURE", false, true},
		{"CANCELLED", false, true},
		{"TIMED_OUT", false, true},
		{"STALE", false, true},
		{"STARTUP_FAILURE", false, true},
		{"ACTION_REQUIRED", false, true},
	}
	for _, tc := range cases {
		c := StatusCheck{TypeName: "CheckRun", Status: "COMPLETED", Conclusion: tc.conclusion}
		assert.Equal(t, tc.passed, c.Passed(), "Passed for %s", tc.conclusion)
		assert.Equal(t, tc.failed, c.Failed(), "Failed for %s", tc.conclusion)
		assert.False(t, c.Pending(), "Pending for %s should be false when completed", tc.conclusion)
	}
}

func TestStatusCheck_PendingCheckRun(t *testing.T) {
	t.Parallel()
	c := StatusCheck{TypeName: "CheckRun", Status: "IN_PROGRESS"}
	assert.True(t, c.Pending())
	assert.False(t, c.Passed())
	assert.False(t, c.Failed())
}

func TestStatusCheck_CommitStatus(t *testing.T) {
	t.Parallel()
	success := StatusCheck{State: "SUCCESS"}
	pending := StatusCheck{State: "PENDING"}
	failure := StatusCheck{State: "FAILURE"}
	errored := StatusCheck{State: "ERROR"}

	assert.True(t, success.Passed())
	assert.True(t, pending.Pending())
	assert.True(t, failure.Failed())
	assert.True(t, errored.Failed())
}

func TestStatusCheck_Display(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ci/build", StatusCheck{Name: "ci/build"}.Display())
	assert.Equal(t, "ctx", StatusCheck{Context: "ctx"}.Display())
	assert.Equal(t, "name", StatusCheck{Name: "name", Context: "ctx"}.Display(), "Name takes precedence")
}
