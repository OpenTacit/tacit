// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Model-list discovery for the settings combo box, and explicit-provider
// construction. Both the Anthropic and OpenAI-compatible /models endpoints
// return {"data":[{"id":...}]}, so one parser serves both; the call is
// best-effort with a short timeout — the combo box is a nicety, never on a
// critical path.
package suggest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/opentacit/tacit/internal/llmprovider"
)

// ModelInfo is one model the provider offers, for the settings combo box. Name
// and Description are best-effort: OpenRouter supplies both, Anthropic a display
// name only, and a plain OpenAI-compatible endpoint neither (id only).
type ModelInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

// modelDescLimit caps a description so a 300-model catalog (OpenRouter) stays a
// small JSON payload and the line under the field reads as a subtitle, not a
// paragraph.
const modelDescLimit = 160

// ClientForProvider builds a client for an EXPLICIT provider name from the
// environment, rather than the configured TACIT_LLM_PROVIDER. FromEnv delegates
// here; the settings model-list endpoint uses it to preview any provider's
// catalog. The wire format, default base, and default research model come from
// internal/llmprovider. Returns ErrNoResearcher when no key is set.
func ClientForProvider(provider string) (Client, error) {
	key := strings.TrimSpace(os.Getenv("TACIT_LLM_API_KEY"))
	if key == "" {
		return nil, ErrNoResearcher
	}
	p := llmprovider.Lookup(provider)
	base := llmprovider.BaseURLFor(provider, os.Getenv("TACIT_LLM_BASE_URL"))
	model := os.Getenv("TACIT_SUGGEST_MODEL")
	if model == "" {
		model = p.ResearchModel
	}
	if p.Wire == llmprovider.WireOpenAI {
		// Live web search for research is OpenRouter's `web` plugin; a plain
		// OpenAI-compatible base has none. TACIT_SUGGEST_SEARCH forces it on for a
		// gateway that proxies search.
		search := p.Key == "openrouter" || strings.Contains(base, "openrouter") ||
			os.Getenv("TACIT_SUGGEST_SEARCH") == "1"
		return &OpenAI{Key: key, Base: base, Model: model, Search: search}, nil
	}
	return &Anthropic{Key: key, Base: base, Model: model, Version: "2023-06-01"}, nil
}

// ListModels returns the Anthropic account's available models (id + display name).
func (a *Anthropic) ListModels() ([]ModelInfo, error) {
	req, _ := http.NewRequest("GET", strings.TrimRight(a.Base, "/")+"/v1/models?limit=1000", nil)
	req.Header.Set("x-api-key", a.Key)
	req.Header.Set("anthropic-version", a.Version)
	return listModels(req)
}

// ListModels returns the OpenAI-compatible endpoint's available models. On
// OpenRouter that includes a name and description; on plain OpenAI, id only.
func (o *OpenAI) ListModels() ([]ModelInfo, error) {
	req, _ := http.NewRequest("GET", strings.TrimRight(o.Base, "/")+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+o.Key)
	return listModels(req)
}

// listModels performs a GET returning {"data":[{...}]} and maps each entry into
// a ModelInfo, reading whatever descriptive fields the provider offers
// (OpenRouter: name/description; Anthropic: display_name; OpenAI: id only).
func listModels(req *http.Request) ([]ModelInfo, error) {
	resp, err := (&http.Client{Timeout: 6 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("models HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			Description string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	infos := make([]ModelInfo, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = m.DisplayName
		}
		desc := ""
		if m.Description != "" {
			desc = truncate([]byte(m.Description), modelDescLimit)
		}
		infos = append(infos, ModelInfo{ID: m.ID, Name: name, Description: desc})
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, nil
}
