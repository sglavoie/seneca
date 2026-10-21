package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (p *peer) receipt() SyncReceiptPublished {
	p.t.Helper()

	published, err := p.journal.PublishSyncReceipt(p.exchange())
	if err != nil {
		p.t.Fatalf("%s: PublishSyncReceipt returned error: %v", p.label, err)
	}
	return published
}

func (p *peer) delivery() SyncDelivery {
	p.t.Helper()

	delivery, err := p.journal.SyncDelivery(p.exchange())
	if err != nil {
		p.t.Fatalf("%s: SyncDelivery returned error: %v", p.label, err)
	}
	return delivery
}

// appliedThere is what the one other device has confirmed of p's
// transactions. A device that has published nothing is not known yet.
func (p *peer) appliedThere() int {
	p.t.Helper()

	delivery := p.delivery()
	if len(delivery.Peers) == 0 {
		return 0
	}
	if len(delivery.Peers) != 1 {
		p.t.Fatalf("%s: delivery names %d peers, want one: %+v", p.label, len(delivery.Peers), delivery)
	}
	return delivery.Peers[0].Applied
}

// A change is saved here, then in the exchange, then in the other device's
// journal, and each of the three is reported only once it is true.
func TestDeliveryTellsSavedFromPublishedFromApplied(t *testing.T) {
	a, b := pair(t)
	base := a.delivery().Recorded

	a.write("2026-08-12", "Written on the desk.", "", "")
	saved := a.delivery()
	if saved.Recorded != base+1 || saved.Published != base {
		t.Fatalf("after a save delivery = %+v, want %d recorded and %d published", saved, base+1, base)
	}

	a.publish()
	if published := a.delivery(); published.Published != base+1 || a.appliedThere() != 0 {
		t.Fatalf("after publishing delivery = %+v, want everything published and nothing confirmed", published)
	}

	// The manifest reaching the other device's folder proves nothing: only
	// its receipt, written after it applied the change, does.
	carry(t, a, b, nil)
	carry(t, b, a, nil)
	if got := a.appliedThere(); got != 0 {
		t.Fatalf("a delivered manifest was reported as applied through %d", got)
	}

	requireNoDiagnostics(t, b.pull())
	if written := b.receipt(); !written.Written || written.ID == "" {
		t.Fatalf("PublishSyncReceipt = %+v, want a new receipt", written)
	}
	carry(t, b, a, nil)
	delivery := a.delivery()
	if got := delivery.Peers[0]; got.Applied != base+1 || got.Name != "phone" || got.Device != b.status().Device {
		t.Fatalf("after the receipt the peer is %+v, want %d applied by phone", got, base+1)
	}
	requireSameArtifacts(t, a, b)
}

func TestAReceiptIsWrittenOnlyWhenWhatWasAppliedGrew(t *testing.T) {
	a, b := pair(t)

	first := b.receipt()
	if again := b.receipt(); again.Written || again.ID != first.ID {
		t.Fatalf("an unchanged state published %+v after %+v", again, first)
	}
	before := readTree(t, b.cloud)
	b.receipt()
	if !sameTree(before, readTree(t, b.cloud)) {
		t.Fatal("publishing the same receipt changed the exchange")
	}

	a.write("2026-08-12", "More.", "", "")
	a.publish()
	carry(t, a, b, nil)
	requireNoDiagnostics(t, b.pull())
	if next := b.receipt(); !next.Written || next.ID == first.ID {
		t.Fatalf("after applying more the receipt is %+v, want a new one", next)
	}
	// Both stay: nothing in an exchange is replaced.
	receipts, err := b.exchange().Receipts()
	if err != nil || len(receipts) != 2 {
		t.Fatalf("Receipts = %v (%v), want both", receipts, err)
	}
}

// A transaction that is stored but waits on something is not applied, and a
// receipt does not claim it.
func TestAReceiptDoesNotClaimWhatIsStillWaiting(t *testing.T) {
	a, b := pair(t)
	base := a.delivery().Recorded

	a.write("2026-08-12", "First.", "", "")
	a.write("2026-08-13", "Second.", "", "")
	a.publish()
	names := manifests(t, a.cloud, a.status().Device)
	second := names[len(names)-1]
	// The second arrives without the first: it is stored and waits.
	carry(t, a, b, func(name string) bool {
		return name == second || strings.HasPrefix(name, syncObjectsName+"/")
	})
	b.pull()
	if pending := b.status().Pending; len(pending) != 1 {
		t.Fatalf("pending = %+v, want the transaction that arrived early", pending)
	}
	b.receipt()
	carry(t, b, a, nil)
	if got := a.appliedThere(); got != base {
		t.Fatalf("applied = %d while a transaction waits, want %d", got, base)
	}

	carry(t, a, b, nil)
	requireNoDiagnostics(t, b.pull())
	b.receipt()
	carry(t, b, a, nil)
	if got := a.appliedThere(); got != base+2 {
		t.Fatalf("applied = %d once both arrived, want %d", got, base+2)
	}
}

func TestDamagedForeignAndForgedReceiptsAreNotCounted(t *testing.T) {
	a, b := pair(t)
	recorded := a.delivery().Recorded
	b.receipt()
	carry(t, b, a, nil)
	if got := a.appliedThere(); got != recorded {
		t.Fatalf("applied = %d, want %d", got, recorded)
	}

	phone := b.status().Device
	dir := filepath.Join(a.cloud, syncReceiptsName, phone)
	plant := func(receipt SyncReceipt, name string) {
		t.Helper()
		data, err := encodeSyncJSON(receipt)
		if err != nil {
			t.Fatal(err)
		}
		if name == "" {
			name = SyncTransactionID(data)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	claim := map[string]int{a.status().Device: recorded + 40}
	// Cut short on the way, another journal's, signed by another device, and
	// one claiming more than was ever recorded.
	if err := os.WriteFile(filepath.Join(dir, strings.Repeat("a", 64)+".json"), []byte(`{"protocol": 1, "jour`), 0644); err != nil {
		t.Fatal(err)
	}
	plant(SyncReceipt{Protocol: SyncProtocolVersion, Journal: strings.Repeat("0", 32), Device: phone, Applied: claim}, "")
	plant(SyncReceipt{Protocol: SyncProtocolVersion, Journal: a.status().Journal, Device: strings.Repeat("1", 32), Applied: claim}, "")
	plant(SyncReceipt{Protocol: SyncProtocolVersion, Journal: a.status().Journal, Device: phone, Applied: claim}, strings.Repeat("b", 64))
	if got := a.appliedThere(); got != recorded {
		t.Fatalf("applied = %d after planted receipts, want %d", got, recorded)
	}
	plant(SyncReceipt{Protocol: SyncProtocolVersion, Journal: a.status().Journal, Device: phone, Applied: claim}, "")
	if got := a.appliedThere(); got != recorded {
		t.Fatalf("applied = %d, want no more than the %d ever recorded", got, recorded)
	}
}

func TestInspectingDeliveryChangesNothing(t *testing.T) {
	a, b := pair(t)
	a.write("2026-08-12", "Mine.", "", "")
	b.receipt()
	carry(t, b, a, nil)

	before, cloud := a.tree(), readTree(t, a.cloud)
	inspected, err := InspectSyncDelivery(a.reflections(), a.exchange())
	if err != nil {
		t.Fatalf("InspectSyncDelivery returned error: %v", err)
	}
	if want := a.delivery(); inspected.Recorded != want.Recorded || inspected.Published != want.Published ||
		len(inspected.Peers) != 1 || inspected.Peers[0] != want.Peers[0] {
		t.Fatalf("InspectSyncDelivery = %+v, want %+v", inspected, want)
	}
	if !sameTree(before, a.tree()) || !sameTree(cloud, readTree(t, a.cloud)) {
		t.Fatal("inspecting delivery changed the journal or the exchange")
	}

	// Nowhere to exchange through: everything is saved here and nothing more.
	local, err := InspectSyncDelivery(a.reflections(), nil)
	if err != nil || local.Recorded != inspected.Recorded || local.Published != 0 {
		t.Fatalf("with no exchange InspectSyncDelivery = %+v (%v)", local, err)
	}
	empty := &DirectoryTransport{root: t.TempDir()}
	if published, err := a.journal.PublishSyncReceipt(empty); err != nil || published.ID != "" {
		t.Fatalf("a receipt for an exchange with no journal = %+v (%v), want none", published, err)
	}
}
