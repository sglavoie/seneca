package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/seneca/engine"
)

// The exchange in these tests is a directory made to look the way iCloud
// Drive leaves one: a file another device published is listed as a
// `.<name>.icloud` placeholder until its bytes arrive. The program that asks
// iCloud for a file is a stub that logs what it was asked for.

// cloudStub installs the stub and returns where it logs.
func (d *desk) cloudStub() string {
	d.t.Helper()
	root := filepath.Dir(d.home)
	log := filepath.Join(root, "brctl.log")
	stub := filepath.Join(root, "brctl-stub")
	d.write(stub, "#!/bin/sh\necho \"$@\" >> \""+log+"\"\n")
	if err := os.Chmod(stub, 0755); err != nil {
		d.t.Fatal(err)
	}
	d.env = append(d.env, "SENECA_BRCTL="+stub)
	return log
}

// exchangeFiles lists the files of the exchange under one of its directories.
func (d *desk) exchangeFiles(under string) []string {
	d.t.Helper()
	var paths []string
	err := filepath.WalkDir(filepath.Join(d.exchange, under), func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			paths = append(paths, path)
		}
		return err
	})
	if err != nil {
		d.t.Fatal(err)
	}
	return paths
}

func placeholderFor(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".icloud")
}

// evict leaves a placeholder where a file was, as a file that is in iCloud
// and not on this Mac.
func (d *desk) evict(path string) {
	d.t.Helper()
	if err := os.Rename(path, placeholderFor(path)); err != nil {
		d.t.Fatal(err)
	}
}

func (d *desk) arrive(path string) {
	d.t.Helper()
	if err := os.Rename(placeholderFor(path), path); err != nil {
		d.t.Fatal(err)
	}
}

// newFiles are the files of the exchange that were not there before.
func newFiles(before, after []string) []string {
	had := map[string]bool{}
	for _, path := range before {
		had[path] = true
	}
	var added []string
	for _, path := range after {
		if !had[path] {
			added = append(added, path)
		}
	}
	return added
}

func TestAPlaceholderInTheExchangeIsAskedForAndImportedOnceItArrives(t *testing.T) {
	d, p := enrolled(t)
	log := d.cloudStub()

	manifests, objects := d.exchangeFiles("transactions"), d.exchangeFiles("objects")
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")
	manifest := newFiles(manifests, d.exchangeFiles("transactions"))
	contents := newFiles(objects, d.exchangeFiles("objects"))
	if len(manifest) != 1 || len(contents) != 1 {
		t.Fatalf("the phone published %v and %v, want one manifest and one object", manifest, contents)
	}
	before := d.read(d.reflections)

	// Listed, not downloaded: the manifest's contents first.
	d.evict(contents[0])
	out, errOut, err := d.run("sync", "once")
	if err != nil {
		t.Fatalf("sync once failed beside a placeholder: %v\n%s", err, errOut)
	}
	if !strings.Contains(errOut, "still downloading from iCloud") || !strings.Contains(errOut, "have not arrived") {
		t.Fatalf("sync once did not say what it waits for:\n%s\n%s", out, errOut)
	}
	if d.read(d.reflections) != before || d.count("seneca: sync import") != 0 {
		t.Fatal("a transaction whose contents are not here was applied")
	}
	if asked := d.read(log); !strings.Contains(asked, "download "+contents[0]) {
		t.Fatalf("iCloud was not asked for the file:\n%s", asked)
	}
	if status := d.must("sync", "status"); !strings.Contains(status, "still downloading from iCloud: 1 file") ||
		!strings.Contains(status, "not imported yet: 1") {
		t.Fatalf("status does not report the download:\n%s", status)
	}
	asked := d.read(log)
	d.must("sync", "status")
	if d.read(log) != asked {
		t.Fatal("a status report asked iCloud for a file")
	}

	// Then the manifest itself is the placeholder.
	d.arrive(contents[0])
	d.evict(manifest[0])
	if _, errOut, _ = d.run("sync", "once"); !strings.Contains(errOut, "still downloading from iCloud") {
		t.Fatalf("a placeholder manifest was not reported:\n%s", errOut)
	}
	if d.read(d.reflections) != before {
		t.Fatal("a transaction whose manifest is not here was applied")
	}
	if !strings.Contains(d.read(log), "download "+manifest[0]) {
		t.Fatalf("iCloud was not asked for the manifest:\n%s", d.read(log))
	}

	d.arrive(manifest[0])
	out = d.must("sync", "once")
	if !strings.Contains(out, "Applied 1 change") || !strings.Contains(d.read(d.reflections), "Hurried a friend.") {
		t.Fatalf("the arrived transaction was not applied:\n%s", out)
	}
	if d.count("seneca: sync import") != 1 {
		t.Fatalf("subjects = %v, want one sync import", d.subjects())
	}
}

func TestADescriptorStillDownloadingNeverStartsASecondJournal(t *testing.T) {
	d, p := enrolled(t)
	d.cloudStub()
	descriptor := filepath.Join(d.exchange, "journal.json")
	d.evict(descriptor)
	files := d.exchangeFiles("")

	if _, errOut, err := d.run("sync", "once"); err == nil || !strings.Contains(errOut+err.Error(), "descriptor is still downloading") {
		t.Fatalf("sync once beside a placeholder descriptor: %v\n%s", err, errOut)
	}
	if got := d.exchangeFiles(""); !equal(got, files) {
		t.Fatalf("the exchange was written while its descriptor was away:\n%v", newFiles(files, got))
	}

	// A second desktop pointed at the same folder is not told it is empty.
	other := newDesk(t)
	other.exchange = d.exchange
	for i, pair := range other.env {
		if strings.HasPrefix(pair, "SENECA_SYNC_EXCHANGE=") {
			other.env[i] = "SENECA_SYNC_EXCHANGE=" + d.exchange
		}
	}
	if _, errOut, err := other.run("sync", "enroll"); err == nil {
		t.Fatalf("a second journal was enrolled into the folder:\n%s", errOut)
	}
	if got := d.exchangeFiles(""); !equal(got, files) {
		t.Fatalf("enrolling beside a placeholder descriptor wrote:\n%v", newFiles(files, got))
	}

	d.arrive(descriptor)
	p.reflect("2026-01-03", "Later.", "", "")
	if out := d.must("sync", "once"); !strings.Contains(out, "Applied 1 change") {
		t.Fatalf("the exchange did not resume:\n%s", out)
	}
}

// The phone learns its change arrived from the desktop's receipt, which the
// desktop writes only once the change is in its Markdown and in Git.
func TestTheDesktopsReceiptFollowsItsMarkdownAndItsCommit(t *testing.T) {
	d, p := enrolled(t)
	applied := func() (int, int) {
		t.Helper()
		delivery, err := p.journal().SyncDelivery(p.transport())
		if err != nil {
			t.Fatalf("phone: delivery: %v", err)
		}
		for _, peer := range delivery.Peers {
			if peer.Name == "desk" {
				return peer.Applied, delivery.Recorded
			}
		}
		return 0, delivery.Recorded
	}

	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")
	if got, recorded := applied(); got != 0 || recorded != 1 {
		t.Fatalf("before the desktop exchanged: applied %d of %d", got, recorded)
	}

	// Git refuses the commit: the Markdown has the entry, the receipt waits.
	lock := filepath.Join(d.repo, ".git", "index.lock")
	d.write(lock, "")
	_, errOut, _ := d.run("sync", "once")
	if !strings.Contains(d.read(d.reflections), "Hurried a friend.") || !strings.Contains(errOut, "will be retried") {
		t.Fatalf("the import did not land or the git failure was not reported:\n%s", errOut)
	}
	if got, _ := applied(); got != 0 {
		t.Fatalf("the desktop confirmed %d changes it has not committed", got)
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	d.must("sync", "once")
	if d.count("seneca: sync import") != 1 {
		t.Fatalf("subjects = %v", d.subjects())
	}
	if got, recorded := applied(); got != 1 || recorded != 1 {
		t.Fatalf("after the commit: applied %d of %d, want all", got, recorded)
	}

	// And the desktop sees the same of its own changes once the phone says so.
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Listened.", "Listened closely.", 1))
	d.must("sync", "once")
	if status := d.must("sync", "status"); strings.Contains(status, "applied on \"phone\"") {
		t.Fatalf("status names a receipt the phone has not written:\n%s", status)
	}
	p.pull()
	if _, err := p.journal().PublishSyncReceipt(p.transport()); err != nil {
		t.Fatal(err)
	}
	status := d.must("sync", "status")
	local, _, err := engine.LoadSyncStatus(d.reflections)
	if err != nil {
		t.Fatal(err)
	}
	want := "applied on \"phone\": " + itoa(local.Sequence) + " of " + itoa(local.Sequence) + " recorded here"
	if !strings.Contains(status, want) {
		t.Fatalf("status = \n%s\nwant a line %q", status, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for ; n > 0; n /= 10 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
	}
	return string(digits)
}
