package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// The watcher is the exchange run by nobody: it stays in the foreground of
// whatever started it (a terminal, or the LaunchAgent `seneca sync service
// install` writes) and runs one exchange whenever the journal's files or the
// exchange directory change, and at an interval whether they changed or not.
//
// It holds nothing between exchanges. Each one opens the journal afresh, takes
// the journal lock like any command, and releases it, so a command the author
// runs meanwhile is never kept waiting by the watcher being alive.

var (
	syncWatchInterval time.Duration
	syncWatchPoll     time.Duration
)

func init() {
	syncWatchCmd.Flags().DurationVar(&syncWatchInterval, "interval", 5*time.Minute, "How often to exchange even when nothing was seen to change; also the longest wait between retries")
	syncWatchCmd.Flags().DurationVar(&syncWatchPoll, "poll", 2*time.Second, "How often to look at the journal's files and the exchange for a change")
	syncCmd.AddCommand(syncWatchCmd)
}

var syncWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Keep exchanging in the foreground until interrupted",
	Long: "Run an exchange whenever the journal's files or the exchange directory change,\n" +
		"and every --interval regardless. A step that fails is retried with a growing\n" +
		"delay that never exceeds --interval. Nothing is imported while Git is in the\n" +
		"middle of a merge or a rebase. This is what the background service runs.",
	Args: cobra.NoArgs,
	RunE: runSyncWatch,
}

func runSyncWatch(cmd *cobra.Command, args []string) error {
	if syncWatchPoll <= 0 || syncWatchInterval < syncWatchPoll {
		return fmt.Errorf("invalid --poll %s and --interval %s: the poll must be positive and no longer than the interval", syncWatchPoll, syncWatchInterval)
	}
	setup, err := resolveSyncSetup()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	watchSync(ctx, cmd.OutOrStdout(), setup, syncWatchInterval, syncWatchPoll)
	return nil
}

// watchFingerprint is everything the watcher looks at between exchanges: the
// size and modification time of each journal file, and of the directories of
// the exchange a delivery lands in. It reads no file's contents.
func watchFingerprint(setup syncSetup) string {
	var b strings.Builder
	stamp := func(path string) {
		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(&b, "%s:-\n", path)
			return
		}
		fmt.Fprintf(&b, "%s:%d:%d\n", path, info.Size(), info.ModTime().UnixNano())
	}
	for _, path := range setup.managed() {
		stamp(path)
	}
	// Enrolling, and every local save, rewrites the state.
	stamp(engine.SyncStatePathFor(setup.reflections))
	if setup.exchange != "" {
		stamp(filepath.Join(setup.exchange, "journal.json"))
		stamp(filepath.Join(setup.exchange, "objects"))
		transactions := filepath.Join(setup.exchange, "transactions")
		stamp(transactions)
		// A manifest arriving changes its device's directory, not the one
		// above it.
		if devices, err := os.ReadDir(transactions); err == nil {
			for _, device := range devices {
				stamp(filepath.Join(transactions, device.Name()))
			}
		}
	}
	return b.String()
}

// retryDelay is how long to wait before trying again after failures in a row:
// the poll doubled each time, never more than the interval.
func retryDelay(failures int, poll, interval time.Duration) time.Duration {
	delay := poll
	for i := 1; i < failures && delay < interval; i++ {
		delay *= 2
	}
	if delay > interval {
		return interval
	}
	return delay
}

// watchSync runs until ctx is done.
func watchSync(ctx context.Context, out io.Writer, setup syncSetup, interval, poll time.Duration) {
	logf := func(format string, args ...any) {
		fmt.Fprintf(out, "%s %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
	}
	exchange := setup.exchange
	if exchange == "" {
		exchange = "no exchange configured"
	}
	logf("watching %s (exchange: %s)", setup.reflections, exchange)

	var (
		seen     string
		changed  = true // the first look is an exchange
		due      time.Time
		scan     time.Time
		failures int
		said     string
	)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		now := time.Now()
		if current := watchFingerprint(setup); current != seen {
			// Something is being written. Wait for one quiet look before
			// reading it: an editor or a file service is rarely done in one.
			if seen != "" {
				changed = true
				due = time.Time{}
				failures = 0
			}
			seen = current
		} else if (changed && !now.Before(due)) || !now.Before(scan) {
			report := runSyncCycle(setup, now, true)
			// What the exchange itself wrote is not a change to react to.
			seen = watchFingerprint(setup)
			scan = now.Add(interval)

			lines := report.done()
			attention := strings.Join(report.attention(), "; ")
			if !report.enrolled && report.importErr == nil {
				attention = "this journal does not take part in sync; waiting for `seneca sync enroll`"
			}
			for _, line := range lines {
				logf("%s", line)
			}
			// A problem is said once, and again only when it changes.
			if attention != said {
				if attention != "" {
					logf("%s", attention)
				} else {
					logf("settled")
				}
				said = attention
			}

			if report.unsettled() {
				failures++
				changed = true
				due = now.Add(retryDelay(failures, poll, interval))
			} else {
				failures = 0
				changed = false
			}
		}

		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				logf("stopping: %v", ctx.Err())
			} else {
				logf("stopping")
			}
			return
		case <-ticker.C:
		}
	}
}
