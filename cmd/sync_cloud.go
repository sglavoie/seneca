package cmd

import (
	"context"
	"errors"
	"fmt"
	"github.com/sglavoie/seneca/engine"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// An exchange directory inside iCloud Drive is not always all there. A file
// another device published can be listed on this Mac before its bytes are: as
// a `.<name>.icloud` placeholder beside where the file will be, or, on a
// current macOS, as a dataless file under its own name whose first read waits
// for the download. A file this Mac already had can go back to that state
// when macOS reclaims the space.
//
// cloudExchange is the desktop's side of that, and only the desktop's: the
// engine sees a directory transport in which such a file has simply not
// arrived yet, which is something it already knows how to wait for. Nothing
// here reads a file that is not local, so no exchange blocks on the network,
// and each such file is asked for so a later exchange finds it.

// brctlEnv names the program that asks iCloud Drive for a file; it exists as
// a seam for tests, the way SENECA_LAUNCHCTL does.
const brctlEnv = "SENECA_BRCTL"

const (
	cloudPlaceholderPrefix = "."
	cloudPlaceholderSuffix = ".icloud"
	// cloudRequestLimit bounds what one exchange asks for; the next asks for
	// the rest.
	cloudRequestLimit   = 64
	cloudRequestTimeout = 10 * time.Second
)

// errCloudDownloading is a file that exists in the exchange and whose bytes
// are not on this Mac. To the engine it has not arrived.
var errCloudDownloading = fmt.Errorf("it is still downloading from iCloud: %w", fs.ErrNotExist)

// cloudDataless reports whether a file's bytes are in the cloud only. Tests
// replace it: a dataless file cannot be made by hand.
var cloudDataless = datalessFile

type cloudExchange struct {
	*engine.DirectoryTransport
	root string
	// waiting names each file of the exchange, by its slash-separated path
	// from the root, that is listed here and not downloaded.
	waiting map[string]bool
}

// placeholderName reads the file name a `.<name>.icloud` placeholder stands
// for.
func placeholderName(name string) (string, bool) {
	if !strings.HasPrefix(name, cloudPlaceholderPrefix) || !strings.HasSuffix(name, cloudPlaceholderSuffix) {
		return "", false
	}
	real := strings.TrimSuffix(strings.TrimPrefix(name, cloudPlaceholderPrefix), cloudPlaceholderSuffix)
	return real, real != ""
}

// newCloudExchange looks at what of the exchange at root is here. It reads
// directory listings and file flags only, and asks for nothing.
func newCloudExchange(root string) *cloudExchange {
	exchange := &cloudExchange{DirectoryTransport: engine.NewDirectoryTransport(root), root: root, waiting: map[string]bool{}}
	exchange.look("", 0)
	return exchange
}

// look walks the exchange's own layout: the descriptor at the root, contents
// one level down, manifests and receipts two.
func (c *cloudExchange) look(relative string, depth int) {
	entries, err := os.ReadDir(filepath.Join(c.root, filepath.FromSlash(relative)))
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		at := name
		if relative != "" {
			at = relative + "/" + name
		}
		if entry.IsDir() {
			if depth < 2 && (depth > 0 || name == "transactions" || name == "objects" || name == "receipts") {
				c.look(at, depth+1)
			}
			continue
		}
		if real, ok := placeholderName(name); ok {
			if relative != "" {
				real = relative + "/" + real
			}
			c.waiting[real] = true
			continue
		}
		if info, err := entry.Info(); err == nil && cloudDataless(info) {
			c.waiting[at] = true
		}
	}
}

// downloading counts the files of the exchange that are not on this Mac.
func (c *cloudExchange) downloading() int {
	return len(c.waiting)
}

// request asks iCloud Drive for each file that is not here, and returns how
// many there are. Asking is all it does: the files arrive when they arrive,
// and the exchange that finds them imports them.
func (c *cloudExchange) request() int {
	if len(c.waiting) == 0 {
		return 0
	}
	program := os.Getenv(brctlEnv)
	if program == "" {
		program = "brctl"
	}
	path, err := exec.LookPath(program)
	if err != nil {
		return len(c.waiting)
	}
	names := make([]string, 0, len(c.waiting))
	for name := range c.waiting {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > cloudRequestLimit {
		names = names[:cloudRequestLimit]
	}
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), cloudRequestTimeout)
		// A request that fails is made again by the next exchange.
		_ = exec.CommandContext(ctx, path, "download", filepath.Join(c.root, filepath.FromSlash(name))).Run()
		cancel()
	}
	return len(c.waiting)
}

func (c *cloudExchange) relative(path string) string {
	relative, err := filepath.Rel(c.root, path)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(relative)
}

// Descriptor refuses to call an exchange empty while its descriptor is on the
// way: publishing into it then would start a second journal in one folder.
func (c *cloudExchange) Descriptor() ([]byte, error) {
	if c.waiting["journal.json"] {
		return nil, errors.New("the exchange's journal descriptor is still downloading from iCloud")
	}
	return c.DirectoryTransport.Descriptor()
}

// Transactions lists a manifest that is only a placeholder too: it is in the
// exchange, and reading it says it has not arrived.
func (c *cloudExchange) Transactions() ([]engine.SyncTransactionRef, error) {
	refs, err := c.DirectoryTransport.Transactions()
	if err != nil {
		return nil, err
	}
	listed := map[string]bool{}
	for _, ref := range refs {
		listed[ref.ID] = true
	}
	for name := range c.waiting {
		parts := strings.Split(name, "/")
		if len(parts) != 3 || parts[0] != "transactions" {
			continue
		}
		if ref, ok := engine.ParseSyncManifestName(parts[1], parts[2]); ok && !listed[ref.ID] {
			refs = append(refs, ref)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		return a.ID < b.ID
	})
	return refs, nil
}

func (c *cloudExchange) Transaction(ref engine.SyncTransactionRef) ([]byte, error) {
	if c.waiting[c.relative(c.DirectoryTransport.ManifestPath(ref))] {
		return nil, errCloudDownloading
	}
	return c.DirectoryTransport.Transaction(ref)
}

func (c *cloudExchange) Object(digest engine.Revision) ([]byte, error) {
	if c.waiting[c.relative(c.DirectoryTransport.ObjectPath(digest))] {
		return nil, errCloudDownloading
	}
	return c.DirectoryTransport.Object(digest)
}

func (c *cloudExchange) Receipt(ref engine.SyncReceiptRef) ([]byte, error) {
	if c.waiting[c.relative(c.DirectoryTransport.ReceiptPath(ref))] {
		return nil, errCloudDownloading
	}
	return c.DirectoryTransport.Receipt(ref)
}
