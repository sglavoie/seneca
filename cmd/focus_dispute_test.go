package cmd

import (
	"github.com/sglavoie/seneca/engine"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShowFocusReportsADisputeInsteadOfAWinner(t *testing.T) {
	t.Parallel()

	exchange := engine.NewDirectoryTransport(t.TempDir())
	open := func(hour int) *engine.Journal {
		journal, err := engine.Open(engine.Options{
			ReflectionsPath: filepath.Join(t.TempDir(), "reflections.md"),
			Settings:        engine.ReflectionSettings{Questions: []string{"Notice?"}},
			Clock:           func() time.Time { return archiveTime(24, hour, 0) },
		})
		if err != nil {
			t.Fatalf("Open returned error: %v", err)
		}
		return journal
	}
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s returned error: %v", what, err)
		}
	}

	desk, phone := open(9), open(10)
	_, err := desk.EnrollSync("desk")
	must("EnrollSync", err)
	_, err = desk.PublishSync(exchange)
	must("PublishSync", err)
	_, err = phone.JoinSync(exchange, "phone")
	must("JoinSync", err)
	_, err = phone.ImportSync(exchange)
	must("ImportSync", err)

	// Each device sets a focus for the same day without the other's in sight.
	_, err = desk.SetFocus("Protect the first hour.")
	must("SetFocus", err)
	_, err = phone.SetFocus("Walk before writing.")
	must("SetFocus", err)
	_, err = phone.PublishSync(exchange)
	must("PublishSync", err)
	_, err = desk.ImportSync(exchange)
	must("ImportSync", err)

	var out strings.Builder
	must("showFocus", showFocus(&out, desk.ArchivePath(), "2026-08-25", "2026-08-25"))
	got := out.String()
	for _, want := range []string{"Focus disputed on 2026-08-25.", "- Protect the first hour.", "- Walk before writing.", "Set the focus again to settle it."} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in the report, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Focus (set") {
		t.Fatalf("a disputed focus was presented as settled:\n%s", got)
	}

	// Setting the focus again, with both in sight, settles it.
	_, err = desk.SetFocus("Walk, then protect the first hour.")
	must("SetFocus", err)
	out.Reset()
	must("showFocus", showFocus(&out, desk.ArchivePath(), "2026-08-25", "2026-08-25"))
	if got := out.String(); !strings.Contains(got, "Focus (set 2026-08-24") || !strings.Contains(got, "Walk, then protect the first hour.") {
		t.Fatalf("expected the focus to be settled by the later change, got:\n%s", got)
	}
}
