package wizard

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/eulercb/pr-merger/internal/config"
)

type fakeDisc struct {
	login    string
	loginErr error
	repos    []string
	reposErr error
}

func (f fakeDisc) CurrentUser(context.Context) (string, error) {
	return f.login, f.loginErr
}

func (f fakeDisc) RecentContributedRepos(context.Context, int) ([]string, error) {
	return f.repos, f.reposErr
}

func TestBuildConfig_Valid(t *testing.T) {
	t.Parallel()
	cfg, err := BuildConfig(Answers{
		Repo:            "toggl/ic-tribe",
		FilterName:      "ic-tribe",
		AuthorRestrict:  true,
		Login:           "alice",
		PollIntervalStr: "45",
	})
	require.NoError(t, err)
	assert.Equal(t, 45, cfg.PollInterval)
	require.Len(t, cfg.Filters, 1)
	f := cfg.Filters[0]
	assert.Equal(t, "ic-tribe", f.Name)
	assert.Equal(t, "toggl/ic-tribe", f.Repo)
	assert.Equal(t, []string{"alice"}, f.Authors)
}

func TestBuildConfig_DefaultFilterName(t *testing.T) {
	t.Parallel()
	cfg, err := BuildConfig(Answers{Repo: "toggl/ic-tribe"})
	require.NoError(t, err)
	assert.Equal(t, "ic-tribe-automerge", cfg.Filters[0].Name)
}

func TestBuildConfig_RejectsBadRepo(t *testing.T) {
	t.Parallel()
	cases := []string{"", "no-slash", "a/b/c", "/b", "a/"}
	for _, tc := range cases {
		_, err := BuildConfig(Answers{Repo: tc})
		assert.Error(t, err, "should reject %q", tc)
	}
}

func TestBuildConfig_RejectsNonPositiveInterval(t *testing.T) {
	t.Parallel()
	_, err := BuildConfig(Answers{Repo: "o/r", PollIntervalStr: "0"})
	require.Error(t, err)

	_, err = BuildConfig(Answers{Repo: "o/r", PollIntervalStr: "-5"})
	require.Error(t, err)

	_, err = BuildConfig(Answers{Repo: "o/r", PollIntervalStr: "not-a-number"})
	require.Error(t, err)
}

func TestBuildConfig_DefaultInterval(t *testing.T) {
	t.Parallel()
	cfg, err := BuildConfig(Answers{Repo: "o/r"})
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.PollInterval)
}

func TestBuildConfig_AuthorRestrictRequiresLogin(t *testing.T) {
	t.Parallel()
	_, err := BuildConfig(Answers{Repo: "o/r", AuthorRestrict: true, Login: ""})
	require.Error(t, err)
}

func TestDefaultRepo(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "", DefaultRepo(Discovery{}))
	assert.Equal(t, "org/a",
		DefaultRepo(Discovery{Repos: []string{"org/a", "org/b"}}))
}

func TestDiscover_PropagatesErrors(t *testing.T) {
	t.Parallel()
	d := fakeDisc{loginErr: errors.New("unauth")}
	got := Discover(context.Background(), d)
	require.Error(t, got.Err)
	assert.Contains(t, got.Err.Error(), "current user")
}

func TestDiscover_RecentReposErrorKeepsLogin(t *testing.T) {
	t.Parallel()
	d := fakeDisc{login: "alice", reposErr: errors.New("graphql failed")}
	got := Discover(context.Background(), d)
	require.Error(t, got.Err)
	assert.Equal(t, "alice", got.Login)
}

func TestDiscover_Success(t *testing.T) {
	t.Parallel()
	d := fakeDisc{login: "alice", repos: []string{"org/a", "org/b"}}
	got := Discover(context.Background(), d)
	require.NoError(t, got.Err)
	assert.Equal(t, "alice", got.Login)
	assert.Equal(t, []string{"org/a", "org/b"}, got.Repos)
}

func TestPrepareHeadless_UsesDiscoveryDefaults(t *testing.T) {
	t.Parallel()
	d := fakeDisc{login: "alice", repos: []string{"org/a"}}
	result, err := PrepareHeadless(context.Background(), d, Answers{AuthorRestrict: true})
	require.NoError(t, err)
	require.NotNil(t, result.Config)
	assert.Equal(t, "org/a", result.Config.Filters[0].Repo)
	assert.Equal(t, []string{"alice"}, result.Config.Filters[0].Authors)
}

func TestPrepareHeadless_UserOverrideWins(t *testing.T) {
	t.Parallel()
	d := fakeDisc{login: "alice", repos: []string{"org/a"}}
	result, err := PrepareHeadless(context.Background(), d, Answers{Repo: "other/repo"})
	require.NoError(t, err)
	assert.Equal(t, "other/repo", result.Config.Filters[0].Repo)
}

func TestPrepareHeadless_DiscoveryErrorStillBuildsIfRepoProvided(t *testing.T) {
	t.Parallel()
	d := fakeDisc{loginErr: errors.New("offline")}
	result, err := PrepareHeadless(context.Background(), d, Answers{Repo: "o/r"})
	require.NoError(t, err)
	require.NotNil(t, result.Config)
	assert.Equal(t, "o/r", result.Config.Filters[0].Repo)
	require.Error(t, result.Discovery.Err)
}

// TestModel_AbortClearsCfg pins the Copilot-flagged contract: if the user
// presses Esc after the wizard already populated Cfg at stepInterval, the
// caller must not persist that config.
func TestModel_AbortClearsCfg(t *testing.T) {
	t.Parallel()
	m := NewModel(Discovery{})
	// Simulate a user who reached stepConfirm — Cfg is populated.
	m.Cfg = &config.Config{PollInterval: 30}
	m.step = stepConfirm

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	final, ok := updated.(Model)
	require.True(t, ok)
	assert.True(t, final.quit, "esc must set quit")
	assert.Nil(t, final.Cfg, "Cfg must be cleared so caller doesn't persist a declined config")
}

func TestValidateRepo(t *testing.T) {
	t.Parallel()

	ok := []string{"toggl/ic-tribe", "o/r", "with-dash/repo.name"}
	for _, s := range ok {
		assert.NoError(t, ValidateRepo(s), "expected %q to validate", s)
	}

	bad := []string{"", "noslash", "a/b/c", "/b", "a/", "  "}
	for _, s := range bad {
		assert.Error(t, ValidateRepo(s), "expected %q to fail validation", s)
	}
}

func TestDefaultFilterName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ic-tribe-automerge", defaultFilterName("toggl/ic-tribe"))
	assert.Equal(t, "auto-automerge", defaultFilterName("a/auto"))
	assert.Equal(t, "auto-automerge", defaultFilterName("auto"))
}
