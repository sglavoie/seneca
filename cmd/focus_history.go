package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
)

var focusHistoryLast int

func init() {
	focusHistoryCmd.Flags().IntVar(&focusHistoryLast, "last", 10, "How many focus events to show, newest first")
	focusCmd.AddCommand(focusHistoryCmd)
}

var focusHistoryCmd = &cobra.Command{
	Use:   "history",
	Short: "Show the focus timeline from the archive",
	Long: "Read the focus event stream back out of the review archive, newest first.\n" +
		"Each event shows the span it held, up to the next event or still current.\n" +
		"This never contacts an AI harness and never creates a file.",
	Args: cobra.NoArgs,
	RunE: runFocusHistory,
}

func runFocusHistory(cmd *cobra.Command, args []string) error {
	path, err := resolveArchivePath()
	if err != nil {
		return err
	}
	return showFocusHistory(cmd.OutOrStdout(), path, focusHistoryLast, term.IsTerminal(os.Stdout.Fd()))
}

// focusHistoryEntry is one focus event with the span it held. until is the
// effective date of the event that replaced it, empty for the newest event,
// which is still current.
type focusHistoryEntry struct {
	event engine.FocusEvent
	until string
}

// showFocusHistory prints the newest `last` focus events. A missing archive or
// an archive holding no focus events is reported, never created or filled.
func showFocusHistory(out io.Writer, path string, last int, tty bool) error {
	if last <= 0 {
		return fmt.Errorf("invalid --last %d: expected a positive number", last)
	}

	archive, exists, err := engine.LoadReviewArchive(path)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(out, "No focus recorded yet: no review archive at %s\n", path)
		return nil
	}
	warnArchiveDiagnostics(archive)

	entries := focusHistoryEntries(archive.FocusEvents())
	if len(entries) == 0 {
		fmt.Fprintf(out, "No focus events yet in %s\n", path)
		return nil
	}
	if len(entries) > last {
		entries = entries[:last]
	}

	if tty {
		fmt.Fprintln(out, renderFocusHistoryTTY(entries))
	} else {
		fmt.Fprintln(out, renderFocusHistoryPlain(entries))
	}
	return nil
}

// focusHistoryEntries reverses the creation-order timeline into newest-first
// display order and pairs each event with the one that replaced it. Reversing
// file order is what makes the display agree with focusOn: when two events
// share an effective date focusOn takes the later file position, and that is
// the one shown first here.
func focusHistoryEntries(events []engine.FocusEvent) []focusHistoryEntry {
	entries := make([]focusHistoryEntry, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		entry := focusHistoryEntry{event: events[i]}
		if i+1 < len(events) {
			entry.until = events[i+1].Effective
		}
		entries = append(entries, entry)
	}
	return entries
}

func renderFocusHistoryTTY(entries []focusHistoryEntry) string {
	isDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	ld := lipgloss.LightDark(isDark)
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Foreground(ld(lipgloss.Color("244"), lipgloss.Color("241")))

	ruleWidth := 72
	if width, _, err := term.GetSize(os.Stdout.Fd()); err == nil && width > 0 && width < ruleWidth {
		ruleWidth = width
	}
	rule := dim.Render(strings.Repeat("─", ruleWidth))

	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		var b strings.Builder
		b.WriteString(bold.Render(focusHistoryTitle(entry.event)))
		b.WriteByte('\n')
		b.WriteString(dim.Render(focusHistoryMeta(entry)))
		if entry.event.Text != "" {
			b.WriteString("\n\n")
			b.WriteString(entry.event.Text)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n\n"+rule+"\n\n")
}

func renderFocusHistoryPlain(entries []focusHistoryEntry) string {
	var b strings.Builder
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "%s\n", focusHistoryTitle(entry.event))
		fmt.Fprintf(&b, "%s\n", focusHistoryMeta(entry))
		if entry.event.Text != "" {
			b.WriteByte('\n')
			b.WriteString(entry.event.Text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func focusHistoryTitle(event engine.FocusEvent) string {
	action := "set"
	if event.Event == engine.FocusEventClear {
		action = "clear"
	}
	return fmt.Sprintf("%s — %s", event.Effective, action)
}

// focusHistoryMeta is the one dim line under a title: where the event came
// from, the review it came from when it came from one, and the span it held.
func focusHistoryMeta(entry focusHistoryEntry) string {
	meta := fmt.Sprintf("origin %s", entry.event.Origin)
	if entry.event.Origin == engine.FocusOriginReview && entry.event.Source != "" {
		meta += fmt.Sprintf("  source %s", engine.ShortRecordID(entry.event.Source))
	}
	return meta + "  " + focusHistorySpan(entry)
}

// focusHistorySpan describes how long an event stood: up to the effective date
// of the next event, or "current" for the newest. The span is structural, not a
// score — no counts, no coverage, no streaks.
func focusHistorySpan(entry focusHistoryEntry) string {
	switch {
	case entry.until == "":
		return fmt.Sprintf("span %s → current", entry.event.Effective)
	case entry.until == entry.event.Effective:
		return fmt.Sprintf("span %s → %s (superseded the same day)", entry.event.Effective, entry.until)
	default:
		return fmt.Sprintf("span %s → %s", entry.event.Effective, entry.until)
	}
}
