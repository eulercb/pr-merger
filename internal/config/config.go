package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Filter selects which PRs pr-merger should manage.
// A PR matches a filter when its repo matches and, if the optional
// fields are set, the PR also matches each of them.
type Filter struct {
	Name    string   `yaml:"name"`
	Repo    string   `yaml:"repo"`
	Authors []string `yaml:"authors,omitempty"`
	Labels  []string `yaml:"labels,omitempty"`
	Base    string   `yaml:"base,omitempty"`
}

type Config struct {
	// PollInterval is how often the watcher re-queries GitHub, in seconds.
	PollInterval int      `yaml:"poll_interval"`
	Filters      []Filter `yaml:"filters"`
}

func defaultPath() (string, error) {
	if p := os.Getenv("PR_MERGER_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pr-merger", "config.yaml"), nil
}

func Load() (*Config, string, error) {
	path, err := defaultPath()
	if err != nil {
		return nil, "", err
	}
	cfg := &Config{PollInterval: 30}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, path, nil
	}
	if err != nil {
		return nil, path, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, path, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 30
	}
	return cfg, path, nil
}

func Save(cfg *Config) (string, error) {
	path, err := defaultPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, err
	}
	sort.Slice(cfg.Filters, func(i, j int) bool { return cfg.Filters[i].Name < cfg.Filters[j].Name })
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return path, err
	}
	return path, os.WriteFile(path, data, 0o644)
}

func (c *Config) FindFilter(name string) (int, *Filter) {
	for i := range c.Filters {
		if c.Filters[i].Name == name {
			return i, &c.Filters[i]
		}
	}
	return -1, nil
}

func (c *Config) RemoveFilter(name string) bool {
	i, _ := c.FindFilter(name)
	if i < 0 {
		return false
	}
	c.Filters = append(c.Filters[:i], c.Filters[i+1:]...)
	return true
}
