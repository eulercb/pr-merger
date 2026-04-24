package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/eulercb/pr-merger/internal/config"
	"github.com/eulercb/pr-merger/internal/gh"
	"github.com/eulercb/pr-merger/internal/prutil"
	"github.com/eulercb/pr-merger/internal/watcher"
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
			"rebases one PR at a time per repo, and lets GitHub auto-merge take over once required checks pass.",
		SilenceUsage: true,
	}
	root.AddCommand(newWatchCmd(), newFilterCmd(), newStatusCmd(), newConfigCmd())
	return root
}

// ---- watch ----

func newWatchCmd() *cobra.Command {
	var interval time.Duration
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Start the watch loop (runs until Ctrl-C)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if len(cfg.Filters) == 0 {
				return fmt.Errorf("no filters configured in %s; use `pr-merger filter add ...`", path)
			}

			pollInterval := interval
			if pollInterval <= 0 {
				pollInterval = time.Duration(cfg.PollInterval) * time.Second
			}

			logger := log.New(os.Stdout, "", log.LstdFlags)
			w := watcher.New(gh.New(), cfg.Filters, pollInterval, logger)

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
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

			// Per-repo timeout so a slow repo can't starve later ones.
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

