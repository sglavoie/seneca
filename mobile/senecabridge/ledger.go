package senecabridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sglavoie/seneca/engine"
)

// The request ledger is what makes a repeated request harmless. The app sends
// a mutation again when it never saw the answer: it was suspended, the screen
// went away, the call was retried. The ledger holds the answer each carried-out
// mutation was given, by request identity, and gives it again instead of
// carrying the request out a second time.
//
// It belongs to the installation, not to the journal: it lives in the app's
// own state directory and is never exchanged.

const (
	ledgerFileName = "requests.json"
	ledgerVersion  = 1
	// ledgerKeep bounds the ledger. A request is repeated within moments of
	// the original or not at all.
	ledgerKeep = 256
)

type ledgerEntry struct {
	RequestID string `json:"request_id"`
	Operation string `json:"operation"`
	// Digest names the request's operation and parameters, so an identity
	// reused for something else is refused instead of answered. It does not
	// name where the journal is: an app's container moves when a new build is
	// installed, and the request is still the same request.
	Digest  string          `json:"digest"`
	Result  json.RawMessage `json:"result"`
	Warning *problem        `json:"warning,omitempty"`
}

type ledgerDocument struct {
	Version int           `json:"version"`
	Entries []ledgerEntry `json:"entries"`
}

type requestLedger struct {
	path    string
	entries []ledgerEntry
}

// loadLedger reads the ledger. One that is missing or unreadable is an empty
// one: the preconditions every mutation carries still stand behind it.
func loadLedger(stateDir string) *requestLedger {
	ledger := &requestLedger{path: filepath.Join(stateDir, ledgerFileName)}
	data, err := os.ReadFile(ledger.path)
	if err != nil {
		return ledger
	}
	var doc ledgerDocument
	if decodeStrict(data, &doc) != nil || doc.Version != ledgerVersion {
		return ledger
	}
	ledger.entries = doc.Entries
	return ledger
}

func (l *requestLedger) find(requestID string) (ledgerEntry, bool) {
	for _, entry := range l.entries {
		if entry.RequestID == requestID {
			return entry, true
		}
	}
	return ledgerEntry{}, false
}

func (l *requestLedger) record(entry ledgerEntry) {
	l.entries = append(l.entries, entry)
	if extra := len(l.entries) - ledgerKeep; extra > 0 {
		l.entries = l.entries[extra:]
	}
	data, err := json.Marshal(ledgerDocument{Version: ledgerVersion, Entries: l.entries})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	_ = engine.DefaultFS().AtomicWriteFile(l.path, append(data, '\n'), 0o600)
}

func requestDigest(operation string, params json.RawMessage) string {
	// Parameters are compared as values, not as the bytes they arrived in.
	var value any
	canonical := []byte(params)
	if json.Unmarshal(params, &value) == nil {
		if encoded, err := json.Marshal(value); err == nil {
			canonical = encoded
		}
	}
	hash := sha256.New()
	for _, part := range [][]byte{[]byte(operation), canonical} {
		hash.Write(part)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
