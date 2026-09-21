// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Omnigent reader: a meta-harness session -> canonical interaction record.
//
// Omnigent is one capture source among several (gateway, hooks); this is the
// only Omnigent-specific code. Source of truth for the shapes:
// github.com/omnigent-ai/omnigent (SessionResponse, ConversationItem, and the
// MessageData / FunctionCallData / FunctionCallOutputData payloads). Accepts
// the parsed JSON from GET /v1/sessions/{id}; no SDK dependency.

package capture

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/opentacit/tacit/internal/auditor/contracts"
)

// segment labels lifted from Omnigent session labels onto our dimensions
var omnigentSegmentLabels = []string{"team", "role", "function", "domain", "surface"}

// non-text content blocks -> the modality they imply
var modalityByBlock = map[string]string{
	"input_image": "image", "input_file": "file", "input_audio": "audio",
}

// itemFields returns an item's type-specific fields, tolerating both Omnigent
// wire shapes: the snapshot nests them under `data`; the items route and the
// SSE stream flatten them onto the top level. A live capture mixes both.
func itemFields(item map[string]any) map[string]any {
	if data, ok := item["data"].(map[string]any); ok {
		return data
	}
	return item
}

// textAndModalities flattens a MessageData.content block list.
func textAndModalities(content any) (string, []string) {
	blocks, _ := content.([]any)
	var texts []string
	var modalities []string
	seen := map[string]bool{}
	add := func(m string) {
		if m != "" && !seen[m] {
			seen[m] = true
			modalities = append(modalities, m)
		}
	}
	for _, b := range blocks {
		block, ok := b.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := block["text"].(string); t != "" {
			texts = append(texts, t)
			add("text")
		} else {
			bt, _ := block["type"].(string)
			add(modalityByBlock[bt])
		}
	}
	return joinLines(texts), modalities
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

// ReadOmnigentSession maps an Omnigent SessionResponse dict onto the
// canonical record.
func ReadOmnigentSession(session map[string]any) contracts.CanonicalRecord {
	items, _ := session["items"].([]any)

	var messages []contracts.Message
	type callWithID struct {
		call   contracts.ToolCall
		callID string
	}
	var calls []callWithID
	outputs := map[string]string{}

	for _, raw := range items {
		it, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		itype, _ := it["type"].(string)
		data := itemFields(it)
		switch itype {
		case "message":
			if meta, _ := data["is_meta"].(bool); meta {
				continue // injected durable context, not user-facing
			}
			text, modalities := textAndModalities(data["content"])
			if len(modalities) == 0 {
				modalities = []string{"text"}
			}
			role, _ := data["role"].(string)
			messages = append(messages, contracts.Message{Role: role, Text: text, Modalities: modalities})
		case "function_call":
			name, _ := data["name"].(string)
			args, _ := data["arguments"].(string)
			callID, _ := data["call_id"].(string)
			calls = append(calls, callWithID{contracts.ToolCall{Name: name, Arguments: args}, callID})
		case "function_call_output":
			callID, _ := data["call_id"].(string)
			outputs[callID] = anyToString(data["output"])
		}
		// reasoning / compaction / error / native_tool items intentionally ignored
	}

	toolCalls := make([]contracts.ToolCall, 0, len(calls))
	for _, c := range calls { // join outputs onto their calls by call_id
		c.call.Output = outputs[c.callID]
		toolCalls = append(toolCalls, c.call)
	}

	labels, _ := session["labels"].(map[string]any)
	segment := contracts.Segment{}
	for _, k := range omnigentSegmentLabels {
		if v, _ := labels[k].(string); v != "" {
			segment[k] = v
		}
	}
	harness, _ := session["harness"].(string)
	if harness != "" && segment["harness"] == "" {
		segment["harness"] = harness
	}
	actorSet := map[string]bool{}
	for _, raw := range items {
		if it, ok := raw.(map[string]any); ok {
			if by, _ := it["created_by"].(string); by != "" {
				actorSet[by] = true
			}
		}
	}
	actors := make([]string, 0, len(actorSet))
	for a := range actorSet {
		actors = append(actors, a)
	}
	sort.Strings(actors)

	model, _ := session["model_override"].(string)
	if model == "" {
		model, _ = session["llm_model"].(string)
	}
	sessionID, _ := session["id"].(string)
	surface, _ := labels["surface"].(string)
	createdAt, _ := session["created_at"].(string)
	status, _ := session["status"].(string)

	rec := contracts.CanonicalRecord{
		SchemaVersion: SchemaVersion,
		Source:        "omnigent",
		SessionID:     sessionID,
		Harness:       harness,
		Surface:       surface,
		Model:         model,
		CreatedAt:     createdAt,
		Status:        status,
		Messages:      messages,
		ToolCalls:     toolCalls,
		Segment:       segment,
		Actors:        actors,
	}
	if v, ok := session["total_cost_usd"].(float64); ok {
		rec.CostUSD = &v
	}
	if v, ok := session["last_total_tokens"].(float64); ok {
		n := int(v)
		rec.Tokens = &n
	}
	return rec
}

func anyToString(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	default:
		return fmt.Sprint(v)
	}
}

// ReadOmnigentFile parses a saved GET /v1/sessions/{id} response.
func ReadOmnigentFile(path string) (contracts.CanonicalRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return contracts.CanonicalRecord{}, err
	}
	var session map[string]any
	if err := json.Unmarshal(raw, &session); err != nil {
		return contracts.CanonicalRecord{}, err
	}
	return ReadOmnigentSession(session), nil
}
