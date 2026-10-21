// Package senecabridge is the only way the iPhone app reaches the engine: one
// JSON request in, one JSON response out. docs/mobile/bridge.md is its
// contract; this package is bound with gomobile into SenecaBridgeCore.
//
// Nothing here decides what a journal means. Every operation is a call into
// engine.Journal, the same one the desktop commands make, and the shapes
// returned are views over what the engine returned.
package senecabridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	// The phone's Go runtime has no zone database of its own to read.
	_ "time/tzdata"

	"github.com/sglavoie/seneca/engine"
)

// Version is the bridge version this engine speaks: one closed set of
// operations, shapes, and error codes. Adding to any of them makes a new one.
//
// Version 2 added sync.delivery, and the receipt sync.export publishes.
// Version 3 added the history queries of entries.list and entries.search, the
// period and revision links of reviews.list, conflicted days, and the
// conflicted refusal of reflection.save.
// Version 4 added reminders.plan, diagnostics.collect, recovery.export, and
// sync.leave, and the codes invalid-reminders, destination-exists, and
// sync-state-version.
// Version 5 added entry.delete.
// Version 6 added packs.list, packs.read, settings.apply_pack, the practice of
// the settings shapes, and the code invalid-pack.
// Version 7 added insights.get.
// Version 8 added share.packet.
const Version = 8

// BridgeVersion reports the highest bridge version the linked engine speaks.
func BridgeVersion() int {
	return Version
}

type request struct {
	Version   int             `json:"version"`
	Operation string          `json:"operation"`
	RequestID string          `json:"request_id,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type response struct {
	Version   int    `json:"version"`
	Operation string `json:"operation,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	OK        bool   `json:"ok"`
	// Replayed marks an answer given from the request ledger: the request had
	// already been carried out, and was not carried out again.
	Replayed bool            `json:"replayed,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	// Warning accompanies a result whose write reached the journal while a
	// later step failed. The change is there; it must not be sent again.
	Warning *problem `json:"warning,omitempty"`
	Error   *problem `json:"error,omitempty"`
}

// problem is a failure as data: a stable code, the message written for the
// author, and whether sending the same request again can help.
type problem struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Retryable bool              `json:"retryable"`
	Details   map[string]string `json:"details,omitempty"`
}

func (p *problem) Error() string { return p.Message }

func refuse(code, format string, args ...any) *problem {
	return &problem{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Bridge is one conversation with the engine: at most one open journal, and
// one request answered at a time. The app holds one for its journal; nothing
// is shared between two of them.
type Bridge struct {
	mu   sync.Mutex
	open *openJournal
	// now is the wall clock; tests replace it.
	now func() time.Time
	// fs is the filesystem journals write through: the real one, except in a
	// test that makes one stage of a write fail.
	fs engine.FS
}

// NewBridge returns a bridge with no journal open.
func NewBridge() *Bridge {
	return &Bridge{now: time.Now}
}

// Call answers one request. It never panics and never returns anything but a
// response document.
func (b *Bridge) Call(request string) string {
	return b.call(request)
}

// openJournal is what journal.open resolved. A fresh engine.Journal is opened
// from it for every request, with that request's time, and nothing is held
// between requests.
type openJournal struct {
	reflectionsPath string
	stateDir        string
	settings        engine.ReflectionSettings
	location        *time.Location
	fixed           *time.Time
}

type operation struct {
	// mutates marks an operation that changes the journal. It must carry a
	// request identity, and its answer is kept in the request ledger.
	mutates bool
	// unopened marks an operation that runs without an open journal.
	unopened bool
	run      func(c *call) (any, error)
}

// call is one request being answered.
type call struct {
	bridge  *Bridge
	open    *openJournal
	journal *engine.Journal
	params  json.RawMessage
}

// operations is the closed set of this bridge version.
var operations map[string]operation

func init() {
	operations = map[string]operation{
		"bridge.describe": {unopened: true, run: describeBridge},
		"journal.open":    {unopened: true, run: openJournalOp},
		"journal.close":   {unopened: true, run: closeJournal},
		"journal.status":  {run: journalStatus},

		"entries.list":   {run: listEntries},
		"entries.search": {run: searchEntries},
		"entry.get":      {run: getEntry},
		"entry.delete":   {mutates: true, run: deleteEntry},

		"reflection.prepare": {run: prepareReflection},
		"reflection.save":    {mutates: true, run: saveReflection},
		"draft.get":          {run: getDraft},
		"draft.retain":       {run: retainDraft},
		"draft.discard":      {run: discardDraft},

		"settings.get":        {run: getSettings},
		"settings.initialize": {mutates: true, run: initializeSettings},
		"settings.update":     {mutates: true, run: updateSettings},
		"settings.apply_pack": {mutates: true, run: applyPack},

		"packs.list": {unopened: true, run: listPacks},
		"packs.read": {unopened: true, run: readPack},

		"insights.get": {run: getInsights},
		"share.packet": {run: sharePacket},

		"reviews.list": {run: listReviews},
		"review.get":   {run: getReview},

		"focus.get":   {run: getFocus},
		"focus.set":   {mutates: true, run: setFocus},
		"focus.clear": {mutates: true, run: clearFocus},

		"sync.status":   {run: syncStatus},
		"sync.enroll":   {mutates: true, run: syncEnroll},
		"sync.join":     {mutates: true, run: syncJoin},
		"sync.import":   {mutates: true, run: syncImport},
		"sync.export":   {run: syncExport},
		"sync.delivery": {run: syncDelivery},
		"sync.recover":  {mutates: true, run: syncRecover},
		"sync.leave":    {mutates: true, run: syncLeave},

		"conflicts.list":        {run: listConflicts},
		"conflict.resolve":      {mutates: true, run: resolveConflict},
		"focus_dispute.resolve": {mutates: true, run: resolveFocusDispute},

		"reminders.plan":      {run: planReminders},
		"diagnostics.collect": {run: collectDiagnostics},
		"recovery.export":     {run: exportRecovery},
	}
}

// operationNames lists the operations of this bridge version, sorted.
func operationNames() []string {
	names := make([]string, 0, len(operations))
	for name := range operations {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (b *Bridge) call(raw string) (out string) {
	var req request
	respond := func(resp response) string {
		resp.Version = Version
		resp.Operation = req.Operation
		resp.RequestID = req.RequestID
		data, err := json.Marshal(resp)
		if err != nil {
			return fmt.Sprintf(`{"version":%d,"ok":false,"error":{"code":"internal","message":"the response could not be encoded","retryable":false}}`, Version)
		}
		return string(data)
	}
	// The engine never panics across the boundary.
	defer func() {
		if r := recover(); r != nil {
			out = respond(response{Error: refuse("internal", "the engine failed unexpectedly: %v", r)})
		}
	}()

	if err := decodeStrict([]byte(raw), &req); err != nil {
		// Whatever could be read of the envelope is still echoed.
		var loose request
		_ = json.Unmarshal([]byte(raw), &loose)
		req.Operation, req.RequestID = loose.Operation, loose.RequestID
		return respond(response{Error: refuse("malformed-request", "the request is not a bridge request: %v", err)})
	}
	if req.Version != Version {
		return respond(response{Error: refuse("unsupported-version",
			"the request was written for bridge version %d; this engine speaks version %d", req.Version, Version)})
	}
	op, ok := operations[req.Operation]
	if !ok {
		return respond(response{Error: refuse("unknown-operation", "bridge version %d has no operation %q", Version, req.Operation)})
	}
	if op.mutates && req.RequestID == "" {
		return respond(response{Error: refuse("missing-request-id", "%s changes the journal and must carry a request_id", req.Operation)})
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	c := &call{bridge: b, open: b.open, params: req.Params}
	if !op.unopened {
		if b.open == nil {
			return respond(response{Error: refuse("journal-not-open", "no journal is open; send journal.open first")})
		}
		journal, err := b.open.journal(b)
		if err != nil {
			return respond(response{Error: describeError(err)})
		}
		c.journal = journal
	}

	var ledger *requestLedger
	var digest string
	if op.mutates {
		ledger = loadLedger(b.open.stateDir)
		digest = requestDigest(req.Operation, req.Params)
		if kept, found := ledger.find(req.RequestID); found {
			if kept.Digest != digest {
				return respond(response{Error: refuse("request-id-reused",
					"request_id %s was already used for a different %s request", req.RequestID, kept.Operation)})
			}
			return respond(response{OK: true, Replayed: true, Result: kept.Result, Warning: kept.Warning})
		}
	}

	result, err := op.run(c)
	var warning *problem
	if err != nil {
		// A write that landed is answered as one, with what it wrote.
		if !engine.WriteCommitted(err) {
			return respond(response{Error: describeError(err)})
		}
		warning = describeError(err)
	}
	encoded, encErr := json.Marshal(result)
	if encErr != nil {
		return respond(response{Error: refuse("internal", "the result could not be encoded: %v", encErr)})
	}
	if op.mutates {
		// Recording is best effort: every mutation is also conditional on the
		// state it was computed against, so a request repeated after a lost
		// record is refused as stale rather than carried out twice.
		ledger.record(ledgerEntry{
			RequestID: req.RequestID, Operation: req.Operation, Digest: digest,
			Result: encoded, Warning: warning,
		})
	}
	return respond(response{OK: true, Result: encoded, Warning: warning})
}

func (o *openJournal) clock(wall func() time.Time) func() time.Time {
	if o.fixed != nil {
		fixed := *o.fixed
		return func() time.Time { return fixed }
	}
	return func() time.Time { return wall().In(o.location) }
}

func (o *openJournal) journal(b *Bridge) (*engine.Journal, error) {
	// One instant per request, as one instant per desktop command.
	now := o.clock(b.now)()
	return engine.Open(engine.Options{
		ReflectionsPath: o.reflectionsPath,
		Settings:        o.settings,
		Clock:           func() time.Time { return now },
		FS:              b.fs,
	})
}

// decodeStrict reads exactly one JSON value with no field the bridge does not
// know.
func decodeStrict(data []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// params reads an operation's parameters strictly. No parameters at all is
// the zero value.
func params[T any](c *call) (T, error) {
	var value T
	if len(bytes.TrimSpace(c.params)) == 0 {
		return value, nil
	}
	if err := decodeStrict(c.params, &value); err != nil {
		return value, refuse("invalid-params", "the parameters are not what this operation takes: %v", err)
	}
	return value, nil
}
