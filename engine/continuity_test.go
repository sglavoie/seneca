package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContinuityContextShowsFocusAndDatedPriorIntention(t *testing.T) {
	t.Parallel()

	archivePath := archiveWithFocus(t, 20, "Protect the first hour.")
	entries := journalEntries(t,
		dayEntry("2026-08-18", "Noticed.", "Handled.", "Finish the migration."),
	)

	ctx := mustContext(t, entries, archivePath, "2026-08-24", 3, false)

	if ctx.Focus != "Protect the first hour." {
		t.Fatalf("expected the active focus, got %q", ctx.Focus)
	}
	if !ctx.ShowFollowThrough {
		t.Fatal("expected a follow-through field")
	}
	// Six days back, not "yesterday": the date shown is the day it was written.
	if ctx.PriorDate != "2026-08-18" || ctx.PriorText != "Finish the migration." {
		t.Fatalf("expected the 2026-08-18 intention, got %+v", ctx)
	}
	if ctx.FollowInitial != "" {
		t.Fatalf("expected an empty follow-through for a new entry, got %q", ctx.FollowInitial)
	}
}

func TestContinuityContextWithoutArchiveKeepsPriorIntention(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), ArchiveFileName)
	entries := journalEntries(t, dayEntry("2026-08-23", "Noticed.", "Handled.", "Ship the parser."))

	ctx := mustContext(t, entries, missing, "2026-08-24", 3, false)

	if ctx.Focus != "" {
		t.Fatalf("expected no focus without an archive, got %q", ctx.Focus)
	}
	if !ctx.ShowFollowThrough || ctx.PriorText != "Ship the parser." {
		t.Fatalf("expected the prior intention to survive a missing archive, got %+v", ctx)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected a read to leave the archive uncreated")
	}
}

func TestContinuityContextHidesClearedAndFutureFocus(t *testing.T) {
	t.Parallel()

	entries := journalEntries(t, dayEntry("2026-08-23", "Noticed.", "Handled.", "Ship the parser."))

	cleared := archiveWithFocus(t, 20, "Protect the first hour.")
	mustAppend(t, cleared, NewFocusClearRecord(archiveTime(22, 9, 0)))
	if ctx := mustContext(t, entries, cleared, "2026-08-24", 3, false); ctx.Focus != "" {
		t.Fatalf("expected a cleared focus to be invisible, got %q", ctx.Focus)
	}

	// A focus set after the reflection date does not reach backwards.
	future := archiveWithFocus(t, 26, "Write the handoff.")
	if ctx := mustContext(t, entries, future, "2026-08-24", 3, false); ctx.Focus != "" {
		t.Fatalf("expected a future-dated focus to be invisible, got %q", ctx.Focus)
	}
	if ctx := mustContext(t, entries, future, "2026-08-27", 3, false); ctx.Focus != "Write the handoff." {
		t.Fatalf("expected the focus once effective, got %q", ctx.Focus)
	}
}

func TestContinuityContextBlankHidesEverythingAndSkipsTheArchive(t *testing.T) {
	t.Parallel()

	// A malformed archive would fail a context load outright; --blank must not
	// even look at it.
	malformed := filepath.Join(t.TempDir(), ArchiveFileName)
	if err := os.WriteFile(malformed, []byte("<!-- seneca:archive v=2 -->\n\n## Focus\n<!-- seneca:focus v=2 -->\n\nbroken\n"), 0644); err != nil {
		t.Fatalf("writing malformed archive: %v", err)
	}
	entries := journalEntries(t, dayEntry("2026-08-23", "Noticed.", "Handled.", "Ship the parser."))

	ctx := mustContext(t, entries, malformed, "2026-08-24", 3, true)
	if ctx.Focus != "" || ctx.ShowFollowThrough || ctx.HasContext() {
		t.Fatalf("expected --blank to hide all context, got %+v", ctx)
	}

	// The same archive is fatal when context is actually requested.
	_, err := ContinuityContextFor(entries, malformed, "2026-08-24", continuityQuestions, 3, false)
	var unreadable *UnreadableContextError
	if !errors.As(err, &unreadable) {
		t.Fatalf("expected an unreadableContextError, got %v", err)
	}
	if !strings.Contains(err.Error(), "seneca doctor") || !strings.Contains(err.Error(), "--blank") {
		t.Fatalf("expected a diagnostic pointing at doctor and --blank, got %q", err.Error())
	}
}

func TestContinuityContextWithoutPriorIntentionOffersNoFollowThrough(t *testing.T) {
	t.Parallel()

	archivePath := archiveWithFocus(t, 20, "Protect the first hour.")

	// An empty journal has nothing to follow through on.
	if ctx := mustContext(t, nil, archivePath, "2026-08-24", 3, false); ctx.ShowFollowThrough {
		t.Fatal("expected no follow-through field on a first-ever reflection")
	}

	// A disabled anchor never produces one either, focus or not.
	entries := journalEntries(t, dayEntry("2026-08-23", "Noticed.", "Handled.", "Ship the parser."))
	ctx := mustContext(t, entries, archivePath, "2026-08-24", 0, false)
	if ctx.ShowFollowThrough {
		t.Fatal("expected a disabled anchor to suppress the follow-through field")
	}
	if ctx.Focus != "Protect the first hour." {
		t.Fatalf("expected focus to survive a disabled anchor, got %q", ctx.Focus)
	}
}

func TestContinuityContextReusesTheStoredSnapshotWhenEditing(t *testing.T) {
	t.Parallel()

	stored := "" +
		"## 2026-08-24\n" +
		"<!-- seneca:entry v=2 -->\n\n" +
		"### Notice?\n" +
		"Noticed.\n\n" +
		"<!-- seneca:follow-through source=2026-08-18 -->\n" +
		"### Follow-through\n" +
		"> Finish the migration.\n\n" +
		"It took the whole morning.\n\n"

	// The journal has moved on: a newer intention exists, but the entry being
	// edited already answers 2026-08-18 and must keep answering it.
	entries := journalEntries(t,
		dayEntry("2026-08-23", "Noticed.", "Handled.", "Ship the parser."),
		stored,
	)

	ctx := mustContext(t, entries, filepath.Join(t.TempDir(), ArchiveFileName), "2026-08-24", 3, false)

	if ctx.PriorDate != "2026-08-18" || ctx.PriorText != "Finish the migration." {
		t.Fatalf("expected the stored snapshot, got %+v", ctx)
	}
	if ctx.FollowInitial != "It took the whole morning." {
		t.Fatalf("expected the stored response as the initial value, got %q", ctx.FollowInitial)
	}
}
