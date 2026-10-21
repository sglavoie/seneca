package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// A receipt is one device saying how much of each device's history it has
// applied. It is what lets the author of a change tell three things apart:
// the change is saved here, the change is in the exchange, and the change is
// in the other device's journal.
//
// A manifest in the exchange proves only the second. A receipt is written
// from the device's own sync state, which changes in the same commit as its
// Markdown, so it never claims a transaction whose changes are not in that
// device's files.
//
// # Format
//
// A receipt names its journal and device and, for each device whose history
// it holds, the highest sequence up to which every transaction of that device
// is applied here. A device's transactions form a chain, so that one number
// describes them all. It carries no time: the same state gives the same
// bytes, and so the same receipt.
//
//	<root>/receipts/<device>/<sha-256>.json
//
// Like everything else in an exchange a receipt is immutable and named by its
// own digest. A device publishes a new one when what it has applied grows,
// and a reader takes, per device, the highest number any whole receipt of the
// author states. Their order of arrival does not matter, and one that is
// damaged or still arriving is simply not counted.

const syncReceiptsName = "receipts"

var syncReceiptIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SyncReceipt is what one device has applied.
type SyncReceipt struct {
	Protocol int    `json:"protocol"`
	Journal  string `json:"journal"`
	Device   string `json:"device"`
	Name     string `json:"name,omitempty"`
	// Applied gives, per device, the sequence up to which that device's
	// transactions are all applied on this one.
	Applied map[string]int `json:"applied"`
}

// SyncReceiptRef names a receipt in an exchange without reading it.
type SyncReceiptRef struct {
	Device string
	ID     string
}

// SyncReceiptTransport is an exchange that also carries receipts. A transport
// without it exchanges the journal all the same; nothing is then known about
// what another device applied.
type SyncReceiptTransport interface {
	Receipts() ([]SyncReceiptRef, error)
	Receipt(ref SyncReceiptRef) ([]byte, error)
	PutReceipt(ref SyncReceiptRef, data []byte) error
}

func (t *DirectoryTransport) receiptPath(ref SyncReceiptRef) string {
	return filepath.Join(t.root, syncReceiptsName, ref.Device, ref.ID+syncManifestSuffix)
}

// Receipts lists the receipts present, by name alone.
func (t *DirectoryTransport) Receipts() ([]SyncReceiptRef, error) {
	root := filepath.Join(t.root, syncReceiptsName)
	devices, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []SyncReceiptRef
	for _, device := range devices {
		if !device.IsDir() || !syncIdentityPattern.MatchString(device.Name()) {
			continue
		}
		names, err := os.ReadDir(filepath.Join(root, device.Name()))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			id, found := strings.CutSuffix(name.Name(), syncManifestSuffix)
			if found && syncReceiptIDPattern.MatchString(id) {
				refs = append(refs, SyncReceiptRef{Device: device.Name(), ID: id})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Device != refs[j].Device {
			return refs[i].Device < refs[j].Device
		}
		return refs[i].ID < refs[j].ID
	})
	return refs, nil
}

func (t *DirectoryTransport) Receipt(ref SyncReceiptRef) ([]byte, error) {
	return os.ReadFile(t.receiptPath(ref))
}

func (t *DirectoryTransport) PutReceipt(ref SyncReceiptRef, data []byte) error {
	if !syncIdentityPattern.MatchString(ref.Device) || !syncReceiptIDPattern.MatchString(ref.ID) {
		return fmt.Errorf("%s/%s does not name a receipt", ref.Device, ref.ID)
	}
	return t.put(t.receiptPath(ref), data)
}

// decodeSyncReceipt reads a receipt found under ref. ok is false for one that
// is damaged, still arriving, of another journal, or not its author's.
func decodeSyncReceipt(ref SyncReceiptRef, data []byte, journal string) (SyncReceipt, bool) {
	if SyncTransactionID(data) != ref.ID {
		return SyncReceipt{}, false
	}
	var receipt SyncReceipt
	if err := decodeSyncJSON(data, &receipt); err != nil {
		return SyncReceipt{}, false
	}
	if receipt.Journal != journal || receipt.Device != ref.Device {
		return SyncReceipt{}, false
	}
	for device, sequence := range receipt.Applied {
		if !syncIdentityPattern.MatchString(device) || sequence < 0 {
			return SyncReceipt{}, false
		}
	}
	return receipt, true
}

// through is the sequence up to which present holds every sequence from 1.
func through(present map[int]bool) int {
	n := 0
	for present[n+1] {
		n++
	}
	return n
}

// appliedThrough reads, from a device's own history, how far each device's
// transactions are applied on it: stored, and not waiting on anything.
func appliedThrough(reflectionsPath string, state syncState) (map[string]int, error) {
	stored, err := listSyncManifests(filepath.Join(SyncDirFor(reflectionsPath), syncTransactionsName))
	if err != nil {
		return nil, fmt.Errorf("listing the sync history: %w", err)
	}
	applied := map[string]map[int]bool{}
	for _, ref := range stored {
		if _, pending := state.Pending[ref.ID]; pending {
			continue
		}
		if applied[ref.Device] == nil {
			applied[ref.Device] = map[int]bool{}
		}
		applied[ref.Device][ref.Sequence] = true
	}
	result := make(map[string]int, len(applied))
	for device, sequences := range applied {
		if n := through(sequences); n > 0 {
			result[device] = n
		}
	}
	return result, nil
}

// SyncReceiptPublished reports what PublishSyncReceipt did.
type SyncReceiptPublished struct {
	// ID names the receipt that describes this device now. It is empty when
	// there is nowhere to publish one.
	ID string
	// Written is false when the exchange already held that receipt.
	Written bool
}

// PublishSyncReceipt tells the exchange what this device has applied, when
// that is not already what its newest receipt says. It reads the journal's
// history and changes nothing in the journal. An exchange that holds no
// journal yet, or that carries no receipts, is left as it is.
//
// Call it once whatever follows an import on this device is done: a receipt
// is the other device's evidence that its change arrived.
func (j *Journal) PublishSyncReceipt(transport SyncTransport) (SyncReceiptPublished, error) {
	state, enrolled, err := j.readSyncState()
	if err != nil {
		return SyncReceiptPublished{}, err
	}
	if !enrolled {
		return SyncReceiptPublished{}, ErrSyncNotEnrolled
	}
	receipts, ok := transport.(SyncReceiptTransport)
	if !ok {
		return SyncReceiptPublished{}, nil
	}
	if found, err := checkExchange(transport, state); err != nil || !found {
		return SyncReceiptPublished{}, err
	}
	applied, err := appliedThrough(j.reflectionsPath, state)
	if err != nil {
		return SyncReceiptPublished{}, err
	}
	data, err := encodeSyncJSON(SyncReceipt{
		Protocol: SyncProtocolVersion, Journal: state.Journal, Device: state.Device, Name: state.Name, Applied: applied,
	})
	if err != nil {
		return SyncReceiptPublished{}, fmt.Errorf("encoding the receipt: %w", err)
	}
	ref := SyncReceiptRef{Device: state.Device, ID: SyncTransactionID(data)}
	published := SyncReceiptPublished{ID: ref.ID}
	there, err := receipts.Receipts()
	if err != nil {
		return published, fmt.Errorf("listing the exchange's receipts: %w", err)
	}
	for _, held := range there {
		if held == ref {
			return published, nil
		}
	}
	if err := receipts.PutReceipt(ref, data); err != nil {
		return published, fmt.Errorf("publishing the receipt: %w", err)
	}
	published.Written = true
	return published, nil
}

// SyncPeerDelivery is how far another device has applied this device's
// transactions, by its own account.
type SyncPeerDelivery struct {
	Device string
	// Name is empty until the device has published a receipt.
	Name string
	// Applied is the sequence up to which this device's transactions are all
	// applied there. Zero says nothing was confirmed, not that nothing arrived.
	Applied int
}

// SyncDelivery says where this device's own transactions have got to.
type SyncDelivery struct {
	// Recorded is how many transactions this device has recorded: all of them
	// are saved here.
	Recorded int
	// Published is the sequence up to which they are all in the exchange.
	Published int
	// Peers are the other devices the exchange or this device's history knows.
	Peers []SyncPeerDelivery
}

// SyncDelivery compares this device's history with an exchange, completing an
// interrupted commit first. It changes neither side. A nil transport, or an
// exchange that holds no journal yet, reports nothing published.
func (j *Journal) SyncDelivery(transport SyncTransport) (SyncDelivery, error) {
	state, enrolled, err := j.readSyncState()
	if err != nil {
		return SyncDelivery{}, err
	}
	if !enrolled {
		return SyncDelivery{}, ErrSyncNotEnrolled
	}
	return deliveryOf(j.reflectionsPath, state, transport)
}

// InspectSyncDelivery is SyncDelivery from what is on disk: no lock is taken
// and no interrupted commit is completed.
func InspectSyncDelivery(reflectionsPath string, transport SyncTransport) (SyncDelivery, error) {
	path := SyncStatePathFor(reflectionsPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return SyncDelivery{}, ErrSyncNotEnrolled
	}
	if err != nil {
		return SyncDelivery{}, fmt.Errorf("reading the sync state %s: %w", path, err)
	}
	state, err := decodeSyncState(path, data)
	if err != nil {
		return SyncDelivery{}, err
	}
	return deliveryOf(reflectionsPath, state, transport)
}

func deliveryOf(reflectionsPath string, state syncState, transport SyncTransport) (SyncDelivery, error) {
	delivery := SyncDelivery{Recorded: state.Sequence}
	peers := map[string]*SyncPeerDelivery{}
	peer := func(device string) *SyncPeerDelivery {
		if peers[device] == nil {
			peers[device] = &SyncPeerDelivery{Device: device}
		}
		return peers[device]
	}
	local, err := listSyncManifests(filepath.Join(SyncDirFor(reflectionsPath), syncTransactionsName))
	if err != nil {
		return SyncDelivery{}, fmt.Errorf("listing the sync history: %w", err)
	}
	for _, ref := range local {
		if ref.Device != state.Device {
			peer(ref.Device)
		}
	}

	found := false
	if transport != nil {
		if found, err = checkExchange(transport, state); err != nil {
			return SyncDelivery{}, err
		}
	}
	if found {
		remote, err := transport.Transactions()
		if err != nil {
			return SyncDelivery{}, fmt.Errorf("listing the exchange: %w", err)
		}
		mine := map[int]bool{}
		for _, ref := range remote {
			if ref.Device == state.Device {
				mine[ref.Sequence] = true
			} else {
				peer(ref.Device)
			}
		}
		delivery.Published = min(through(mine), delivery.Recorded)

		if receipts, ok := transport.(SyncReceiptTransport); ok {
			refs, err := receipts.Receipts()
			if err != nil {
				return SyncDelivery{}, fmt.Errorf("listing the exchange's receipts: %w", err)
			}
			for _, ref := range refs {
				if ref.Device == state.Device {
					continue
				}
				data, err := receipts.Receipt(ref)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					// One that cannot be read yet says nothing; the next look
					// may find it whole.
					continue
				}
				receipt, ok := decodeSyncReceipt(ref, data, state.Journal)
				if !ok {
					continue
				}
				p := peer(ref.Device)
				if receipt.Name != "" {
					p.Name = receipt.Name
				}
				p.Applied = max(p.Applied, min(receipt.Applied[state.Device], delivery.Recorded))
			}
		}
	}

	for _, p := range peers {
		delivery.Peers = append(delivery.Peers, *p)
	}
	sort.Slice(delivery.Peers, func(i, j int) bool { return delivery.Peers[i].Device < delivery.Peers[j].Device })
	return delivery, nil
}
