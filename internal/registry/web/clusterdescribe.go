// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Naming the knowledge-map clusters with a language model.
//
// techmap detects the clusters and names each from its dominant tag — an honest,
// free default. This adds the richer layer the operator asked for: a short name
// and a one-sentence description per cluster, from the model. It follows the
// same shape as the tag-merge namer — reuse the registry's configured client,
// disable thinking (a short structured answer), cache the result — but it does
// NOT gate behind a review step: a cluster label is cosmetic (it renames nothing,
// touches no technique), so the operator runs it and the map shows it.
package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/opentacit/tacit/internal/registry/suggest"
	"github.com/opentacit/tacit/internal/registry/techmap"
)

// clusterModel is the language model the namer runs against — an interface so
// the flow is testable without a network.
type clusterModel interface {
	Complete(prompt string, maxTokens int) (string, error)
}

type clusterLabel struct{ Name, Desc string }

func (s *Server) clusterNamer() (clusterModel, error) {
	if s.ClusterNamer != nil {
		return s.ClusterNamer, nil
	}
	return suggest.FromEnv()
}

// clusterSig identifies a cluster by its members, independent of layout or id,
// so a filtered view reuses the label of a cluster with the same techniques.
func clusterSig(c techmap.Cluster, nodes []techmap.Node) string {
	ids := make([]string, len(c.Members))
	for i, m := range c.Members {
		ids[i] = nodes[m].ID
	}
	sort.Strings(ids)
	return strings.Join(ids, "|")
}

// applyClusterLabels overwrites each cluster's Name/Desc from the cache, where a
// previous describe run left one. Clusters without a cached label keep their
// tag-derived name and an empty description.
func (s *Server) applyClusterLabels(g *techmap.Graph) {
	s.clusterMu.Lock()
	defer s.clusterMu.Unlock()
	for i := range g.Clusters {
		if g.Clusters[i].Ungrouped {
			continue // the bucket's name is fixed; no label may shadow it
		}
		if lab, ok := s.clusterCache[clusterSig(g.Clusters[i], g.Nodes)]; ok {
			g.Clusters[i].Name = lab.Name
			g.Clusters[i].Desc = lab.Desc
		}
	}
}

// handleClusterDescribe rebuilds the current view's clusters, asks the model to
// name and describe them, caches the result, and returns to the map. On failure
// it returns with ?describe=error so the page can say so.
func (s *Server) handleClusterDescribe(w http.ResponseWriter, r *http.Request) {
	back := "/techniques/map"
	if r.URL.RawQuery != "" {
		back += "?" + r.URL.RawQuery
	}
	// A model call the caller pays for, and a cache every reader of the map
	// then sees: session-gated like the other browser actions.
	if !s.signedInOrRedirect(w, r, back) {
		return
	}
	// Say which of the four things went wrong, and log the reason.
	//
	// All four used to redirect to one message — "could not reach the model,
	// check TACIT_LLM_API_KEY" — including the two that have nothing to do with
	// the key. An operator who has just set a key correctly is then told to
	// check it, which is worse than silence: it sends them to fix the one thing
	// that was already right.
	fail := func(why string, err error) {
		log.Printf("[describe] %s: %v", why, err)
		u := "/techniques/map?describe=" + why
		if q := r.URL.RawQuery; q != "" {
			u = "/techniques/map?" + q + "&describe=" + why
		}
		http.Redirect(w, r, u, http.StatusFound)
	}

	in, err := s.readAnalyticsInputs(false)
	if err != nil {
		fail("nodata", err)
		return
	}
	techniques, events := in.techniques, in.events
	g := techmap.Build(s.mapTechniquesForRequest(r, techniques), events)
	if len(g.Clusters) == 0 {
		http.Redirect(w, r, back, http.StatusFound)
		return
	}
	m, err := s.clusterNamer()
	if err != nil {
		fail("nokey", err)
		return
	}
	labels, err := describeClusters(m, g.Clusters, g.Nodes)
	if err != nil {
		fail("model", err)
		return
	}
	s.clusterMu.Lock()
	if s.clusterCache == nil {
		s.clusterCache = map[string]clusterLabel{}
	}
	for id, lab := range labels {
		for _, c := range g.Clusters {
			if c.ID == id {
				s.clusterCache[clusterSig(c, g.Nodes)] = lab
			}
		}
	}
	s.clusterMu.Unlock()
	http.Redirect(w, r, back, http.StatusFound)
}

// describeClusters asks the model for a name and one-line description per cluster
// and returns them keyed by cluster id. Anything the model omits or returns for a
// cluster that does not exist is dropped; those clusters keep their tag name.
func describeClusters(m clusterModel, clusters []techmap.Cluster, nodes []techmap.Node) (map[int]clusterLabel, error) {
	text, err := m.Complete(clusterPrompt(clusters, nodes), 2048)
	if err != nil {
		return nil, err
	}
	start, end := strings.Index(text, "["), strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no JSON array in the naming response (%.200s)", text)
	}
	var raw []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
		Desc string `json:"desc"`
	}
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("naming response JSON: %w", err)
	}
	valid := map[int]bool{}
	for _, c := range clusters {
		valid[c.ID] = !c.Ungrouped // the Ungrouped bucket keeps its honest name
	}
	out := map[int]clusterLabel{}
	for _, r := range raw {
		name := strings.TrimSpace(r.Name)
		if valid[r.ID] && name != "" {
			out[r.ID] = clusterLabel{Name: name, Desc: strings.TrimSpace(r.Desc)}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the model returned no valid area names")
	}
	return out, nil
}

// clusterPrompt lays the clusters out for the model — each area's tags and the
// names of its techniques, which carry the real meaning.
func clusterPrompt(clusters []techmap.Cluster, nodes []techmap.Node) string {
	var b strings.Builder
	b.WriteString(`You are naming the areas of practice in an organization's library of AI-agent
techniques. Each area below is a cluster of techniques that share tags.

Give each area a short NAME (2–4 words, Title Case) and a one-sentence DESCRIPTION
of what its techniques have in common. Judge by the technique names, not only
the tags. Names should be distinct from each other.

AREAS:
`)
	for _, c := range clusters {
		if c.Ungrouped {
			continue // not an area of practice — nothing to name
		}
		fmt.Fprintf(&b, "[id %d] tags: %s\n", c.ID, strings.Join(c.Tags, ", "))
		shown := 0
		for _, m := range c.Members {
			if shown >= 9 {
				fmt.Fprintf(&b, "  … and %d more\n", len(c.Members)-shown)
				break
			}
			fmt.Fprintf(&b, "  - %s\n", nodes[m].Name)
			shown++
		}
	}
	b.WriteString(`
Respond with a JSON array and nothing else, one object per area:
[{"id": <id>, "name": "<short name>", "desc": "<one sentence>"}]`)
	return b.String()
}
