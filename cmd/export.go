package cmd

import (
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Export is Seneca's evidence path: it answers "what did I actually write
// between these two days" with the author's own bytes and nothing else.
//
// # Read-only contract
//
// Export creates nothing, writes nothing, backs up nothing, stages nothing, and
// commits nothing. It reads the resolved reflections file exactly once and
// writes one Markdown packet to the command's output writer. It loads no review
// archive, runs no AI harness, translates nothing, stamps no time, truncates
// nothing, and styles nothing — so two runs over the same bytes produce the same
// packet, which is what makes the packet quotable as evidence.
//
// # Selection
//
// The range is either two canonical `YYYY-MM-DD` bounds, both included, or one
// named month as `--month=YYYY-MM`, which is shorthand for its first and last
// day. Every form names its days outright: there is no "this month" and no
// open end defaulting to today, because the same command must select the same
// days whenever it is run. A bound in the future is allowed: it selects the entries that exist
// and invents no day the author never wrote. Days missing inside the range are
// simply absent; the packet never fills a gap.
//
// # Refusals
//
// The whole document is validated before anything is selected, using the same
// policy a rewrite uses, because the same ambiguity that makes a rewrite lossy
// makes an export untrustworthy: an entry Seneca cannot date may belong inside
// the range, and a date used twice means the range's own entries are not
// uniquely identified. A refusal therefore fires on a bad heading anywhere in
// the file, not only between the bounds, and it fires before a single byte of
// packet is written.
//
// # Line endings
//
// Entry bytes are replayed verbatim, so an entry written with CRLF is exported
// with CRLF. Only the framing between entries is LF — exactly the normalization
// RenderReflectionEntries has always applied on the way out.

const (
	exportPacketTitle = "# Seneca journal export"
)

var (
	exportFrom  string
	exportTo    string
	exportMonth string
)

func init() {
	exportCmd.Flags().StringVar(&exportFrom, "from", "", "First date to export (YYYY-MM-DD, inclusive)")
	exportCmd.Flags().StringVar(&exportTo, "to", "", "Last date to export (YYYY-MM-DD, inclusive)")
	exportCmd.Flags().StringVar(&exportMonth, "month", "", "Export one whole month (YYYY-MM) instead of --from/--to")
	rootCmd.AddCommand(exportCmd)
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Print complete entries in a date range as Markdown",
	Long: "Print every reflection entry dated inside an inclusive range, complete and\n" +
		"unchanged, as one deterministic Markdown packet on standard output.\n\n" +
		"Give the range as --from and --to, both canonical YYYY-MM-DD calendar dates\n" +
		"with both ends included, or as --month=YYYY-MM for one whole month. A future\n" +
		"bound is allowed: it selects the entries that exist rather than inventing the\n" +
		"days that do not.\n\n" +
		"Export is strictly read-only. It writes nothing, backs up nothing, commits\n" +
		"nothing, and calls no AI harness. Nothing is summarized, reordered inside an\n" +
		"entry, or trimmed: the packet carries each entry's own bytes.\n\n" +
		"It refuses before printing anything when the journal holds an entry seneca\n" +
		"cannot date or a date used twice — anywhere in the file, not only inside the\n" +
		"range — because a packet built from an ambiguous journal could quietly omit\n" +
		"or misdate a day.",
	Args: cobra.NoArgs,
	RunE: runExport,
	// A failure here is a bad range or an unreadable journal, not a mistyped
	// command. Cobra's usage dump would bury the reason, and its error print
	// would be a second copy of what main already writes to stderr — and both go
	// to the writer the packet is built on, so silencing them is also what keeps
	// a failed run from emitting something that reads like output.
	SilenceUsage:  true,
	SilenceErrors: true,
}

func runExport(cmd *cobra.Command, args []string) error {
	bounds, err := exportRangeFromFlags(cmd, exportFrom, exportTo, exportMonth)
	if err != nil {
		return err
	}

	path, err := resolveFilePath()
	if err != nil {
		return err
	}

	return exportJournal(cmd.OutOrStdout(), path, bounds)
}

// exportRange is an inclusive, forward-running span of calendar days.
type exportRange struct{ from, to string }

// contains reports whether a canonical entry date falls inside the range.
// Comparing the strings is comparing the days: a validated entry date is
// zero-padded YYYY-MM-DD, which sorts lexicographically exactly as it sorts
// chronologically. An undated entry belongs to no range.
func (r exportRange) contains(date string) bool {
	return date != "" && r.from <= date && date <= r.to
}

// exportRangeFromFlags picks the range form the author used. Exactly one form
// is accepted: a month and an explicit bound together would leave it unclear
// which one the packet's Range line is promising.
func exportRangeFromFlags(cmd *cobra.Command, from, to, month string) (exportRange, error) {
	fromSet, toSet := cmd.Flags().Changed("from"), cmd.Flags().Changed("to")
	switch {
	case cmd.Flags().Changed("month"):
		if fromSet || toSet {
			return exportRange{}, fmt.Errorf("--month cannot be combined with --from or --to; give one range")
		}
		return parseExportMonth(month)
	case !fromSet && !toSet:
		return exportRange{}, fmt.Errorf("export needs a range: --from YYYY-MM-DD --to YYYY-MM-DD, or --month=YYYY-MM")
	case !toSet:
		return exportRange{}, fmt.Errorf("--from needs --to: the range is inclusive and both ends must be named")
	case !fromSet:
		return exportRange{}, fmt.Errorf("--to needs --from: the range is inclusive and both ends must be named")
	}
	return parseExportRange(from, to)
}

// parseExportMonth expands a canonical YYYY-MM into its first and last day.
// The layout is fixed-width, so `2026-9` is refused rather than guessed at.
func parseExportMonth(month string) (exportRange, error) {
	first, err := time.Parse("2006-01", month)
	if err != nil {
		return exportRange{}, fmt.Errorf("invalid --month %q: expected a month as YYYY-MM", month)
	}
	last := first.AddDate(0, 1, -1)
	return exportRange{from: first.Format(engine.CalendarDateLayout), to: last.Format(engine.CalendarDateLayout)}, nil
}

func parseExportRange(from, to string) (exportRange, error) {
	if err := engine.RequireCalendarDate("from", from); err != nil {
		return exportRange{}, err
	}
	if err := engine.RequireCalendarDate("to", to); err != nil {
		return exportRange{}, err
	}
	if from > to {
		return exportRange{}, fmt.Errorf(
			"invalid range: --from %s is after --to %s; the range is inclusive and must run forward in time", from, to)
	}
	return exportRange{from: from, to: to}, nil
}

// emptyExportError reports a range that selected nothing. Exporting silence
// would look identical to exporting a day the author left blank, so an empty
// selection is a refusal rather than an empty packet.
type emptyExportError struct {
	path   string
	bounds exportRange
	total  int
}

func (e *emptyExportError) Error() string {
	return fmt.Sprintf("no reflection entries dated from %s through %s in %s; the journal holds %s (nothing was exported)",
		e.bounds.from, e.bounds.to, e.path, engine.Plural(e.total, "entry", "entries"))
}

func exportJournal(out io.Writer, path string, bounds exportRange) error {
	// Provenance has to name a file the reader can open from wherever they are
	// standing, and SENECA_FILE is allowed to hold a relative path.
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving %s: %w", path, err)
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	doc := engine.ParseReflectionDocument(string(data))
	if err := engine.ValidateReflectionDocument(engine.ReflectionsFileLabel, abs, doc); err != nil {
		return err
	}

	selected := selectExportEntries(doc.Entries, bounds)
	if len(selected) == 0 {
		return &emptyExportError{path: abs, bounds: bounds, total: len(doc.Entries)}
	}

	// Built whole before anything is written, so a writer that fails partway
	// cannot leave a truncated packet that still reads like a complete one.
	packet := renderExportPacket(abs, bounds, selected)
	if _, err := io.WriteString(out, packet); err != nil {
		return fmt.Errorf("writing export: %w", err)
	}
	return nil
}

// selectExportEntries returns the entries inside bounds, in date order. The
// parsed document keeps its source order: this command does not rewrite the
// file, so reordering the parse result would only make the in-memory view
// disagree with the bytes on disk.
func selectExportEntries(entries []engine.ReflectionEntry, bounds exportRange) []engine.ReflectionEntry {
	var selected []engine.ReflectionEntry
	for _, e := range entries {
		if bounds.contains(e.Date) {
			selected = append(selected, e)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].Date < selected[j].Date
	})
	return selected
}

// renderExportPacket lays out the packet: a provenance header, then the entries
// themselves through the shared renderer.
//
// The header is framed like an entry — it ends with the same separator every
// entry ends with — so the packet has one boundary grammar throughout and a
// reader can tell the provenance apart from the writing without guessing. The
// header can never be mistaken for an entry: only a line that is exactly `---`
// separates, and an entry raw never contains one, because that is where the
// parser cut.
func renderExportPacket(path string, bounds exportRange, entries []engine.ReflectionEntry) string {
	dates := make([]string, 0, len(entries))
	for _, e := range entries {
		dates = append(dates, e.Date)
	}

	var b strings.Builder
	b.WriteString(exportPacketTitle)
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "- Source: %s\n", path)
	fmt.Fprintf(&b, "- Range: %s to %s (inclusive)\n", bounds.from, bounds.to)
	fmt.Fprintf(&b, "- Entries: %d\n", len(entries))
	fmt.Fprintf(&b, "- Dates: %s", strings.Join(dates, ", "))
	b.WriteString(engine.EntryTerminator)
	b.WriteString(engine.RenderReflectionEntries(entries))
	return b.String()
}
