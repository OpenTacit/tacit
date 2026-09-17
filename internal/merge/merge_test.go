// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentacit/tacit/pkg/contracts"
)

func sampleTechnique() contracts.Technique {
	return contracts.Technique{
		ID:             "run-the-thing",
		Name:           "Run the thing",
		Description:    "Start it before you describe it.",
		Recipe:         "Run it, look at the output, then answer.",
		Scope:          "general",
		Status:         "stable",
		Provenance:     "mined",
		Version:        7,
		Tags:           []string{"verification"},
		TaskTypes:      []string{"debug"},
		AppliesWhen:    "there is something runnable",
		Embedding:      []float32{0.1, 0.2},
		EmbeddingModel: "onnx-v1",
		Source:         "mined",
		Channels:       []string{"public"},
		CreatedAt:      "2026-06-12T09:00:00Z",
	}
}

// The sender's whole job is what to leave out, so this is the test that matters
// most: a field that should not cross must not cross.
func TestContributionCarriesNoLocalIdentityOrStanding(t *testing.T) {
	body := Contribution(sampleTechnique(), "")
	for _, field := range []string{
		"id", "status", "provenance", "version", "embedding", "embedding_model",
		"source", "channels", "created_at", "updated_at", "origin", "decay_signal",
	} {
		if _, present := body[field]; present {
			t.Errorf("%q crossed the border; the destination sets or ignores it", field)
		}
	}
	if body["name"] != "Run the thing" || body["scope"] != "general" {
		t.Errorf("the technique itself did not survive: %v", body)
	}
	if body["applies_when"] != "there is something runnable" {
		t.Errorf("applies_when = %v, want it carried", body["applies_when"])
	}
}

// Empty optional fields stay out entirely rather than arriving as empty keys a
// reviewer has to read past.
func TestContributionOmitsEmptyOptionalFields(t *testing.T) {
	bare := contracts.Technique{Name: "N", Description: "D", Recipe: "R", Scope: "org"}
	body := Contribution(bare, "")
	for _, field := range []string{"before_after", "not_when", "shipped", "tags", "task_types"} {
		if _, present := body[field]; present {
			t.Errorf("%q is present but empty", field)
		}
	}
}

func TestStandingRidesInTheDescriptionAsAClaim(t *testing.T) {
	standing := StandingNote(contracts.Outcome{Shown: 30, Adopted: 22, Helped: 14}, "12 June 2026")
	if !strings.Contains(standing, "30 shown") || !strings.Contains(standing, "12 June 2026") {
		t.Fatalf("standing = %q, want the contributor's figures", standing)
	}
	// The disclaimer is the point of the sentence: these numbers are not the
	// organization's, and a reviewer must not read them as measured here.
	if !strings.Contains(standing, "Not measured by this organization") {
		t.Errorf("standing = %q, want it disclaimed", standing)
	}
	body := Contribution(sampleTechnique(), standing)
	desc, _ := body["description"].(string)
	if !strings.Contains(desc, "Start it before you describe it.") || !strings.Contains(desc, "30 shown") {
		t.Errorf("description = %q, want the original followed by the claim", desc)
	}
	// It must not become a number anywhere the destination could roll up.
	for _, field := range []string{"shown", "adopted", "helped", "outcomes"} {
		if _, present := body[field]; present {
			t.Errorf("%q crossed as data; evidence travels only as prose", field)
		}
	}
}

// "0 shown, 0 adopted" is not evidence and reads like a warning.
func TestAnUnusedTechniqueGetsNoStanding(t *testing.T) {
	if s := StandingNote(contracts.Outcome{}, "12 June 2026"); s != "" {
		t.Errorf("standing = %q, want nothing for an unused technique", s)
	}
}

func TestLedgerMakesASecondRunSendNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "personal", LedgerName)
	l, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := l.Record("run-the-thing", "https://org.example", "run-the-thing"); err != nil {
		t.Fatalf("record: %v", err)
	}

	reopened, err := OpenLedger(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	sent, ok := reopened.AlreadySent("run-the-thing", "https://org.example")
	if !ok {
		t.Fatal("the ledger forgot a technique it recorded; a second run would send it again")
	}
	if sent.DraftID != "run-the-thing" {
		t.Errorf("draft id = %q", sent.DraftID)
	}
	// A different organization is a contribution, not a re-contribution.
	if _, ok := reopened.AlreadySent("run-the-thing", "https://other.example"); ok {
		t.Error("the ledger claims this went to an organization it never went to")
	}
}

// A ledger that cannot be read must stop the run: treating it as empty would
// re-send everything it recorded, and UniqueID would duplicate all of it.
func TestAnUnreadableLedgerIsAnErrorNotAFreshStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), LedgerName)
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLedger(path); err == nil {
		t.Fatal("a corrupt ledger was read as empty")
	}
}

func TestOutcomesCountWhatTheMemberActuallyGot(t *testing.T) {
	events := []contracts.FeedbackEvent{
		{TechniqueID: "a", Stage: "shown", CreatedAt: "2026-06-12T09:00:00Z"},
		{TechniqueID: "a", Stage: "shown", CreatedAt: "2026-06-13T09:00:00Z"},
		{TechniqueID: "a", Stage: "adopted", CreatedAt: "2026-06-13T09:01:00Z"},
		{TechniqueID: "a", Stage: "helped", Value: true, CreatedAt: "2026-06-13T09:02:00Z"},
		// A member saying it did NOT help is not a help.
		{TechniqueID: "a", Stage: "helped", Value: false, CreatedAt: "2026-06-14T09:00:00Z"},
		// Shadow stages measure techniques nobody was shown.
		{TechniqueID: "a", Stage: "shadow_shown", CreatedAt: "2026-06-15T09:00:00Z"},
		{TechniqueID: "b", Stage: "dismissed", CreatedAt: "2026-06-16T09:00:00Z"},
	}
	got := OutcomeIndex(events)
	a := got["a"]
	if a.Shown != 2 || a.Adopted != 1 || a.Helped != 1 {
		t.Errorf("a = %d shown, %d adopted, %d helped; want 2/1/1", a.Shown, a.Adopted, a.Helped)
	}
	if got["b"].Dismissed != 1 {
		t.Errorf("b dismissed = %d, want 1", got["b"].Dismissed)
	}
	if since := FirstSeen(events); since != "12 June 2026" {
		t.Errorf("first seen = %q, want 12 June 2026", since)
	}
}

// The archive is the answer to "retiring the registry destroys the evidence".
func TestArchiveHoldsTheEvidenceThatDoesNotTravel(t *testing.T) {
	events := []contracts.FeedbackEvent{{TechniqueID: "a", Stage: "shown", CreatedAt: "2026-06-12T09:00:00Z"}}
	a := NewArchive("http://127.0.0.1:8081", "https://org.example",
		[]contracts.Technique{sampleTechnique()}, events,
		map[string]Sent{"run-the-thing": {Destination: "https://org.example", DraftID: "run-the-thing"}})
	if len(a.Events) != 1 || len(a.Outcomes) != 1 || len(a.Techniques) != 1 {
		t.Fatalf("archive = %d events, %d outcomes, %d techniques", len(a.Events), len(a.Outcomes), len(a.Techniques))
	}
	if !strings.Contains(a.Note, "sample of one") {
		t.Errorf("the archive does not say why it exists: %q", a.Note)
	}
	path := filepath.Join(t.TempDir(), ArchiveName(time.Now()))
	if err := a.Write(path); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.HasSuffix(path, ".json") || !strings.Contains(path, "tacit-personal-archive-") {
		t.Errorf("archive name %q does not say what the file is", path)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// A merged registry's data must not be reused: starting again from it would BE
// the merged registry, serving a playbook that has already moved. Its
// configuration is the machine's rather than the instance's, and stays — the
// settings, and the ledger's record of where the work went.
func TestAMergedRegistryLosesItsDataAndKeepsItsConfig(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config", "personal")
	dataHome := filepath.Join(root, "share")
	dataDir := filepath.Join(dataHome, "personal", "data")
	techDir := filepath.Join(dataHome, "personal", "techniques")
	for _, d := range []string{cfgDir, dataDir, techDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFile(filepath.Join(cfgDir, "registry.env"), "TACIT_OWNER_SECRET=s\n"); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(dataDir, "techniques.json"), "[]"); err != nil {
		t.Fatal(err)
	}
	l, err := OpenLedger(LedgerPath(filepath.Join(cfgDir, "registry.env")))
	if err != nil {
		t.Fatal(err)
	}
	l.Destination = "https://org.example"
	if err := l.Record("a", "https://org.example", "a"); err != nil {
		t.Fatal(err)
	}

	// Not retired yet: a merge that only contributed leaves the registry alone.
	if moved, err := SweepMerged(cfgDir, dataDir, techDir, dataHome); err != nil || moved != nil {
		t.Fatalf("swept a live profile: %v %v", moved, err)
	}
	if !fileThere(filepath.Join(cfgDir, "registry.env")) {
		t.Fatal("a profile that was only contributed from was moved aside")
	}

	if err := l.MarkRetired(); err != nil {
		t.Fatal(err)
	}
	if !IsRetired(cfgDir) {
		t.Fatal("the profile does not report itself retired")
	}
	moved, err := SweepMerged(cfgDir, dataDir, techDir, dataHome)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(moved) != 3 {
		t.Fatalf("moved %v, want the data, the techniques and the ledger", moved)
	}
	for _, gone := range []string{dataDir, techDir} {
		if fileThere(gone) {
			t.Errorf("%s is still in place; the next registry would be the merged one", gone)
		}
	}
	// The machine's own: settings it did not ask to lose, and an API key that
	// sweeping would take with it.
	if !fileThere(filepath.Join(cfgDir, "registry.env")) {
		t.Error("the sweep took the machine's settings with the instance")
	}
	// Moved, never deleted: the member's own measurements are in there.
	if !fileThere(filepath.Join(moved[0], "techniques.json")) {
		t.Errorf("the data did not survive the move to %s", moved[0])
	}
	// And a swept registry has nothing left to report: the ledger that said it
	// was finished is beside it under a name of its own.
	if IsRetired(cfgDir) {
		t.Error("the swept registry still reads as retired; it would never start")
	}
}

// A registry can be pointed at a shared techniques directory — the repo's own,
// on a developer's machine. Renaming that would take something else's files.
func TestSweepLeavesATechniquesDirectoryItDoesNotOwn(t *testing.T) {
	root := t.TempDir()
	cfgDir := filepath.Join(root, "config", "personal")
	dataHome := filepath.Join(root, "share")
	dataDir := filepath.Join(dataHome, "personal", "data")
	shared := filepath.Join(root, "checkout", "techniques")
	for _, d := range []string{cfgDir, dataDir, shared} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	l, _ := OpenLedger(LedgerPath(filepath.Join(cfgDir, "registry.env")))
	l.Destination = "https://org.example"
	l.Sent["a"] = Sent{Destination: "https://org.example", DraftID: "a"}
	if err := l.MarkRetired(); err != nil {
		t.Fatal(err)
	}
	if _, err := SweepMerged(cfgDir, dataDir, shared, dataHome); err != nil {
		t.Fatal(err)
	}
	if !fileThere(shared) {
		t.Error("a shared techniques directory outside the data home was moved")
	}
}

func fileThere(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Both merge surfaces — the command and the browser flow — used to carry their
// own copy of "$HOME plus a timestamp", and fixing one left the other filling
// the home directory. The decision lives in one place now, and these are the
// two halves of it.
func TestArchivePathReusesTheOneAlreadyWritten(t *testing.T) {
	dir := t.TempDir()
	recorded := filepath.Join(dir, "tacit-personal-archive-20260910-000000.json")
	if err := os.WriteFile(recorded, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ArchivePath(dir, recorded); got != recorded {
		t.Fatalf("a retry wrote a second archive: %s", got)
	}
	// A recorded path the member has since deleted falls through to a new one:
	// the archive exists so the evidence survives, and refusing to write it
	// because the old copy is gone gets that exactly backwards.
	gone := filepath.Join(dir, "deleted", "tacit-personal-archive-20260910-000000.json")
	got := ArchivePath(dir, gone)
	if got == gone || got == "" {
		t.Fatalf("a missing recorded archive was reused: %s", got)
	}
	if filepath.Dir(got) != filepath.Join(dir, "archives") {
		t.Fatalf("a fresh archive went to %s, want %s", filepath.Dir(got), filepath.Join(dir, "archives"))
	}
	if !strings.Contains(filepath.Base(got), "tacit-personal-archive-") {
		t.Fatalf("archive lost its identifiable name: %s", got)
	}
}

// No state directory means nothing could say where state goes. The home
// directory is then better than not writing the archive at all, which is the
// one outcome the step it serves refuses.
func TestArchivePathFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := ArchivePath("", "")
	if filepath.Dir(got) != home {
		t.Fatalf("archive path = %s, want a file directly in %s", got, home)
	}
}
