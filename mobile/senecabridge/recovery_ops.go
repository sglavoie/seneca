package senecabridge

import (
	"path/filepath"
	"time"

	"github.com/sglavoie/seneca/engine"
)

// --- reminders.plan ----------------------------------------------------------

type remindersParams struct {
	// Times are the evening's reminder times, HH:MM. Left out, they are the
	// desktop's schedule.
	Times []string `json:"times,omitempty"`
	// Days is how many days to plan, from today. Left out, a week.
	Days int `json:"days,omitempty"`
}

type plannedReminder struct {
	Date string `json:"date"`
	Time string `json:"time"`
	// At is the instant it fires, RFC 3339, in the journal's zone.
	At string `json:"at"`
}

type reminderPlan struct {
	Today string `json:"today"`
	// TimeZone is the zone the plan was made in: a new one needs a new plan.
	TimeZone  string            `json:"time_zone"`
	Times     []string          `json:"times"`
	Defaults  []string          `json:"defaults"`
	Complete  []string          `json:"complete"`
	Reminders []plannedReminder `json:"reminders"`
}

const defaultReminderDays = 7

// planReminders plans the evening reminders from the journal's now: every
// planned time of every day this device holds no entry for.
func planReminders(c *call) (any, error) {
	p, err := params[remindersParams](c)
	if err != nil {
		return nil, err
	}
	if p.Days == 0 {
		p.Days = defaultReminderDays
	}
	entries, err := c.journal.Entries()
	if err != nil {
		return nil, err
	}
	now := c.journal.Now()
	plan, err := engine.PlanReminders(entries, now, p.Times, p.Days)
	if err != nil {
		return nil, err
	}
	view := reminderPlan{
		Today:     plan.Today,
		TimeZone:  now.Location().String(),
		Times:     plan.Times,
		Defaults:  append([]string{}, engine.DefaultReminderTimes...),
		Complete:  nonNil(plan.Complete),
		Reminders: make([]plannedReminder, 0, len(plan.Reminders)),
	}
	for _, reminder := range plan.Reminders {
		view.Reminders = append(view.Reminders, plannedReminder{
			Date: reminder.Date, Time: reminder.Time, At: reminder.At.Format(time.RFC3339),
		})
	}
	return view, nil
}

// --- diagnostics.collect -----------------------------------------------------

type diagnosticsParams struct {
	// ExchangePath is the exchange as this device holds it, absolute. Left
	// out, the exchange is not looked at.
	ExchangePath string `json:"exchange_path,omitempty"`
}

// collectDiagnostics reports on the journal in versions, counts, and codes,
// with nothing written in it. It changes nothing.
func collectDiagnostics(c *call) (any, error) {
	p, err := params[diagnosticsParams](c)
	if err != nil {
		return nil, err
	}
	var transport engine.SyncTransport
	if p.ExchangePath != "" {
		directory, err := transportAt(p.ExchangePath)
		if err != nil {
			return nil, err
		}
		transport = directory
	}
	return engine.CollectDiagnostics(c.journal.ReflectionsPath(), transport), nil
}

// --- recovery.export ---------------------------------------------------------

type recoveryParams struct {
	// Destination is a new directory, absolute. Its parent must exist.
	Destination string `json:"destination"`
	// DraftsDir is the app's own directory of unsaved writing, absolute, to
	// copy in as it is.
	DraftsDir string `json:"drafts_dir,omitempty"`
}

type recoveryFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	Digest string `json:"digest"`
	Kind   string `json:"kind"`
}

type recoveryResult struct {
	Directory string         `json:"directory"`
	Files     []recoveryFile `json:"files"`
	// ConflictVersions and Drafts count what the export holds besides the
	// journal's own files.
	ConflictVersions int `json:"conflict_versions"`
	Drafts           int `json:"drafts"`
}

func exportRecovery(c *call) (any, error) {
	p, err := params[recoveryParams](c)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(p.Destination) {
		return nil, refuse("invalid-params", "destination must be an absolute path")
	}
	if p.DraftsDir != "" && !filepath.IsAbs(p.DraftsDir) {
		return nil, refuse("invalid-params", "drafts_dir must be an absolute path")
	}
	export, err := c.journal.ExportRecovery(p.Destination, p.DraftsDir)
	if err != nil {
		return nil, err
	}
	view := recoveryResult{
		Directory:        export.Directory,
		Files:            make([]recoveryFile, 0, len(export.Files)),
		ConflictVersions: export.Count("conflict"),
		Drafts:           export.Count("draft") + export.Count("review-draft"),
	}
	for _, file := range export.Files {
		view.Files = append(view.Files, recoveryFile{Path: file.Path, Bytes: file.Bytes, Digest: string(file.Digest), Kind: file.Kind})
	}
	return view, nil
}

// --- sync.leave --------------------------------------------------------------

type syncLeft struct {
	Journal string `json:"journal"`
	// Device is the identity this device had. It is retired.
	Device string `json:"device"`
	// Kept is where the old history lies, beside the journal's metadata.
	Kept      string `json:"kept"`
	Conflicts int    `json:"conflicts"`
}

func syncLeave(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	left, err := c.journal.LeaveSync()
	if err != nil {
		return nil, err
	}
	return syncLeft{Journal: left.Journal, Device: left.Device, Kept: left.Kept, Conflicts: left.Conflicts}, nil
}
