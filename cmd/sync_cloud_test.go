package cmd

import (
	"errors"
	"github.com/sglavoie/seneca/engine"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExchangeFile(t *testing.T, root, name string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file whose bytes are in the cloud is never read: to the engine it has not
// arrived, whichever of the two forms macOS gives it.
func TestACloudOnlyFileHasNotArrived(t *testing.T) {
	root := t.TempDir()
	device := strings.Repeat("a", 32)
	here := engine.SyncTransactionRef{Device: device, Sequence: 1, ID: strings.Repeat("1", 64)}
	dataless := engine.SyncTransactionRef{Device: device, Sequence: 2, ID: strings.Repeat("2", 64)}
	placeholder := engine.SyncTransactionRef{Device: device, Sequence: 3, ID: strings.Repeat("3", 64)}
	object := engine.Revision("sha256:" + strings.Repeat("4", 64))
	receipt := engine.SyncReceiptRef{Device: device, ID: strings.Repeat("5", 64)}

	plain := engine.NewDirectoryTransport(root)
	writeExchangeFile(t, root, "journal.json")
	writeExchangeFile(t, root, "notes/unrelated.txt")
	writeExchangeFile(t, root, ".DS_Store")
	writeExchangeFile(t, root, filepath.ToSlash(mustRel(t, root, plain.ManifestPath(here))))
	datalessPath := writeExchangeFile(t, root, filepath.ToSlash(mustRel(t, root, plain.ManifestPath(dataless))))
	held := plain.ManifestPath(placeholder)
	writeExchangeFile(t, root, filepath.ToSlash(mustRel(t, root, filepath.Join(filepath.Dir(held), "."+filepath.Base(held)+".icloud"))))
	objectPath := writeExchangeFile(t, root, filepath.ToSlash(mustRel(t, root, plain.ObjectPath(object))))
	receiptPath := writeExchangeFile(t, root, filepath.ToSlash(mustRel(t, root, plain.ReceiptPath(receipt))))

	original := cloudDataless
	cloudDataless = func(info fs.FileInfo) bool {
		return info.Name() == filepath.Base(datalessPath) || info.Name() == filepath.Base(objectPath) || info.Name() == filepath.Base(receiptPath)
	}
	t.Cleanup(func() { cloudDataless = original })

	exchange := newCloudExchange(root)
	if got := exchange.downloading(); got != 4 {
		t.Fatalf("downloading = %d (%v), want the dataless manifest, object and receipt and the placeholder", got, exchange.waiting)
	}
	refs, err := exchange.Transactions()
	if err != nil || len(refs) != 3 || refs[0] != here || refs[1] != dataless || refs[2] != placeholder {
		t.Fatalf("Transactions = %v (%v), want all three by name", refs, err)
	}
	if _, err := exchange.Transaction(here); err != nil {
		t.Fatalf("a local manifest: %v", err)
	}
	for name, err := range map[string]error{
		"dataless manifest":    second(exchange.Transaction(dataless)),
		"placeholder manifest": second(exchange.Transaction(placeholder)),
		"dataless object":      second(exchange.Object(object)),
		"dataless receipt":     second(exchange.Receipt(receipt)),
	} {
		if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "downloading from iCloud") {
			t.Errorf("%s: error = %v, want one that says it has not arrived", name, err)
		}
	}

	// The descriptor away is an error, never an empty exchange.
	if err := os.Rename(filepath.Join(root, "journal.json"), filepath.Join(root, ".journal.json.icloud")); err != nil {
		t.Fatal(err)
	}
	if _, err := newCloudExchange(root).Descriptor(); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Descriptor beside its placeholder: %v, want an error that is not absence", err)
	}
	if _, err := newCloudExchange(t.TempDir()).Descriptor(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Descriptor of an empty exchange: %v, want absence", err)
	}
}

func second(_ []byte, err error) error { return err }

func mustRel(t *testing.T, root, path string) string {
	t.Helper()
	relative, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatal(err)
	}
	return relative
}

func TestAskingForCloudFilesIsBoundedAndSurvivesAMissingProgram(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < cloudRequestLimit+5; i++ {
		digest := strings.Repeat("0", 61) + string(rune('a'+i/26/26%26)) + string(rune('a'+i/26%26)) + string(rune('a'+i%26))
		writeExchangeFile(t, root, "objects/."+digest+".icloud")
	}
	log := filepath.Join(t.TempDir(), "asked.log")
	stub := filepath.Join(t.TempDir(), "brctl")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\n"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv(brctlEnv, stub)
	if got := newCloudExchange(root).request(); got != cloudRequestLimit+5 {
		t.Fatalf("request reported %d waiting, want all %d", got, cloudRequestLimit+5)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != cloudRequestLimit {
		t.Fatalf("asked for %d files in one exchange, want at most %d", len(lines), cloudRequestLimit)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "download "+filepath.Join(root, "objects")+string(filepath.Separator)) || strings.Contains(line, ".icloud") {
			t.Fatalf("asked for %q, want the file by its real name", line)
		}
	}

	t.Setenv(brctlEnv, filepath.Join(t.TempDir(), "no-such-program"))
	if got := newCloudExchange(root).request(); got != cloudRequestLimit+5 {
		t.Fatalf("without the program request reported %d, want the files still counted", got)
	}
}
