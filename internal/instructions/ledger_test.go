package instructions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/matjam/gandalf/internal/vault"
)

// remove deletes a seeded document from the vault, as a user would.
func remove(t *testing.T, v *vault.Vault, rel string) {
	t.Helper()

	if err := os.Remove(filepath.Join(v.Root(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %q: %v", rel, err)
	}
}

// TestDeletedDocumentsStayDeleted is the point of the ledger. A user who
// deletes a standard has made a decision, and restoring it on the next run
// would revert that decision exactly as overwriting an edit would.
func TestDeletedDocumentsStayDeleted(t *testing.T) {
	v := seeded(t)
	remove(t, v, "Standards/database.md")

	results, err := Seed(v, on(t), false)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if v.Exists("Standards/database.md") {
		t.Fatal("seeding restored a document the user deleted")
	}
	if got := Removed(results); got != 1 {
		t.Errorf("removed = %d, want 1", got)
	}
	if got := Created(results); got != 0 {
		t.Errorf("created = %d, want 0", got)
	}

	// Still true after several runs, which is when it would matter.
	for range 3 {
		if _, err := Seed(v, on(t), false); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}
	if v.Exists("Standards/database.md") {
		t.Error("a later run restored the deleted document")
	}
}

func TestRestorePutsDeletedDocumentsBack(t *testing.T) {
	v := seeded(t)
	remove(t, v, "Standards/database.md")

	results, err := Seed(v, on(t), true)
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if !v.Exists("Standards/database.md") {
		t.Fatal("restore did not put the document back")
	}
	if got := Created(results); got != 1 {
		t.Errorf("created = %d, want 1", got)
	}
}

// TestLedgerAdoptsPreexistingDocuments covers a vault seeded before the ledger
// existed: its documents must be recorded, or deleting one afterwards would
// look like it had never been seeded and get restored.
func TestLedgerAdoptsPreexistingDocuments(t *testing.T) {
	v := seeded(t)

	if err := os.Remove(filepath.Join(v.Root(), filepath.FromSlash(LedgerPath))); err != nil {
		t.Fatalf("remove ledger: %v", err)
	}

	// A run with the ledger missing should re-record what is already there.
	if _, err := Seed(v, on(t), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	ledger, err := LoadLedger(v)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	for _, doc := range Docs() {
		if !ledger.Records(doc.ID) {
			t.Errorf("%s was not adopted into the ledger", doc.ID)
		}
	}

	remove(t, v, "Standards/database.md")
	if _, err := Seed(v, on(t), false); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if v.Exists("Standards/database.md") {
		t.Error("a deletion after adoption was reverted")
	}
}

func TestLedgerIsNotPartOfTheVaultsNotes(t *testing.T) {
	v := seeded(t)

	paths, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range paths {
		if p == LedgerPath {
			t.Error("the ledger is listed as a note")
		}
	}

	// It must also not produce lint findings, being machinery rather than memory.
	findings, err := v.Lint()
	if err != nil {
		t.Fatalf("Lint: %v", err)
	}
	if len(findings) > 0 {
		t.Errorf("seeded vault is not clean: %+v", findings)
	}
}

// TestCorruptLedgerIsAnError checks the failure is loud. Treating an unreadable
// ledger as empty would silently restore everything the user had deleted.
func TestCorruptLedgerIsAnError(t *testing.T) {
	v := seeded(t)

	abs := filepath.Join(v.Root(), filepath.FromSlash(LedgerPath))
	if err := os.WriteFile(abs, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Seed(v, on(t), false); err == nil {
		t.Error("seeding accepted a corrupt ledger")
	}
	if _, err := Doctor(v); err == nil {
		t.Error("doctor accepted a corrupt ledger")
	}
}

func TestLedgerSurvivesReload(t *testing.T) {
	v := seeded(t)

	ledger, err := LoadLedger(v)
	if err != nil {
		t.Fatalf("LoadLedger: %v", err)
	}
	if len(ledger.Seeded) != len(Docs()) {
		t.Errorf("ledger records %d documents, want %d", len(ledger.Seeded), len(Docs()))
	}
	for _, doc := range Docs() {
		if !ledger.Records(doc.ID) {
			t.Errorf("%s is not recorded", doc.ID)
		}
	}
	if got := ledger.Seeded["operating"]; got != on(t).String() {
		t.Errorf("seeded date = %q, want %q", got, on(t))
	}
}
