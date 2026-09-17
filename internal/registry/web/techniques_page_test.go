// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
)

func techniqueFixture() []models.Technique {
	return []models.Technique{
		{ID: "live-one", Name: "Live One", Status: "reviewed", Tags: []string{"sql"}, Provenance: "curated"},
		{ID: "live-two", Name: "Live Two", Status: "reviewed", Tags: []string{"diagram"}, Provenance: "contributed", Scope: "org"},
		{ID: "a-draft", Name: "A Draft", Status: "draft"},
		{ID: "a-shadow", Name: "A Shadow", Status: "shadow"},
		{ID: "an-old-one", Name: "An Old One", Status: "retired", Tags: []string{"sql"}},
	}
}

func ids(rows []capListRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.rawid
	}
	return out
}

// The default view is the live set: drafts live under Review, shadows are under
// evaluation, and the retired are one click away in the archive — counted, not
// listed.
func TestCollectTechniqueRowsDefaultsToTheLiveSet(t *testing.T) {
	got := collectTechniqueRows(techniqueFixture(), organizationFilters{}, "", "", false)
	if want := []string{"live-one", "live-two"}; strings.Join(ids(got.rows), ",") != strings.Join(want, ",") {
		t.Errorf("rows = %v, want %v", ids(got.rows), want)
	}
	if got.shown != 2 {
		t.Errorf("shown = %d, want 2", got.shown)
	}
	if got.retiredCount != 1 {
		t.Errorf("retiredCount = %d, want 1", got.retiredCount)
	}
	if len(got.live) != 2 {
		t.Errorf("live set = %d techniques, want 2", len(got.live))
	}
	if got.scopesSeen["org"] != 1 || got.scopesSeen["general"] != 1 {
		t.Errorf("scopesSeen = %v, want one org and one general", got.scopesSeen)
	}
}

// The archive shows the retired and nothing else, and its rows carry the
// Restore action. It is not part of the live set, so grouping gets nothing.
func TestCollectTechniqueRowsArchiveShowsOnlyTheRetired(t *testing.T) {
	got := collectTechniqueRows(techniqueFixture(), organizationFilters{}, "retired", "", true)
	if want := []string{"an-old-one"}; strings.Join(ids(got.rows), ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", ids(got.rows), want)
	}
	if !strings.Contains(got.rows[0].restore, "/admin/techniques/restore/an-old-one") {
		t.Errorf("archive row missing its Restore action: %q", got.rows[0].restore)
	}
	if len(got.live) != 0 {
		t.Errorf("archive contributed %d techniques to the live set, want 0", len(got.live))
	}
}

// Within a dimension the selected values union; across dimensions they
// intersect — the same rule the filter uses on Outcomes.
func TestCollectTechniqueRowsAppliesTheFilters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		filters organizationFilters
		want    string
	}{
		{"by tag", organizationFilters{Tag: []string{"sql"}}, "live-one"},
		{"by scope", organizationFilters{Scope: []string{"org"}}, "live-two"},
		{"by provenance", organizationFilters{Provenance: []string{"curated"}}, "live-one"},
		{"two dimensions intersect", organizationFilters{Tag: []string{"sql"}, Scope: []string{"org"}}, ""},
		{"values within one dimension union", organizationFilters{Tag: []string{"sql", "diagram"}}, "live-one,live-two"},
		{"no match", organizationFilters{Tag: []string{"nothing"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := collectTechniqueRows(techniqueFixture(), tc.filters, "", "", false)
			if joined := strings.Join(ids(got.rows), ","); joined != tc.want {
				t.Errorf("rows = %q, want %q", joined, tc.want)
			}
		})
	}
}

// The row's hidden search string spans the technique's own fields. It used to
// carry the technique's helped rate too, which cost one store read per row for
// text no cell showed; collectTechniqueRows now reads no store at all.
func TestCollectTechniqueRowsSearchKeyCoversTheTechniqueOnly(t *testing.T) {
	techniques := []models.Technique{{
		ID: "one", Name: "One", Status: "reviewed", Description: "a description",
		AppliesWhen: "when it applies", NotWhen: "when it does not", Tags: []string{"sql"},
	}}
	got := collectTechniqueRows(techniques, organizationFilters{}, "", "", false)
	key := got.rows[0].searchKey
	for _, want := range []string{"one", "a description", "when it applies", "when it does not", "sql"} {
		if !strings.Contains(key, want) {
			t.Errorf("searchKey = %q, missing %q", key, want)
		}
	}
	if strings.Contains(key, "helped") || strings.Contains(key, "n=") {
		t.Errorf("searchKey still carries outcome text: %q", key)
	}
}
