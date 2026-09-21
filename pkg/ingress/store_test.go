// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package ingress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The route table is the only state whose loss renames every tenant: an instance
// that cannot be resolved from its key enrols again and is given a new hostname,
// and every address anybody wrote down stops working. These three tests are the
// three ways it was losable.

func TestACorruptRouteTableIsMovedAsideRatherThanStoppingTheIngress(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "instances.json")

	first, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	in, _, err := first.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// A half-written file is what a full disk or a crash mid-rename leaves.
	if err := os.WriteFile(path, []byte(`[{"name":"olive-har`), 0o600); err != nil {
		t.Fatalf("corrupt the table: %v", err)
	}

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("the ingress refused to start over one unreadable file: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, ok := s.Get(in.Name); !ok {
		t.Errorf("instance %s was lost; the spare copy holds it", in.Name)
	}
	if s.Warning() == "" {
		t.Error("nothing was said about the unreadable table; an operator has to be told")
	}
	aside, _ := filepath.Glob(path + ".corrupt-*")
	if len(aside) != 1 {
		t.Errorf("found %d files moved aside, want the bad one kept for inspection", len(aside))
	}
}

func TestHandshakesCoalesceInsteadOfRewritingTheTableEachTime(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	in, _, err := s.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}

	for i := range 20 {
		s.Touch(in.Name, fmt.Sprintf("build-%d", i), "127.0.0.1:9999", false)
	}
	if v := versionOnDisk(t, dir, in.Name); v != "" {
		t.Errorf("the table was rewritten during the burst (%q on disk); a reconnect storm "+
			"must not serialize one whole-file rewrite per handshake", v)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if v := versionOnDisk(t, dir, in.Name); v != "build-19" {
		t.Errorf("version on disk = %q, want the last handshake's", v)
	}
}

func TestASaveThatFailsLeavesTheTableAsItWas(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only directory anyway")
	}
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	in, _, err := s.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if err := s.SetOwnerNote(in.Name, "Someone@Example.com", "the finance team"); err == nil {
		t.Fatal("SetOwnerNote reported success on a table it could not write")
	}
	got, _ := s.Get(in.Name)
	if got.Owner != "" || got.Note != "" {
		t.Errorf("owner %q and note %q survived a failed save; memory now disagrees with the file",
			got.Owner, got.Note)
	}
}

// versionOnDisk reads the version the route table FILE records for one instance,
// which is the only way to tell a save that happened from one that was owed.
func versionOnDisk(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "instances.json"))
	if err != nil {
		t.Fatalf("read the table: %v", err)
	}
	var list []Instance
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("parse the table: %v", err)
	}
	for _, in := range list {
		if in.Name == name {
			return in.Version
		}
	}
	t.Fatalf("instance %s is not in the table on disk", name)
	return ""
}

// A fleet-wide decision is one write, not one write per name. The route table
// is rewritten in full on every save, so suspending everything a console's
// filter matched used to cost a rewrite of the whole table per instance and
// held the write lock for all of it — minutes, on a fleet worth filtering.
//
// The check is a ratio rather than a stopwatch: the same number of instances
// down each path, so a slow machine slows both and the test still means what it
// says.
func TestABulkDecisionIsOneWriteNotOnePerName(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	const each = 40
	names := make([]string, 0, 2*each)
	for range 2 * each {
		in, _, err := s.Enroll(NewKey(), 0)
		if err != nil {
			t.Fatalf("enrol: %v", err)
		}
		names = append(names, in.Name)
	}
	oneAtATime, asASet := names[:each], names[each:]

	start := time.Now()
	for _, name := range oneAtATime {
		if err := s.SetDisabled(name, true); err != nil {
			t.Fatalf("suspend %s: %v", name, err)
		}
	}
	perName := time.Since(start)

	start = time.Now()
	changed, err := s.SetDisabledMany(asASet, true)
	if err != nil {
		t.Fatalf("suspend the set: %v", err)
	}
	asOne := time.Since(start)

	if len(changed) != each {
		t.Errorf("the set suspended %d of %d", len(changed), each)
	}
	for _, name := range names {
		if in, ok := s.Get(name); !ok || !in.Disabled {
			t.Fatalf("%s did not take the decision", name)
		}
	}
	// The same count down each path. One save against forty of them is orders
	// of magnitude; five is the floor that cannot be luck on a loaded machine.
	if asOne*5 > perName {
		t.Errorf("the set took %v and the same names one at a time took %v — "+
			"this is saving the route table per name again", asOne, perName)
	}

	// A name that went between the list being drawn and the decision being made
	// is skipped, not fatal: the rest of the operator's selection still lands.
	if err := s.Delete(names[0]); err != nil {
		t.Fatalf("delete: %v", err)
	}
	back, err := s.SetDisabledMany([]string{names[0], names[1]}, false)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(back) != 1 || back[0] != names[1] {
		t.Errorf("a departed name was not skipped: %v", back)
	}

	dropped, err := s.DeleteMany([]string{names[1], names[2], "never-enrolled"})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(dropped) != 2 {
		t.Errorf("released %d names, expected the two that were there: %v", len(dropped), dropped)
	}
	for _, name := range dropped {
		if _, ok := s.Get(name); ok {
			t.Errorf("%s was released and is still held", name)
		}
	}

	// The set's one save is a real save: what it decided survives a reopen.
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// The sweep reclaims test names and cannot reach anything else. A registry
// coming back after a quiet quarter to find its address reissued is the failure
// this whole service exists to prevent, so the rule is not "idle" — it is
// "idle AND it told us it was a test".
func TestOnlyDeclaredTestNamesAreEverSwept(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	long, quiet := time.Now().Add(-90*24*time.Hour), time.Now().Add(-30*24*time.Hour)
	name := func(version string, test bool, seen time.Time) string {
		in, _, err := s.Enroll(NewKey(), 0)
		if err != nil {
			t.Fatalf("enrol: %v", err)
		}
		s.Touch(in.Name, version, "10.0.0.1", test)
		// Reach past Touch's own clock: what is being tested is age.
		s.mu.Lock()
		s.byID[in.Name].LastSeen = seen
		s.mu.Unlock()
		return in.Name
	}

	oldTest := name("v1", true, long)
	freshTest := name("v1", true, time.Now())
	oldReal := name("v1", false, long)      // a tenant that has been away for a quarter
	quietReal := name("v1", false, quiet)   // and one away for a month
	suspendedTest := name("v1", true, long) // declared a test, but somebody decided about it
	if err := s.SetDisabled(suspendedTest, true); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	dropped, err := s.ExpireTests(time.Now().Add(-60 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != oldTest {
		t.Fatalf("the sweep released %v, expected only %s", dropped, oldTest)
	}
	for _, kept := range []struct {
		name, why string
	}{
		{oldReal, "a registry that never said it was a test, away for a quarter"},
		{quietReal, "a registry that never said it was a test, away for a month"},
		{freshTest, "a test that is still connecting"},
		{suspendedTest, "a suspended name, which somebody decided about"},
	} {
		if _, ok := s.Get(kept.name); !ok {
			t.Errorf("the sweep took %s — %s", kept.name, kept.why)
		}
	}

	// A test that enrolled and never came back has no last-seen at all. Its
	// enrolment is the last thing known about it, and it ages from there.
	in, _, err := s.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	s.mu.Lock()
	s.byID[in.Name].Test, s.byID[in.Name].Created = true, long
	s.mu.Unlock()
	dropped, err = s.ExpireTests(time.Now().Add(-60 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != in.Name {
		t.Errorf("a test that never came back was not swept: %v", dropped)
	}

	// Nothing to do is not an error, and must not write.
	if dropped, err := s.ExpireTests(time.Now().Add(-60 * 24 * time.Hour)); err != nil || dropped != nil {
		t.Errorf("an empty sweep reported %v, %v", dropped, err)
	}
}

// The flag is a claim about what a registry IS, so it is taken from every
// handshake. A scratch instance rebuilt as somebody's registry says so on its
// next connect, and its name stops being reclaimable from that moment.
func TestTheTestFlagFollowsTheLatestHandshake(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	in, _, err := s.Enroll(NewKey(), 0)
	if err != nil {
		t.Fatalf("enrol: %v", err)
	}
	for _, step := range []struct {
		test bool
		why  string
	}{
		{true, "declared a test on its first connect"},
		{true, "said it again"},
		{false, "came back as somebody's registry"},
		{true, "and was pointed at a test again"},
	} {
		s.Touch(in.Name, "v1", "10.0.0.1", step.test)
		got, ok := s.Get(in.Name)
		if !ok || got.Test != step.test {
			t.Errorf("%s: the store says test=%v", step.why, got.Test)
		}
	}
}
