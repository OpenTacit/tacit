// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/opentacit/tacit/internal/registry/models"
	"github.com/opentacit/tacit/internal/registry/techmap"
)

type fakeNamer struct {
	reply, prompt string
	err           error
}

func (f *fakeNamer) Complete(p string, _ int) (string, error) { f.prompt = p; return f.reply, f.err }

func TestDescribeClustersParsesAndValidates(t *testing.T) {
	clusters := []techmap.Cluster{{ID: 0, Members: []int{0, 1}}, {ID: 1, Members: []int{2, 3}}}
	nodes := []techmap.Node{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}}
	m := &fakeNamer{reply: `here:
	[{"id":0,"name":"Review & audit","desc":"Practices that verify AI work."},
	 {"id":1,"name":"","desc":"no name -> dropped"},
	 {"id":9,"name":"Ghost","desc":"cluster that doesn't exist -> dropped"}]`}

	got, err := describeClusters(m, clusters, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Review & audit" || got[0].Desc == "" {
		t.Fatalf("kept the wrong set: %+v", got)
	}
	// The member names reach the model, not just the tag list.
	if !strings.Contains(m.prompt, "- A") || !strings.Contains(m.prompt, "- C") {
		t.Fatal("prompt did not carry the technique names")
	}
}

func TestDescribeClustersSurfacesFailure(t *testing.T) {
	c := []techmap.Cluster{{ID: 0, Members: []int{0}}}
	n := []techmap.Node{{Name: "A"}}
	if _, err := describeClusters(&fakeNamer{err: errors.New("boom")}, c, n); err == nil {
		t.Fatal("a model error must not be reported as success")
	}
	if _, err := describeClusters(&fakeNamer{reply: "not json"}, c, n); err == nil {
		t.Fatal("an unparseable answer must not be reported as success")
	}
	if _, err := describeClusters(&fakeNamer{reply: "[]"}, c, n); err == nil {
		t.Fatal("naming nothing is a failure, not a silent success")
	}
}

// The whole flow: describe caches labels, the map then shows them, and a rebuild
// of the same clusters reuses the cache (keyed by members, not layout).
func TestClusterDescribeFlow(t *testing.T) {
	srv, ts := newServer(t)
	for _, c := range []models.Technique{
		{ID: "cd-a", Name: "Cd A", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "cd-b", Name: "Cd B", Status: "stable", Tags: []string{"review", "audit"}},
		{ID: "cd-c", Name: "Cd C", Status: "stable", Tags: []string{"review", "audit"}},
	} {
		if err := srv.Store.UpsertTechnique(c); err != nil {
			t.Fatal(err)
		}
	}
	// Name every cluster id the detector might assign.
	var reply strings.Builder
	reply.WriteString("[")
	for i := 0; i < 10; i++ {
		if i > 0 {
			reply.WriteString(",")
		}
		reply.WriteString(`{"id":` + itoa(i) + `,"name":"Named ` + itoa(i) + `","desc":"Area ` + itoa(i) + ` description."}`)
	}
	reply.WriteString("]")
	srv.ClusterNamer = &fakeNamer{reply: reply.String()}

	// Before: tag-derived names, no descriptions, button says "Describe with AI".
	_, before := fetchHTML(t, ts.URL+"/techniques/map")
	if strings.Contains(before, "cmap-area-desc") {
		t.Fatal("descriptions present before any describe run")
	}
	if !strings.Contains(before, ">Describe with AI<") {
		t.Fatal("the describe button is missing")
	}

	post(t, ts.URL+"/techniques/map/describe", url.Values{})

	// After: the areas carry LLM names and descriptions, and the button flips.
	_, after := fetchHTML(t, ts.URL+"/techniques/map")
	if !strings.Contains(after, "cmap-area-desc") || !strings.Contains(after, "description.") {
		t.Fatal("the LLM descriptions did not reach the map")
	}
	if !strings.Contains(after, `class="cmap-area-name">Named`) {
		t.Fatal("the LLM names did not replace the tag-derived ones")
	}
	if !strings.Contains(after, ">Re-describe<") {
		t.Fatal("the button should offer to re-describe once names exist")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
