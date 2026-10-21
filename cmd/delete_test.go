package cmd

import (
	"bytes"
	"errors"
	"github.com/sglavoie/seneca/engine"
	"strings"
	"testing"
	"time"
)

const deleteSeed = "" +
	"## 2026-05-24\n\n### One?\nKeep me.\n---\n\n" +
	"## 2026-05-25\n\n### One?\nDelete me.\n---\n\n"

func yes(string) (bool, error) { return true, nil }

func TestDeleteRemovesOnlyTheDayAndCommits(t *testing.T) {
	root, path := seedSortRepo(t, deleteSeed)
	journal, err := openJournal(path, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := deleteReflection(&out, journal, path, "2026-05-25", yes); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got := readFileString(t, path)
	if strings.Contains(got, "Delete me.") || !strings.Contains(got, "Keep me.") {
		t.Fatalf("unexpected journal after delete: %q", got)
	}
	if backup := readFileString(t, path+engine.BackupSuffix); backup != deleteSeed {
		t.Fatalf("backup should hold the bytes before the deletion, got %q", backup)
	}
	if !strings.Contains(out.String(), "Delete me.") {
		t.Fatalf("the entry was not shown before deleting: %q", out.String())
	}
	head := runGit(t, root, "show", "--name-status", "--format=%s", "HEAD")
	if !strings.Contains(head, "seneca: delete reflection 2026-05-25") {
		t.Fatalf("expected a delete commit, got %q", head)
	}
}

func TestDeleteDeclinedOrUnknownDayWritesNothing(t *testing.T) {
	_, path := seedSortRepo(t, deleteSeed)
	journal, err := openJournal(path, engine.ReflectionSettings{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	no := askConfirmation(strings.NewReader("n\n"), &bytes.Buffer{}, true)
	var out bytes.Buffer
	if err := deleteReflection(&out, journal, path, "2026-05-25", no); err != nil {
		t.Fatalf("declined delete: %v", err)
	}
	if !strings.Contains(out.String(), "nothing was deleted") {
		t.Fatalf("declining should say so: %q", out.String())
	}

	if err := deleteReflection(&bytes.Buffer{}, journal, path, "2026-05-20", yes); err == nil ||
		!strings.Contains(err.Error(), "no reflection saved for 2026-05-20") {
		t.Fatalf("an unknown day should be refused, got %v", err)
	}

	quiet := askConfirmation(strings.NewReader("y\n"), &bytes.Buffer{}, false)
	if err := deleteReflection(&bytes.Buffer{}, journal, path, "2026-05-25", quiet); !errors.Is(err, errDeleteNeedsYes) {
		t.Fatalf("without a terminal the delete needs --yes, got %v", err)
	}

	if got := readFileString(t, path); got != deleteSeed {
		t.Fatalf("nothing should have been written, got %q", got)
	}
}
