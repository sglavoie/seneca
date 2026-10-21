package senecabridge

import (
	"path/filepath"
	"sort"

	"github.com/sglavoie/seneca/engine"
)

// --- sync status, enrollment, exchange ---------------------------------------

type syncPending struct {
	Transaction string `json:"transaction"`
	Reason      string `json:"reason"`
	Artifact    string `json:"artifact"`
	Detail      string `json:"detail"`
}

type syncSuspended struct {
	// File is the journal file not being read: reflections, archive, settings.
	File   string `json:"file"`
	Reason string `json:"reason"`
}

type syncStatusResult struct {
	// Enrolled is false for a journal that does not take part in sync; every
	// other field is then empty.
	Enrolled bool   `json:"enrolled"`
	Journal  string `json:"journal"`
	Device   string `json:"device"`
	Name     string `json:"name"`
	Genesis  string `json:"genesis"`
	Sequence int    `json:"sequence"`
	// Heads are the newest transactions of the history applied here.
	Heads     []string        `json:"heads"`
	Pending   []syncPending   `json:"pending"`
	Suspended []syncSuspended `json:"suspended"`
	// Conflicts names each artifact that waits for the author to choose, and
	// DisputedFocus each day whose focus is disputed. conflicts.list has both
	// in full.
	Conflicts     []string `json:"conflicts"`
	DisputedFocus []string `json:"disputed_focus"`
}

func syncStatusOf(status engine.SyncStatus, enrolled bool) syncStatusResult {
	view := syncStatusResult{
		Enrolled:      enrolled,
		Journal:       status.Journal,
		Device:        status.Device,
		Name:          status.Name,
		Genesis:       status.Genesis,
		Sequence:      status.Sequence,
		Heads:         nonNil(status.Heads),
		Pending:       pendingOf(status.Pending),
		Suspended:     []syncSuspended{},
		Conflicts:     nonNil(status.Conflicts),
		DisputedFocus: nonNil(status.DisputedFocus),
	}
	for file, reason := range status.Suspended {
		view.Suspended = append(view.Suspended, syncSuspended{File: file, Reason: reason})
	}
	sort.Slice(view.Suspended, func(i, j int) bool { return view.Suspended[i].File < view.Suspended[j].File })
	return view
}

func pendingOf(pending []engine.SyncPending) []syncPending {
	views := make([]syncPending, 0, len(pending))
	for _, p := range pending {
		views = append(views, syncPending{Transaction: p.Transaction, Reason: string(p.Reason), Artifact: p.Artifact, Detail: p.Detail})
	}
	return views
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func syncStatus(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	status, enrolled, err := c.journal.SyncStatus()
	if err != nil {
		return nil, err
	}
	return syncStatusOf(status, enrolled), nil
}

type enrollParams struct {
	// Name is a label for this device, for a person to read.
	Name string `json:"name"`
}

func syncEnroll(c *call) (any, error) {
	p, err := params[enrollParams](c)
	if err != nil {
		return nil, err
	}
	status, err := c.journal.EnrollSync(p.Name)
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return syncStatusOf(status, true), err
}

type exchangeParams struct {
	// ExchangePath is the directory both devices exchange through, absolute.
	ExchangePath string `json:"exchange_path"`
}

type joinParams struct {
	ExchangePath string `json:"exchange_path"`
	Name         string `json:"name"`
}

func transportAt(path string) (*engine.DirectoryTransport, error) {
	if !filepath.IsAbs(path) {
		return nil, refuse("invalid-params", "exchange_path must be an absolute path")
	}
	return engine.NewDirectoryTransport(filepath.Clean(path)), nil
}

func syncJoin(c *call) (any, error) {
	p, err := params[joinParams](c)
	if err != nil {
		return nil, err
	}
	transport, err := transportAt(p.ExchangePath)
	if err != nil {
		return nil, err
	}
	status, err := c.journal.JoinSync(transport, p.Name)
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return syncStatusOf(status, true), err
}

type syncDiagnostic struct {
	Code        string `json:"code"`
	Transaction string `json:"transaction"`
	Subject     string `json:"subject"`
	Detail      string `json:"detail"`
	// Message is the diagnostic as `seneca sync` prints it.
	Message string `json:"message"`
}

type syncImported struct {
	Applied  []string `json:"applied"`
	Recorded []string `json:"recorded"`
	// Written names the journal files the import replaced: reflections,
	// archive, settings.
	Written     []string         `json:"written"`
	Merged      []string         `json:"merged"`
	Conflicts   []string         `json:"conflicts"`
	Pending     []syncPending    `json:"pending"`
	Diagnostics []syncDiagnostic `json:"diagnostics"`
}

func syncImport(c *call) (any, error) {
	p, err := params[exchangeParams](c)
	if err != nil {
		return nil, err
	}
	transport, err := transportAt(p.ExchangePath)
	if err != nil {
		return nil, err
	}
	imported, err := c.journal.ImportSync(transport)
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	view := syncImported{
		Applied:     nonNil(imported.Applied),
		Recorded:    nonNil(imported.Recorded),
		Written:     fileNames(c.journal, imported.Written),
		Merged:      nonNil(imported.Merged),
		Conflicts:   nonNil(imported.Conflicts),
		Pending:     pendingOf(imported.Pending),
		Diagnostics: make([]syncDiagnostic, 0, len(imported.Diagnostics)),
	}
	for _, d := range imported.Diagnostics {
		view.Diagnostics = append(view.Diagnostics, syncDiagnostic{
			Code: string(d.Code), Transaction: d.Transaction, Subject: d.Subject, Detail: d.Detail, Message: d.String(),
		})
	}
	return view, err
}

// fileNames names journal files by what they are instead of where they are.
func fileNames(journal *engine.Journal, paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		switch path {
		case journal.ReflectionsPath():
			names = append(names, "reflections")
		case journal.ArchivePath():
			names = append(names, "archive")
		case engine.SettingsPathFor(journal.ReflectionsPath()):
			names = append(names, "settings")
		default:
			names = append(names, filepath.Base(path))
		}
	}
	return names
}

type syncPublished struct {
	Transactions []string `json:"transactions"`
	// Receipt names the receipt that says what this device has applied, and
	// ReceiptWritten whether this call put it in the exchange.
	Receipt        string `json:"receipt"`
	ReceiptWritten bool   `json:"receipt_written"`
}

// syncExport sends this device's transactions and then its receipt. On the
// phone nothing stands between an import and its journal, so what the state
// says is applied is applied.
func syncExport(c *call) (any, error) {
	p, err := params[exchangeParams](c)
	if err != nil {
		return nil, err
	}
	transport, err := transportAt(p.ExchangePath)
	if err != nil {
		return nil, err
	}
	published, err := c.journal.PublishSync(transport)
	if err != nil {
		return nil, err
	}
	receipt, err := c.journal.PublishSyncReceipt(transport)
	if err != nil {
		return nil, err
	}
	return syncPublished{
		Transactions: nonNil(published.Transactions), Receipt: receipt.ID, ReceiptWritten: receipt.Written,
	}, nil
}

type syncPeer struct {
	Device string `json:"device"`
	// Name is empty until the device has published a receipt.
	Name string `json:"name"`
	// Applied is the sequence up to which this device's transactions are all
	// applied on that one, by its own receipt.
	Applied int `json:"applied"`
}

type syncDeliveryResult struct {
	// Recorded counts this device's transactions: all are saved here.
	Recorded int `json:"recorded"`
	// Published is the sequence up to which they are all in the exchange.
	Published int        `json:"published"`
	Peers     []syncPeer `json:"peers"`
}

func syncDelivery(c *call) (any, error) {
	p, err := params[exchangeParams](c)
	if err != nil {
		return nil, err
	}
	transport, err := transportAt(p.ExchangePath)
	if err != nil {
		return nil, err
	}
	delivery, err := c.journal.SyncDelivery(transport)
	if err != nil {
		return nil, err
	}
	view := syncDeliveryResult{Recorded: delivery.Recorded, Published: delivery.Published, Peers: make([]syncPeer, 0, len(delivery.Peers))}
	for _, peer := range delivery.Peers {
		view.Peers = append(view.Peers, syncPeer{Device: peer.Device, Name: peer.Name, Applied: peer.Applied})
	}
	return view, nil
}

type recoverParams struct {
	// File is reflections, archive, or settings.
	File string `json:"file"`
	// Action is restore or accept-deletions.
	Action string `json:"action"`
}

type syncRecovered struct {
	Restored []string `json:"restored"`
	Deleted  []string `json:"deleted"`
	Written  []string `json:"written"`
}

func syncRecover(c *call) (any, error) {
	p, err := params[recoverParams](c)
	if err != nil {
		return nil, err
	}
	action := engine.SyncRecovery(p.Action)
	if action != engine.SyncRecoverRestore && action != engine.SyncRecoverAcceptDeletions {
		return nil, refuse("invalid-params", "action must be %s or %s", engine.SyncRecoverRestore, engine.SyncRecoverAcceptDeletions)
	}
	recovered, err := c.journal.RecoverSyncFile(p.File, action)
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return syncRecovered{
		Restored: nonNil(recovered.Restored),
		Deleted:  nonNil(recovered.Deleted),
		Written:  fileNames(c.journal, recovered.Written),
	}, err
}

// --- conflicts ---------------------------------------------------------------

type conflictVersion struct {
	// Revision is "absent" for a deletion, or for a base that did not exist.
	Revision    string `json:"revision"`
	Transaction string `json:"transaction"`
	Device      string `json:"device"`
	Deleted     bool   `json:"deleted"`
	// Content is the version's bytes, empty for a deletion.
	Content string `json:"content"`
	// Lines is Content as an entry is displayed: without Seneca's markers.
	Lines []string `json:"lines"`
}

type syncConflict struct {
	// ID names the conflict by the versions competing in it; conflict.resolve
	// is conditional on it.
	ID       string            `json:"id"`
	Artifact string            `json:"artifact"`
	Date     string            `json:"date"`
	Reason   string            `json:"reason"`
	Base     conflictVersion   `json:"base"`
	Ours     conflictVersion   `json:"ours"`
	Theirs   []conflictVersion `json:"theirs"`
}

type focusDispute struct {
	// ID names the dispute by the changes competing in it.
	ID         string           `json:"id"`
	Effective  string           `json:"effective"`
	Candidates []focusCandidate `json:"candidates"`
}

type conflictsResult struct {
	Conflicts     []syncConflict `json:"conflicts"`
	FocusDisputes []focusDispute `json:"focus_disputes"`
}

func conflictVersionOf(version engine.SyncConflictVersion) conflictVersion {
	view := conflictVersion{
		Revision:    string(version.Revision),
		Transaction: version.Transaction,
		Device:      version.Device,
		Deleted:     version.Deleted(),
		Content:     version.Content,
		Lines:       []string{},
	}
	if version.Content != "" {
		view.Lines = engine.VisibleEntryLines(version.Content)
	}
	return view
}

func listConflicts(c *call) (any, error) {
	if _, err := params[struct{}](c); err != nil {
		return nil, err
	}
	result := conflictsResult{Conflicts: []syncConflict{}, FocusDisputes: []focusDispute{}}
	if _, enrolled, err := c.journal.SyncStatus(); err != nil {
		return nil, err
	} else if !enrolled {
		// A journal that does not take part in sync has nothing to settle.
		return result, nil
	}
	conflicts, err := c.journal.SyncConflicts()
	if err != nil {
		return nil, err
	}
	for _, conflict := range conflicts {
		view := syncConflict{
			ID: conflict.ID, Artifact: conflict.Artifact, Date: conflict.Date, Reason: conflict.Reason,
			Base: conflictVersionOf(conflict.Base), Ours: conflictVersionOf(conflict.Ours),
			Theirs: make([]conflictVersion, 0, len(conflict.Theirs)),
		}
		for _, theirs := range conflict.Theirs {
			view.Theirs = append(view.Theirs, conflictVersionOf(theirs))
		}
		result.Conflicts = append(result.Conflicts, view)
	}
	disputes, err := c.journal.FocusDisputes()
	if err != nil {
		return nil, err
	}
	for _, dispute := range disputes {
		result.FocusDisputes = append(result.FocusDisputes, focusDispute{
			ID: dispute.ID, Effective: dispute.Effective, Candidates: focusCandidatesOf(dispute.Candidates),
		})
	}
	return result, nil
}

type resolveConflictParams struct {
	// ID is the conflict as conflicts.list named it.
	ID string `json:"id"`
	// Keep is the revision to keep: one of the conflict's versions, or
	// "absent" to keep a deletion that competes in it.
	Keep string `json:"keep,omitempty"`
	// Content is what to write instead of any of them, used when Keep is left
	// out.
	Content *string `json:"content,omitempty"`
}

type syncResolved struct {
	Artifact    string   `json:"artifact"`
	Revision    string   `json:"revision"`
	Transaction string   `json:"transaction"`
	Written     []string `json:"written"`
}

func resolveConflict(c *call) (any, error) {
	p, err := params[resolveConflictParams](c)
	if err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, refuse("invalid-params", "id must name the conflict being answered")
	}
	if (p.Keep == "") == (p.Content == nil) {
		return nil, refuse("invalid-params", "answer a conflict with exactly one of keep and content")
	}
	resolution := engine.SyncResolution{Keep: engine.Revision(p.Keep)}
	if p.Content != nil {
		resolution.Content = *p.Content
	}
	resolved, err := c.journal.ResolveSyncConflict(p.ID, resolution)
	if err != nil && !engine.WriteCommitted(err) {
		return nil, err
	}
	return syncResolved{
		Artifact:    resolved.Artifact,
		Revision:    string(resolved.Revision),
		Transaction: resolved.Transaction,
		Written:     fileNames(c.journal, resolved.Written),
	}, err
}

type resolveDisputeParams struct {
	// ID is the dispute as conflicts.list named it.
	ID string `json:"id"`
	// Record keeps what one of the competing changes said. Text sets a focus
	// of the author's own instead. With neither, the day's focus is cleared.
	Record string `json:"record,omitempty"`
	Text   string `json:"text,omitempty"`
}

func resolveFocusDispute(c *call) (any, error) {
	p, err := params[resolveDisputeParams](c)
	if err != nil {
		return nil, err
	}
	if p.ID == "" {
		return nil, refuse("invalid-params", "id must name the dispute being answered")
	}
	if p.Record != "" && p.Text != "" {
		return nil, refuse("invalid-params", "answer a disputed focus with at most one of record and text")
	}
	change, err := c.journal.ResolveFocusDispute(p.ID, engine.FocusResolution{Record: p.Record, Text: p.Text})
	return focusChangeOf(c, change, err)
}
