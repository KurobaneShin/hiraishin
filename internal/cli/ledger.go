package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func ledgerCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ledger",
		Short: "Ledger maintenance commands",
	}
	cmd.AddCommand(archiveCmd(a))
	return cmd
}

func archiveCmd(a *app) *cobra.Command {
	var before, to string
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Move old events out of the live ledger into an archive file",
		Args:  args(0, ""),
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, err := parseBefore(before)
			if err != nil {
				return usageError{msg: fmt.Sprintf("--before: %v", err)}
			}
			ctx := cmd.Context()
			e, err := a.open(ctx)
			if err != nil {
				return err
			}
			defer func() { _ = e.Close() }()
			cutoff := a.env.Now().Add(-d)
			archivePath := to
			if archivePath == "" {
				archivePath = filepath.Join(e.Paths.DataDir, "archive", fmt.Sprintf("ledger-archive-%s.db", cutoff.UTC().Format("20060102T150405Z")))
			}
			copied, err := e.Ledger.Archive(ctx, cutoff, archivePath)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Archived %d events before %s to %s\n", copied, cutoff.UTC().Format(time.RFC3339), archivePath)
			return err
		},
	}
	cmd.Flags().StringVar(&before, "before", "30d", "archive events older than this duration (e.g. 30d, 720h)")
	cmd.Flags().StringVar(&to, "to", "", "archive file path (default: <data_dir>/archive/ledger-archive-<ts>.db)")
	return cmd
}

func parseBefore(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("want N days, got %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
