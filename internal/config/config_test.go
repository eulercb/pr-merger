package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withTempConfig sets PR_MERGER_CONFIG to a path inside t.TempDir() for the
// duration of the test, restoring the previous value afterwards.
func withTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	prev, had := os.LookupEnv("PR_MERGER_CONFIG")
	t.Setenv("PR_MERGER_CONFIG", path)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("PR_MERGER_CONFIG", prev)
		} else {
			_ = os.Unsetenv("PR_MERGER_CONFIG")
		}
	})
	return path
}

func TestLoad_MissingFileReturnsDefault(t *testing.T) {
	path := withTempConfig(t)
	cfg, got, err := Load()
	require.NoError(t, err)
	assert.Equal(t, path, got)
	assert.Equal(t, 30, cfg.PollInterval, "default poll interval should be 30s")
	assert.Empty(t, cfg.Filters)
}

func TestSaveAndLoadRoundtrip(t *testing.T) {
	path := withTempConfig(t)
	in := &Config{
		PollInterval: 45,
		Filters: []Filter{
			{Name: "b-filter", Repo: "o/b"},
			{Name: "a-filter", Repo: "o/a", Authors: []string{"alice"}},
		},
	}
	got, err := Save(in)
	require.NoError(t, err)
	assert.Equal(t, path, got)

	out, _, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 45, out.PollInterval)
	require.Len(t, out.Filters, 2)
	// Save sorts filters by name.
	assert.Equal(t, "a-filter", out.Filters[0].Name)
	assert.Equal(t, "b-filter", out.Filters[1].Name)
}

func TestLoad_PollIntervalFloor(t *testing.T) {
	path := withTempConfig(t)
	// Hand-written YAML with a non-positive poll interval.
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("poll_interval: 0\nfilters: []\n"), 0o644))

	cfg, _, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 30, cfg.PollInterval)
}

func TestLoad_MalformedYAMLReturnsError(t *testing.T) {
	path := withTempConfig(t)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("not: [valid"), 0o644))
	_, _, err := Load()
	require.Error(t, err)
}

func TestFindFilter_FoundAndMissing(t *testing.T) {
	cfg := &Config{Filters: []Filter{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}}

	i, f := cfg.FindFilter("b")
	require.NotNil(t, f)
	assert.Equal(t, 1, i)
	assert.Equal(t, "b", f.Name)

	i, f = cfg.FindFilter("missing")
	assert.Equal(t, -1, i)
	assert.Nil(t, f)
}

func TestRemoveFilter(t *testing.T) {
	cfg := &Config{Filters: []Filter{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}}
	assert.True(t, cfg.RemoveFilter("b"))
	require.Len(t, cfg.Filters, 2)
	assert.Equal(t, "a", cfg.Filters[0].Name)
	assert.Equal(t, "c", cfg.Filters[1].Name)

	assert.False(t, cfg.RemoveFilter("missing"))
	assert.Len(t, cfg.Filters, 2)
}

func TestSave_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "c", "config.yaml")
	t.Setenv("PR_MERGER_CONFIG", nested)

	_, err := Save(&Config{PollInterval: 10})
	require.NoError(t, err)
	_, err = os.Stat(nested)
	require.NoError(t, err)
}
