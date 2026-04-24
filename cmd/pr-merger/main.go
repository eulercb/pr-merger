package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
	"github.com/eulercb/pr-merger/internal/prutil"
	"github.com/eulercb/pr-merger/internal/tui"
	"github.com/eulercb/pr-merger/internal/watcher"
	"github.com/eulercb/pr-merger/internal/wizard"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "pr-merger",
		Short: "Watch GitHub PRs with auto-merge enabled and rebase them one at a time",
		Long: "pr-merger monitors PRs that have auto-merge enabled in the repos/filters you configure, " +
			"rebases one PR at a time per repo, and lets GitHub auto-merge take over once required checks pass. " +
			"With no subcommand, it launches an interactive dashboard.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDashboard(cmd.Context())
		},
	}
	root.AddCommand(newWatchCmd(), newFilterCmd(), newStatusCmd(), newConfigCmd(), newSetupCmd())
	return root
}

// ---- dashboard (default) ----

func runDashboard(parent context.Context) error {
	cfg, path, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if len(cfg.Filters) == 0 {
		fmt.Fprintln(os.Stderr, "No filters configured. Launching setup wizard...")
		built, err := runWizard(parent)
		if err != nil {
			return err
		}
		if built == nil {
			return nil // user aborted
		}
		if _, err := config.Save(built); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Saved config to %s\n", path)
		cfg = built
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := gh.New()
	interval := time.Duration(cfg.PollInterval) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}

	// Watcher logs must never hit stdout/stderr while the TUI owns the
	// alt-screen — the escape sequences would corrupt the render. Try to
	// write to a log file next to the config; on failure, silently discard
	// rather than poison the UI.
	logOut := io.Writer(io.Discard)
	if f, err := os.OpenFile(logPathForConfig(path), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		defer f.Close()
		logOut = f
	}
	logger := log.New(logOut, "", log.LstdFlags)

	w := watcher.New(client, cfg.Filters, interval, logger)

	model := tui.NewModel(cfg)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithAltScreen())

	// Pump watcher events into the bubbletea program.
	go func() {
		for ev := range w.Events {
			program.Send(tui.WatcherEventMsg(ev))
		}
	}()

	// Run the watcher in the background; stop it when the TUI exits.
	watcherDone := make(chan error, 1)
	go func() {
		watcherDone <- w.Run(ctx)
	}()

	_, uiErr := program.Run()
	stop() // cancel the watcher
	watcherErr := <-watcherDone
	if watcherErr != nil {
		watcherErr = fmt.Errorf("watcher: %w", watcherErr)
	}
	return errors.Join(uiErr, watcherErr)
}

// logPathForConfig returns the log file path alongside the user's config
// file, e.g. ~/.config/pr-merger/config.yaml → ~/.config/pr-merger/pr-merger.log.
func logPathForConfig(cfgPath string) string {
	dir := filepath.Dir(cfgPath)
	if dir == "" || dir == "." {
		return "pr-merger.log"
	}
	return filepath.Join(dir, "pr-merger.log")
}

// runWizard launches the interactive wizard and returns the resulting config,
// or (nil, nil) if the user aborted.
func runWizard(ctx context.Context) (*config.Config, error) {
	discCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	disc := wizard.Discover(discCtx, gh.New())
	cancel()

	m := wizard.NewModel(disc)
	prog := tea.NewProgram(m)
	final, err := prog.Run()
	if err != nil {
		return nil, fmt.Errorf("wizard: %w", err)
	}
	fm, ok := final.(wizard.Model)
	if !ok {
		return nil, errors.New("wizard: unexpected final model type")
	}
	return fm.Cfg, nil
}

// ---- setup ----

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Run the interactive setup wizard (overwrites any existing config)",
		RunE: func(cmd *cobra.Command, args []string) error {
			built, err := runWizard(cmd.Context())
			if err != nil {
				return err
			}
			if built == nil {
				fmt.Fprintln(os.Stderr, "wizard aborted")
				return nil
			}
			path, err := config.Save(built)
			if err != nil {
				return err
			}
			fmt.Printf("saved config to %s\n", path)
			return nil
		},
	}
}

// ---- watch (headless) ----

func newWatchCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Run the watcher loop without a TUI (logs to stdout)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if len(cfg.Filters) == 0 {
				return fmt.Errorf("no filters configured in %s; run `pr-merger setup`", path)
			}

			pollInterval := interval
			if pollInterval <= 0 {
				pollInterval = time.Duration(cfg.PollInterval) * time.Second
			}

			logger := log.New(os.Stdout, "", log.LstdFlags)
			w := watcher.New(gh.New(), cfg.Filters, pollInterval, logger)

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			// Drain events (the watcher closes the channel on shutdown).
			go func() {
				for range w.Events {
				}
			}()
			return w.Run(ctx)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 0, "Poll interval (default: value from config, or 30s)")
	return cmd
}

// ---- status ----

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print matching PRs and their auto-merge/rebase state (one pass, no watch)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			if len(cfg.Filters) == 0 {
				return fmt.Errorf("no filters configured")
			}

			client := gh.New()
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "REPO\t#\tAUTHOR\tAUTO-MERGE\tSTATE\tMERGEABLE\tTITLE")

			repos := map[string][]config.Filter{}
			for _, f := range cfg.Filters {
				repos[f.Repo] = append(repos[f.Repo], f)
			}
			keys := make([]string, 0, len(repos))
			for k := range repos {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			const perRepoTimeout = 30 * time.Second
			for _, repo := range keys {
				repoCtx, cancel := context.WithTimeout(cmd.Context(), perRepoTimeout)
				prs, err := client.ListOpenPRs(repoCtx, repo)
				cancel()
				if err != nil {
					fmt.Fprintf(os.Stderr, "[%s] list error: %v\n", repo, err)
					continue
				}
				sort.Slice(prs, func(i, j int) bool { return prs[i].Number < prs[j].Number })
				for _, pr := range prs {
					if !prutil.MatchesAny(pr, repos[repo]) {
						continue
					}
					auto := "no"
					if pr.AutoMergeRequest != nil {
						auto = strings.ToLower(pr.AutoMergeRequest.MergeMethod)
					}
					fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
						repo, pr.Number, pr.Author.Login, auto,
						pr.MergeStateStatus, pr.Mergeable, prutil.Truncate(pr.Title, 60))
				}
			}
			return w.Flush()
		},
	}
}

// ---- filter ----

func newFilterCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "filter",
		Short: "Manage PR filters",
	}
	cmd.AddCommand(newFilterAddCmd(), newFilterListCmd(), newFilterRemoveCmd())
	return cmd
}

func newFilterAddCmd() *cobra.Command {
	var f config.Filter
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add or update a filter",
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.Name == "" || f.Repo == "" {
				return fmt.Errorf("--name and --repo are required")
			}
			if !strings.Contains(f.Repo, "/") {
				return fmt.Errorf("--repo must be in OWNER/NAME form")
			}
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			if i, existing := cfg.FindFilter(f.Name); existing != nil {
				cfg.Filters[i] = f
			} else {
				cfg.Filters = append(cfg.Filters, f)
			}
			path, err := config.Save(cfg)
			if err != nil {
				return err
			}
			fmt.Printf("saved filter %q to %s\n", f.Name, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&f.Name, "name", "", "Filter name (unique key)")
	cmd.Flags().StringVar(&f.Repo, "repo", "", "Repository in OWNER/NAME form")
	cmd.Flags().StringSliceVar(&f.Authors, "author", nil, "Restrict to PRs by these authors (repeatable)")
	cmd.Flags().StringSliceVar(&f.Labels, "label", nil, "Require all of these labels (repeatable)")
	cmd.Flags().StringVar(&f.Base, "base", "", "Restrict to this base branch")
	return cmd
}

func newFilterListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured filters",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.Load()
			if err != nil {
				return err
			}
			if len(cfg.Filters) == 0 {
				fmt.Printf("no filters configured (%s)\n", path)
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tREPO\tAUTHORS\tLABELS\tBASE")
			for _, f := range cfg.Filters {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					f.Name, f.Repo,
					strings.Join(f.Authors, ","),
					strings.Join(f.Labels, ","),
					f.Base,
				)
			}
			return w.Flush()
		},
	}
}

func newFilterRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a filter by name",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := config.Load()
			if err != nil {
				return err
			}
			if !cfg.RemoveFilter(args[0]) {
				return fmt.Errorf("no filter named %q", args[0])
			}
			path, err := config.Save(cfg)
			if err != nil {
				return err
			}
			fmt.Printf("removed filter %q (%s)\n", args[0], path)
			return nil
		},
	}
}

// ---- config ----

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect configuration",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, path, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	})
	return cmd
}
