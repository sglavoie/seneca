package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/seneca/engine"
)

// These tests run the real program as separate processes against a journal in
// a real Git repository, with a second device played by the engine alone: it
// has its own directory and identity and shares nothing with the desktop but
// the exchange directory, which is what a phone shares with it.
//
// Nothing here reaches the author's journal, home directory, Git
// configuration, or launchd: HOME, SENECA_FILE, the exchange, the global Git
// configuration, and the launchctl program are all the test's own.

var (
	senecaBuild   sync.Once
	senecaBinary  string
	senecaBuildOK error
	senecaBuilt   string
)

// builtSeneca compiles the program once for every test in the package.
func builtSeneca(t *testing.T) string {
	t.Helper()

	senecaBuild.Do(func() {
		dir, err := os.MkdirTemp("", "seneca-sync-test-")
		if err != nil {
			senecaBuildOK = err
			return
		}
		senecaBuilt = dir
		// The program names itself by its real path, so the test does too.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		senecaBinary = filepath.Join(dir, "seneca")
		if out, err := exec.Command("go", "build", "-o", senecaBinary, ".").CombinedOutput(); err != nil {
			senecaBuildOK = fmt.Errorf("building seneca: %v\n%s", err, out)
		}
	})
	if senecaBuildOK != nil {
		t.Fatal(senecaBuildOK)
	}
	return senecaBinary
}

func TestMain(m *testing.M) {
	code := m.Run()
	if senecaBuilt != "" {
		_ = os.RemoveAll(senecaBuilt)
	}
	os.Exit(code)
}

var syncQuestions = []string{"What did I do badly today?", "What did I do well today?", "What do I want to improve?"}

func seedEntry(date string, answers ...string) string {
	return engine.FormatReflectionEntry(date, syncQuestions, answers) + "---\n\n"
}

// desk is the desktop: a journal inside a Git repository, driven only through
// the seneca program.
type desk struct {
	t           *testing.T
	bin         string
	home        string
	repo        string
	dir         string
	reflections string
	archive     string
	exchange    string
	launchctl   string
	env         []string
}

func newDesk(t *testing.T) *desk {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}

	d := &desk{t: t, bin: builtSeneca(t)}
	// The paths a process resolves for itself are the real ones, so the test
	// names them the same way.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d.home = filepath.Join(root, "home")
	d.repo = filepath.Join(root, "repo")
	d.dir = filepath.Join(d.repo, "journal")
	d.exchange = filepath.Join(root, "exchange")
	d.reflections = filepath.Join(d.dir, "reflections.md")
	d.archive = filepath.Join(d.dir, engine.ArchiveFileName)
	d.launchctl = filepath.Join(root, "launchctl.log")
	for _, dir := range []string{d.home, d.dir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	stub := filepath.Join(root, "launchctl-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho \"$@\" >> \""+d.launchctl+"\"\n"), 0755); err != nil {
		t.Fatal(err)
	}

	d.env = []string{
		"HOME=" + d.home,
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"SENECA_FILE=" + d.reflections,
		"SENECA_SYNC_EXCHANGE=" + d.exchange,
		"SENECA_LAUNCHCTL=" + stub,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=Seneca Test", "GIT_AUTHOR_EMAIL=test@example.invalid",
		"GIT_COMMITTER_NAME=Seneca Test", "GIT_COMMITTER_EMAIL=test@example.invalid",
	}

	d.write(d.reflections, seedEntry("2026-01-01", "Rushed.", "Listened.", "Slow down.")+seedEntry("2026-01-02", "Snapped.", "Apologized.", "Pause first."))
	d.write(filepath.Join(d.repo, "notes.txt"), "unrelated\n")
	d.git("init", "--quiet", "--initial-branch=main")
	d.git("add", "-A")
	d.git("commit", "--quiet", "-m", "initial")
	return d
}

func (d *desk) write(path, content string) {
	d.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		d.t.Fatalf("writing %s: %v", path, err)
	}
}

func (d *desk) read(path string) string {
	d.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		d.t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func (d *desk) gitTry(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = d.repo
	cmd.Env = d.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (d *desk) git(args ...string) string {
	d.t.Helper()
	out, err := d.gitTry(args...)
	if err != nil {
		d.t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// subjects are the commit subjects of the repository, newest first.
func (d *desk) subjects() []string {
	d.t.Helper()
	return strings.Split(strings.TrimSpace(d.git("log", "--format=%s")), "\n")
}

func (d *desk) count(subject string) int {
	n := 0
	for _, got := range d.subjects() {
		if got == subject {
			n++
		}
	}
	return n
}

// run executes seneca and returns what it printed on each stream.
func (d *desk) run(args ...string) (string, string, error) {
	d.t.Helper()
	cmd := exec.Command(d.bin, args...)
	cmd.Dir = d.repo
	cmd.Env = d.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func (d *desk) must(args ...string) string {
	d.t.Helper()
	stdout, stderr, err := d.run(args...)
	if err != nil {
		d.t.Fatalf("seneca %s failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

// manifests lists every transaction the desktop holds, by file name.
func (d *desk) manifests() []string {
	d.t.Helper()
	var names []string
	root := filepath.Join(engine.SyncDirFor(d.reflections), "transactions")
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			names = append(names, entry.Name())
		}
		return nil
	})
	sort.Strings(names)
	return names
}

// tree is every file under the repository and the exchange with its contents,
// Git's own bookkeeping included: what a command that changes nothing leaves
// exactly as it was.
func (d *desk) tree() map[string]string {
	d.t.Helper()
	files := map[string]string{}
	for _, root := range []string{d.repo, d.exchange, d.home} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				d.t.Fatalf("reading %s: %v", path, err)
			}
			files[path] = string(data)
			return nil
		})
	}
	return files
}

func requireSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for path, content := range after {
		if old, existed := before[path]; !existed {
			t.Errorf("%s created %s", what, path)
		} else if old != content {
			t.Errorf("%s changed %s", what, path)
		}
	}
	for path := range before {
		if _, kept := after[path]; !kept {
			t.Errorf("%s removed %s", what, path)
		}
	}
}

// watcher is `seneca sync watch` running as its own process, the way launchd
// runs it.
type watcher struct {
	t    *testing.T
	cmd  *exec.Cmd
	log  string
	done chan struct{}
}

func (d *desk) startWatcher(program string, args ...string) *watcher {
	d.t.Helper()
	w := &watcher{t: d.t, log: filepath.Join(d.t.TempDir(), "watch.log"), done: make(chan struct{})}
	logFile, err := os.Create(w.log)
	if err != nil {
		d.t.Fatal(err)
	}
	w.cmd = exec.Command(program, args...)
	w.cmd.Dir = d.home
	w.cmd.Env = d.env
	w.cmd.Stdout, w.cmd.Stderr = logFile, logFile
	if err := w.cmd.Start(); err != nil {
		d.t.Fatalf("starting the watcher: %v", err)
	}
	go func() {
		_ = w.cmd.Wait()
		_ = logFile.Close()
		close(w.done)
	}()
	d.t.Cleanup(w.stop)
	return w
}

func (d *desk) watch() *watcher {
	return d.startWatcher(d.bin, "sync", "watch", "--poll", "50ms", "--interval", "1s")
}

func (w *watcher) stop() {
	select {
	case <-w.done:
		return
	default:
	}
	_ = w.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-w.done:
	case <-time.After(10 * time.Second):
		_ = w.cmd.Process.Kill()
		<-w.done
		w.t.Errorf("the watcher did not stop on SIGTERM; its log:\n%s", w.output())
	}
}

func (w *watcher) output() string {
	data, _ := os.ReadFile(w.log)
	return string(data)
}

// eventually waits for a condition a background process brings about.
func eventually(t *testing.T, what string, context func() string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s\n%s", what, context())
}

// phone is the other device: the engine over its own directory.
type phone struct {
	t        *testing.T
	dir      string
	exchange string
}

func newPhone(t *testing.T, exchange string) *phone {
	t.Helper()
	p := &phone{t: t, dir: t.TempDir(), exchange: exchange}
	if _, err := p.journal().JoinSync(p.transport(), "phone"); err != nil {
		t.Fatalf("phone: joining: %v", err)
	}
	p.pull()
	return p
}

func (p *phone) reflections() string { return filepath.Join(p.dir, "reflections.md") }

func (p *phone) transport() engine.SyncTransport { return engine.NewDirectoryTransport(p.exchange) }

func (p *phone) journal() *engine.Journal {
	p.t.Helper()
	journal, err := engine.Open(engine.Options{
		ReflectionsPath: p.reflections(),
		Settings:        engine.ReflectionSettings{Questions: syncQuestions, AnchorQuestionIndex: 3},
		Clock:           time.Now,
	})
	if err != nil {
		p.t.Fatalf("phone: opening its journal: %v", err)
	}
	return journal
}

func (p *phone) pull() engine.SyncImported {
	p.t.Helper()
	imported, err := p.journal().ImportSync(p.transport())
	if err != nil {
		p.t.Fatalf("phone: importing: %v", err)
	}
	return imported
}

func (p *phone) publish() {
	p.t.Helper()
	if _, err := p.journal().PublishSync(p.transport()); err != nil {
		p.t.Fatalf("phone: publishing: %v", err)
	}
}

// reflect saves a day on the phone as its form would, and publishes it.
func (p *phone) reflect(date string, answers ...string) {
	p.t.Helper()
	journal := p.journal()
	draft, err := journal.PrepareReflection(date, true)
	if err != nil {
		p.t.Fatalf("phone: preparing %s: %v", date, err)
	}
	if _, err := journal.SaveReflection(draft, engine.ReflectionResult{Saved: true, Answers: answers}, true); err != nil {
		p.t.Fatalf("phone: saving %s: %v", date, err)
	}
	p.publish()
}

func (p *phone) text() string {
	p.t.Helper()
	data, err := os.ReadFile(p.reflections())
	if err != nil {
		p.t.Fatalf("phone: reading its journal: %v", err)
	}
	return string(data)
}

// enrolled is a desktop whose journal is enrolled and published, and a phone
// that joined it and holds the same entries.
func enrolled(t *testing.T) (*desk, *phone) {
	t.Helper()
	d := newDesk(t)
	out := d.must("sync", "enroll", "--name", "desk")
	if !strings.Contains(out, "Enrolled journal") || !strings.Contains(out, "Published") {
		t.Fatalf("unexpected enroll output:\n%s", out)
	}
	p := newPhone(t, d.exchange)
	if !strings.Contains(p.text(), "Apologized.") {
		t.Fatalf("the phone did not receive the journal:\n%s", p.text())
	}
	return d, p
}

func TestBackgroundImportReachesMarkdownAndAnIsolatedGitCommit(t *testing.T) {
	d, p := enrolled(t)

	// Work the author staged and has not committed yet, in the same repository.
	d.write(filepath.Join(d.repo, "notes.txt"), "unrelated\nstaged, not ours\n")
	d.git("add", "notes.txt")
	// And an edit they have not even staged.
	d.write(filepath.Join(d.repo, "scratch.txt"), "untracked\n")

	w := d.watch()
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")

	eventually(t, "the phone's entry to be committed", w.output, func() bool {
		return d.count("seneca: sync import") == 1
	})
	if text := d.read(d.reflections); !strings.Contains(text, "Hurried a friend.") || !strings.Contains(text, "Apologized.") {
		t.Fatalf("the journal does not hold the phone's entry beside its own:\n%s", text)
	}

	// The commit is the journal file and nothing else.
	if files := strings.Fields(d.git("show", "--name-only", "--format=", "HEAD")); len(files) != 1 || files[0] != "journal/reflections.md" {
		t.Fatalf("the sync commit names %v, want only journal/reflections.md", files)
	}
	// What was staged is still staged, and still uncommitted.
	if staged := strings.Fields(d.git("diff", "--cached", "--name-only")); len(staged) != 1 || staged[0] != "notes.txt" {
		t.Fatalf("staged work after the sync commit = %v, want only notes.txt", staged)
	}
	if committed := d.git("show", "HEAD:notes.txt"); committed != "unrelated\n" {
		t.Fatalf("the author's staged change was committed: %q", committed)
	}
	// The history sync keeps beside the journal is this device's own.
	for _, tracked := range strings.Fields(d.git("ls-files")) {
		if strings.Contains(tracked, "/sync/") || strings.Contains(tracked, "/desktop/") || strings.Contains(tracked, "/directory/") {
			t.Errorf("runtime metadata is tracked by git: %s", tracked)
		}
	}
	if status := d.git("status", "--porcelain", "--", "journal/reflections.md"); status != "" {
		t.Fatalf("the journal file is not clean after the sync commit: %q", status)
	}

	// A second change makes a second commit, and only one.
	p.reflect("2026-01-04", "Skipped the walk.", "Wrote instead.", "Walk first.")
	eventually(t, "the second entry to be committed", w.output, func() bool {
		return d.count("seneca: sync import") == 2
	})
	if n := strings.Count(d.read(d.reflections), "## 2026-01-03"); n != 1 {
		t.Fatalf("the first imported entry appears %d times", n)
	}
}

func TestAGitFailureIsRetriedWithoutRepeatingTheImport(t *testing.T) {
	d, p := enrolled(t)

	// Another Git process "holds" the index: every commit fails until it lets go.
	lock := filepath.Join(d.repo, ".git", "index.lock")
	d.write(lock, "")

	w := d.watch()
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")

	note := filepath.Join(engine.JournalMetadataDirFor(d.reflections), "desktop", "git-pending.json")
	attempts := func() int {
		data, err := os.ReadFile(note)
		if err != nil {
			return 0
		}
		var pending struct {
			Attempts int `json:"attempts"`
		}
		_ = json.Unmarshal(data, &pending)
		return pending.Attempts
	}

	// The save does not wait for Git.
	eventually(t, "the entry to reach the journal despite git", w.output, func() bool {
		return strings.Contains(d.read(d.reflections), "Hurried a friend.") && attempts() >= 1
	})
	held := d.manifests()
	text := d.read(d.reflections)

	// It is retried, and each retry changes nothing but the note.
	eventually(t, "the commit to be retried", w.output, func() bool { return attempts() >= 3 })
	if got := d.manifests(); !equal(got, held) {
		t.Fatalf("retrying the commit recorded more history:\nbefore %v\nafter  %v", held, got)
	}
	if got := d.read(d.reflections); got != text {
		t.Fatalf("retrying the commit rewrote the journal:\n%s", got)
	}
	if n := d.count("seneca: sync import"); n != 0 {
		t.Fatalf("%d sync commits were made while git was failing", n)
	}
	status := d.must("sync", "status")
	if !strings.Contains(status, "not committed yet") || !strings.Contains(status, "git add failed") {
		t.Fatalf("status does not report the failing commit:\n%s", status)
	}
	if !strings.Contains(w.output(), "will be retried") {
		t.Fatalf("the watcher did not report the failing commit:\n%s", w.output())
	}

	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the commit to succeed once git is free", w.output, func() bool {
		return d.count("seneca: sync import") == 1
	})
	eventually(t, "the retry note to be dropped", w.output, func() bool {
		_, err := os.Stat(note)
		return errors.Is(err, os.ErrNotExist)
	})
	if n := strings.Count(d.read(d.reflections), "## 2026-01-03"); n != 1 {
		t.Fatalf("the imported entry appears %d times", n)
	}
	if got := d.manifests(); !equal(got, held) {
		t.Fatalf("the late commit recorded more history:\nbefore %v\nafter  %v", held, got)
	}
	if n := d.count("seneca: sync import"); n != 1 {
		t.Fatalf("%d sync commits for one import", n)
	}
}

func equal(a, b []string) bool {
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}

func TestAMergeInProgressDefersBackgroundImport(t *testing.T) {
	d, p := enrolled(t)

	// A merge the author has not finished: two branches changed notes.txt.
	d.git("checkout", "--quiet", "-b", "other")
	d.write(filepath.Join(d.repo, "notes.txt"), "theirs\n")
	d.git("commit", "--quiet", "-am", "other")
	d.git("checkout", "--quiet", "main")
	d.write(filepath.Join(d.repo, "notes.txt"), "ours\n")
	d.git("commit", "--quiet", "-am", "main")
	if out, err := d.gitTry("merge", "other"); err == nil {
		t.Fatalf("expected the merge to stop on a conflict:\n%s", out)
	}

	before := d.read(d.reflections)
	held := d.manifests()
	w := d.watch()
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")

	eventually(t, "the watcher to notice the merge", w.output, func() bool {
		return strings.Contains(w.output(), "a merge is in progress")
	})
	// Give it every chance to import anyway: several retries and a full scan.
	time.Sleep(2500 * time.Millisecond)
	if got := d.read(d.reflections); got != before {
		t.Fatalf("the journal was changed during a merge:\n%s", got)
	}
	if got := d.manifests(); !equal(got, held) {
		t.Fatalf("history was recorded during a merge:\nbefore %v\nafter  %v", held, got)
	}
	if n := d.count("seneca: sync import"); n != 0 {
		t.Fatalf("%d sync commits were made during a merge", n)
	}
	if status := d.must("sync", "status"); !strings.Contains(status, "a merge is in progress") || !strings.Contains(status, "not imported yet: 1") {
		t.Fatalf("status does not report the deferral:\n%s", status)
	}

	d.git("merge", "--abort")
	eventually(t, "the import to land once the merge is over", w.output, func() bool {
		return d.count("seneca: sync import") == 1
	})
	if !strings.Contains(d.read(d.reflections), "Hurried a friend.") {
		t.Fatalf("the journal does not hold the phone's entry:\n%s", d.read(d.reflections))
	}
}

func TestARebaseOrAnUnmergedJournalDefersSyncOnce(t *testing.T) {
	d, p := enrolled(t)
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")
	before := d.read(d.reflections)

	// A rebase stopped halfway leaves this directory until it is continued.
	rebase := filepath.Join(d.repo, ".git", "rebase-merge")
	if err := os.Mkdir(rebase, 0755); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := d.run("sync", "once")
	if err != nil || !strings.Contains(stderr, "a rebase is in progress") {
		t.Fatalf("expected sync once to defer during a rebase, got err=%v stderr:\n%s", err, stderr)
	}
	if got := d.read(d.reflections); got != before {
		t.Fatalf("the journal was changed during a rebase:\n%s", got)
	}
	if err := os.Remove(rebase); err != nil {
		t.Fatal(err)
	}

	// The journal file itself left unmerged by a merge the author resolved
	// everything else of.
	d.git("checkout", "--quiet", "-b", "other")
	d.write(d.reflections, before+seedEntry("2026-02-01", "Theirs.", "", ""))
	d.git("commit", "--quiet", "-am", "other")
	d.git("checkout", "--quiet", "main")
	d.write(d.reflections, before+seedEntry("2026-02-01", "Ours.", "", ""))
	d.git("commit", "--quiet", "-am", "main")
	if out, err := d.gitTry("merge", "other"); err == nil {
		t.Fatalf("expected the merge to stop on a conflict:\n%s", out)
	}
	// Even with the merge itself no longer announced, the file is unmerged.
	if err := os.Remove(filepath.Join(d.repo, ".git", "MERGE_HEAD")); err != nil {
		t.Fatal(err)
	}
	conflicted := d.read(d.reflections)
	_, stderr, err = d.run("sync", "once")
	if err != nil || !strings.Contains(stderr, "unresolved merge") {
		t.Fatalf("expected sync once to defer on an unmerged journal, got err=%v stderr:\n%s", err, stderr)
	}
	if got := d.read(d.reflections); got != conflicted {
		t.Fatalf("the unmerged journal was rewritten:\n%s", got)
	}
}

func TestReadOnlyCommandsNeverImportRecoverOrCommit(t *testing.T) {
	d, p := enrolled(t)
	d.must("focus", "set", "Protect the first hour.")

	// Something waits in the exchange, and an edit waits in the file.
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Rushed.", "Rushed, twice.", 1))

	before := d.tree()
	for _, args := range [][]string{
		{"sync", "status"},
		{"doctor"},
		{"show"},
		{"show", "--last", "5"},
		{"focus"},
		{"focus", "history"},
		{"review", "history"},
		{"settings"},
		{"sync", "conflicts"},
	} {
		stdout, stderr, err := d.run(args...)
		if err != nil {
			t.Fatalf("seneca %s failed: %v\n%s\n%s", strings.Join(args, " "), err, stdout, stderr)
		}
		requireSameTree(t, "seneca "+strings.Join(args, " "), before, d.tree())
	}

	status := d.must("sync", "status")
	for _, want := range []string{"this device is \"desk\"", "Exchange: " + d.exchange, "not imported yet: 1", "not published yet: 0", "Conflicts: none", "Background service: not installed"} {
		if runtime.GOOS != "darwin" && strings.HasPrefix(want, "Background service") {
			continue
		}
		if !strings.Contains(status, want) {
			t.Errorf("status does not say %q:\n%s", want, status)
		}
	}
}

func TestAMutationImportsFirstAndPublishesAfterward(t *testing.T) {
	d, p := enrolled(t)
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")

	out := d.must("focus", "set", "Protect the first hour.")
	if !strings.Contains(out, "Applied 1 change from other devices.") || !strings.Contains(out, "Focus set for") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	// What had arrived is in the journal and in Git, as its own commit, before
	// the command's own.
	if !strings.Contains(d.read(d.reflections), "Hurried a friend.") {
		t.Fatalf("the journal does not hold the phone's entry:\n%s", d.read(d.reflections))
	}
	subjects := d.subjects()
	if len(subjects) < 2 || !strings.HasPrefix(subjects[0], "seneca: focus set ") || subjects[1] != "seneca: sync import" {
		t.Fatalf("unexpected commits, newest first: %v", subjects)
	}

	// And the command's own change is already in the exchange for the phone.
	imported := p.pull()
	if len(imported.Applied) != 1 {
		t.Fatalf("the phone applied %d changes, want the focus alone: %+v", len(imported.Applied), imported)
	}
	state, _, err := p.journal().Focus(time.Now().Format("2006-01-02"))
	if err != nil || state.Text != "Protect the first hour." {
		t.Fatalf("the phone's focus = %q (%v)", state.Text, err)
	}

	// With the exchange gone the command still saves; only publishing waits.
	blocked := filepath.Join(filepath.Dir(d.exchange), "blocked")
	d.write(blocked, "not a directory\n")
	d.env = append(d.env, "SENECA_SYNC_EXCHANGE="+filepath.Join(blocked, "exchange"))
	stdout, stderr, err := d.run("focus", "set", "Leave on time.")
	if err != nil || !strings.Contains(stdout, "Leave on time.") {
		t.Fatalf("a save with no exchange failed: %v\n%s\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "saved here but was not published") {
		t.Fatalf("expected a warning that the change was not published:\n%s", stderr)
	}
	if !strings.Contains(d.read(d.archive), "Leave on time.") {
		t.Fatalf("the archive does not hold the focus:\n%s", d.read(d.archive))
	}
}

func TestConflictsAreListedAndResolvedFromTheCommandLine(t *testing.T) {
	d, p := enrolled(t)

	// Both devices rewrite the same answer of the same day.
	if err := os.WriteFile(p.reflections(), []byte(strings.Replace(p.text(), "Snapped.", "Snapped at the phone.", 1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.journal().RecordExternalEdits(); err != nil {
		t.Fatalf("phone: recording its edit: %v", err)
	}
	p.publish()
	d.write(d.reflections, strings.Replace(d.read(d.reflections), "Snapped.", "Snapped at the desk.", 1))

	_, stderr, err := d.run("sync", "once")
	if err != nil || !strings.Contains(stderr, "1 conflict waits for you") {
		t.Fatalf("expected sync once to report a conflict, got err=%v stderr:\n%s", err, stderr)
	}
	if text := d.read(d.reflections); !strings.Contains(text, "Snapped at the desk.") || strings.Contains(text, "Snapped at the phone.") {
		t.Fatalf("a conflict must leave this device's version in the file:\n%s", text)
	}

	listing := d.must("sync", "conflicts")
	for _, want := range []string{"entry:2026-01-02", "Snapped at the desk.", "Snapped at the phone.", "ours", "theirs"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("the listing does not show %q:\n%s", want, listing)
		}
	}
	id := regexp.MustCompile(`Conflict ([0-9a-f]+):`).FindStringSubmatch(listing)
	if id == nil {
		t.Fatalf("the listing names no conflict id:\n%s", listing)
	}

	if _, stderr, err := d.run("sync", "resolve", id[1]); err == nil || !strings.Contains(stderr, "choose one of --keep") {
		t.Fatalf("expected a resolve with no answer to be refused, got err=%v stderr:\n%s", err, stderr)
	}
	if _, stderr, err := d.run("sync", "resolve", "zz", "--keep", "ours"); err == nil || !strings.Contains(stderr, "names 0 of the conflicts") {
		t.Fatalf("expected an unknown id to be refused, got err=%v stderr:\n%s", err, stderr)
	}

	out := d.must("sync", "resolve", id[1], "--keep", "theirs")
	if !strings.Contains(out, "Resolved entry:2026-01-02") || !strings.Contains(out, "Committed: seneca: sync resolve entry:2026-01-02") {
		t.Fatalf("unexpected resolve output:\n%s", out)
	}
	if text := d.read(d.reflections); !strings.Contains(text, "Snapped at the phone.") || strings.Contains(text, "Snapped at the desk.") {
		t.Fatalf("the kept version is not in the file:\n%s", text)
	}
	if listing := d.must("sync", "conflicts"); !strings.Contains(listing, "No conflicts.") {
		t.Fatalf("the conflict is still listed:\n%s", listing)
	}
	// The answer travels: the phone, which held the same conflict, has none.
	p.pull()
	if conflicts, err := p.journal().SyncConflicts(); err != nil || len(conflicts) != 0 {
		t.Fatalf("the phone still holds %d conflicts (%v)", len(conflicts), err)
	}
}

func TestEnrollRefusesToStartASecondJournalInOneExchange(t *testing.T) {
	d, _ := enrolled(t)

	if out := d.must("sync", "enroll"); !strings.Contains(out, "Already enrolled") {
		t.Fatalf("expected a second enroll to report the journal as it is:\n%s", out)
	}

	// A second desktop pointed at the same exchange must join, not enroll.
	other := newDesk(t)
	other.env = append(other.env, "SENECA_SYNC_EXCHANGE="+d.exchange)
	before := other.tree()
	_, stderr, err := other.run("sync", "enroll")
	if err == nil || !strings.Contains(stderr, "already carries a journal") {
		t.Fatalf("expected enrolling into a used exchange to be refused, got err=%v stderr:\n%s", err, stderr)
	}
	requireSameTree(t, "a refused enroll", before, other.tree())

	if out := other.must("sync", "enroll", "--join", "--name", "laptop"); !strings.Contains(out, "Joined journal") {
		t.Fatalf("unexpected join output:\n%s", out)
	}
	if _, stderr, err := other.run("sync", "once"); err != nil {
		t.Fatalf("sync once after joining failed: %v\n%s", err, stderr)
	}

	// Outside sync nothing is exchanged and nothing is created.
	plain := newDesk(t)
	snapshot := plain.tree()
	if _, stderr, err := plain.run("sync", "once"); err == nil || !strings.Contains(stderr, "does not take part in sync") {
		t.Fatalf("expected sync once to refuse a journal that is not enrolled, got err=%v stderr:\n%s", err, stderr)
	}
	if out := plain.must("sync", "status"); !strings.Contains(out, "does not take part in sync") {
		t.Fatalf("unexpected status:\n%s", out)
	}
	requireSameTree(t, "sync on a journal that is not enrolled", snapshot, plain.tree())
}

func TestRemovingTheServiceLeavesTheJournalUsable(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the background service is a macOS LaunchAgent")
	}
	d, p := enrolled(t)

	out := d.must("sync", "service", "install")
	plistPath := filepath.Join(d.home, "Library", "LaunchAgents", "io.sglavoie.seneca.sync.plist")
	if !strings.Contains(out, plistPath) {
		t.Fatalf("install does not name the LaunchAgent:\n%s", out)
	}
	plist := d.read(plistPath)
	for _, want := range []string{"<string>" + d.reflections + "</string>", "<string>" + d.exchange + "</string>", "<key>KeepAlive</key>", "<key>RunAtLoad</key>"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("the LaunchAgent does not hold %s:\n%s", want, plist)
		}
	}
	if calls := d.read(d.launchctl); !strings.Contains(calls, "bootstrap gui/") || !strings.Contains(calls, plistPath) {
		t.Fatalf("launchd was not asked to start the agent:\n%s", calls)
	}
	if status := d.must("sync", "status"); !strings.Contains(status, "Background service: installed") {
		t.Fatalf("status does not report the service:\n%s", status)
	}

	// Run exactly what the agent tells launchd to run, as launchd would.
	arguments := regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>(.*?)</array>`).FindStringSubmatch(plist)
	if arguments == nil {
		t.Fatalf("the LaunchAgent has no ProgramArguments:\n%s", plist)
	}
	var program []string
	for _, match := range regexp.MustCompile(`<string>(.*?)</string>`).FindAllStringSubmatch(arguments[1], -1) {
		program = append(program, match[1])
	}
	if len(program) != 3 || program[0] != d.bin || program[1] != "sync" || program[2] != "watch" {
		t.Fatalf("the agent runs %v, want %s sync watch", program, d.bin)
	}
	service := d.startWatcher(program[0], program[1:]...)

	// No command is run on the desktop from here until the entry is in Git.
	p.reflect("2026-01-03", "Hurried a friend.", "Called back.", "Leave room.")
	eventually(t, "the service to commit the phone's entry", service.output, func() bool {
		return d.count("seneca: sync import") == 1
	})
	if !strings.Contains(d.read(d.reflections), "Hurried a friend.") {
		t.Fatalf("the journal does not hold the phone's entry:\n%s", d.read(d.reflections))
	}

	out = d.must("sync", "service", "remove")
	if !strings.Contains(out, "Background sync removed") {
		t.Fatalf("unexpected remove output:\n%s", out)
	}
	if _, err := os.Stat(plistPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the LaunchAgent is still there: %v", err)
	}
	if calls := d.read(d.launchctl); !strings.Contains(calls, "bootout gui/") {
		t.Fatalf("launchd was not asked to stop the agent:\n%s", calls)
	}
	service.stop()
	if out := d.must("sync", "service", "remove"); !strings.Contains(out, "not installed") {
		t.Fatalf("removing twice should say there is nothing to remove:\n%s", out)
	}

	// With the service gone, and even with the exchange unreachable, the
	// journal is the same local journal it always was.
	blocked := filepath.Join(filepath.Dir(d.exchange), "blocked")
	d.write(blocked, "not a directory\n")
	d.env = append(d.env, "SENECA_SYNC_EXCHANGE="+filepath.Join(blocked, "exchange"))

	if out := d.must("focus", "set", "Leave on time."); !strings.Contains(out, "Committed: seneca: focus set") {
		t.Fatalf("a local save did not commit:\n%s", out)
	}
	if out := d.must("focus"); !strings.Contains(out, "Leave on time.") {
		t.Fatalf("the focus is not shown:\n%s", out)
	}
	if out := d.must("show", "--date", "2026-01-03"); !strings.Contains(out, "Hurried a friend.") {
		t.Fatalf("the imported entry is not shown:\n%s", out)
	}
	if out := d.must("sort"); !strings.Contains(out, "entries") {
		t.Fatalf("unexpected sort output:\n%s", out)
	}
	stdout, stderr, err := d.run("doctor")
	if err != nil {
		t.Fatalf("doctor reports an error after the service was removed: %v\n%s\n%s", err, stdout, stderr)
	}
	if status := d.must("sync", "status"); !strings.Contains(status, "Background service: not installed") {
		t.Fatalf("status still reports the service:\n%s", status)
	}

	// And with the exchange back, one command by hand catches up.
	d.env = append(d.env, "SENECA_SYNC_EXCHANGE="+d.exchange)
	d.must("sync", "once")
	p.pull()
	if state, _, err := p.journal().Focus(time.Now().Format("2006-01-02")); err != nil || state.Text != "Leave on time." {
		t.Fatalf("the phone's focus = %q (%v)", state.Text, err)
	}
}

func TestADisputedFocusIsSettledFromTheCommandLine(t *testing.T) {
	d, p := enrolled(t)

	// Each device sets a focus for today without having seen the other's.
	if _, err := p.journal().SetFocus("Phone focus."); err != nil {
		t.Fatalf("phone: setting its focus: %v", err)
	}
	d.must("focus", "set", "Desk focus.")
	p.publish()
	_, stderr, err := d.run("sync", "once")
	if err != nil {
		t.Fatalf("sync once failed: %v\n%s", err, stderr)
	}

	if out := d.must("focus"); !strings.Contains(out, "Focus disputed") {
		t.Fatalf("the focus is not reported as disputed:\n%s", out)
	}
	if status := d.must("sync", "status"); !strings.Contains(status, "Conflicts: 1 wait for you") {
		t.Fatalf("status does not count the dispute:\n%s", status)
	}
	listing := d.must("sync", "conflicts")
	id := regexp.MustCompile(`Disputed focus ([0-9a-f]+):`).FindStringSubmatch(listing)
	if id == nil || !strings.Contains(listing, "Phone focus.") || !strings.Contains(listing, "Desk focus.") {
		t.Fatalf("the listing does not show the dispute:\n%s", listing)
	}

	if _, stderr, err := d.run("sync", "resolve", id[1], "--file", d.reflections); err == nil || !strings.Contains(stderr, "--file is for an entry") {
		t.Fatalf("expected --file to be refused for a focus, got err=%v stderr:\n%s", err, stderr)
	}
	out := d.must("sync", "resolve", id[1], "--text", "One focus.")
	if !strings.Contains(out, "One focus.") || !strings.Contains(out, "Committed: seneca: sync resolve focus") {
		t.Fatalf("unexpected resolve output:\n%s", out)
	}
	if out := d.must("focus"); !strings.Contains(out, "One focus.") || strings.Contains(out, "disputed") {
		t.Fatalf("the focus is not settled:\n%s", out)
	}
	if listing := d.must("sync", "conflicts"); !strings.Contains(listing, "No conflicts.") {
		t.Fatalf("the dispute is still listed:\n%s", listing)
	}
	p.pull()
	if state, _, err := p.journal().Focus(time.Now().Format("2006-01-02")); err != nil || state.Text != "One focus." {
		t.Fatalf("the phone's focus = %q disputed=%v (%v)", state.Text, state.Disputed, err)
	}
}

func TestAMissingJournalFileIsRecoveredNotPublishedAsDeletions(t *testing.T) {
	d, p := enrolled(t)
	original := d.read(d.reflections)

	if err := os.Remove(d.reflections); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := d.run("sync", "once")
	if err != nil || !strings.Contains(stderr, "local-missing") {
		t.Fatalf("expected sync once to report the missing file, got err=%v stderr:\n%s", err, stderr)
	}
	if status := d.must("sync", "status"); !strings.Contains(status, "Not read: the reflections file") {
		t.Fatalf("status does not report the file:\n%s", status)
	}
	// Nothing of it reached the other device.
	if imported := p.pull(); len(imported.Applied) != 0 || !strings.Contains(p.text(), "Apologized.") {
		t.Fatalf("the phone received something for a missing file: %+v", imported)
	}

	if _, stderr, err := d.run("sync", "recover", "reflections"); err == nil || !strings.Contains(stderr, "choose one of --restore or --accept-deletions") {
		t.Fatalf("expected a recover with no decision to be refused, got err=%v stderr:\n%s", err, stderr)
	}
	out := d.must("sync", "recover", "reflections", "--restore")
	if !strings.Contains(out, "Restored 2 items") || !strings.Contains(out, "read normally again") {
		t.Fatalf("unexpected recover output:\n%s", out)
	}
	restored := d.read(d.reflections)
	for _, want := range []string{"## 2026-01-01", "Rushed.", "## 2026-01-02", "Apologized."} {
		if !strings.Contains(restored, want) {
			t.Fatalf("the restored journal lacks %q:\n%s\n(it was)\n%s", want, restored, original)
		}
	}
	if status := d.must("sync", "status"); strings.Contains(status, "Not read") {
		t.Fatalf("status still reports the file:\n%s", status)
	}
}
