// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package jsonschema

import (
	"strings"
	"testing"
)

const schemaDoc = `{
  "type": "object",
  "required": ["id", "stage"],
  "properties": {
    "id": {"type": "string", "pattern": "^[a-z0-9-]+$", "minLength": 3},
    "stage": {"enum": ["shown", "adopted", "helped", "dismissed"]},
    "rank": {"type": "integer", "minimum": 1, "maximum": 100},
    "rate": {"type": ["number", "null"]},
    "tags": {"type": "array", "items": {"type": "string"}, "minItems": 1},
    "segment": {"type": "object", "additionalProperties": {"type": "string"}},
    "value": {"anyOf": [{"type": "boolean"}, {"enum": ["not-relevant", "already-knew", "didnt-work"]}]},
    "nested": {"$ref": "#/$defs/inner"}
  },
  "additionalProperties": false,
  "$defs": {
    "inner": {"type": "object", "required": ["kind"], "properties": {"kind": {"const": "technique"}}}
  }
}`

func mustParse(t *testing.T) *Schema {
	t.Helper()
	s, err := Parse([]byte(schemaDoc))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidInstance(t *testing.T) {
	s := mustParse(t)
	errs := s.ValidateBytes([]byte(`{
	  "id": "use-connector", "stage": "helped", "rank": 3, "rate": null,
	  "tags": ["data"], "segment": {"team": "revops"},
	  "value": true, "nested": {"kind": "technique"}
	}`))
	if len(errs) != 0 {
		t.Fatalf("valid instance rejected: %v", errs)
	}
}

func TestViolations(t *testing.T) {
	s := mustParse(t)
	cases := []struct{ doc, wantErr string }{
		{`{"stage": "shown"}`, `missing required property "id"`},
		{`{"id": "ok-id", "stage": "exploded"}`, "not in enum"},
		{`{"id": "UPPER", "stage": "shown"}`, "does not match pattern"},
		{`{"id": "ok-id", "stage": "shown", "rank": 3.5}`, "expected type integer"},
		{`{"id": "ok-id", "stage": "shown", "rank": 0}`, "< minimum"},
		{`{"id": "ok-id", "stage": "shown", "tags": []}`, "minItems"},
		{`{"id": "ok-id", "stage": "shown", "segment": {"team": 4}}`, "expected type string"},
		{`{"id": "ok-id", "stage": "shown", "value": "sideways"}`, "anyOf"},
		{`{"id": "ok-id", "stage": "shown", "nested": {"kind": "boat"}}`, "const"},
		{`{"id": "ok-id", "stage": "shown", "mystery": 1}`, `unexpected property "mystery"`},
	}
	for _, tc := range cases {
		errs := s.ValidateBytes([]byte(tc.doc))
		found := false
		for _, e := range errs {
			if strings.Contains(e.Error(), tc.wantErr) {
				found = true
			}
		}
		if !found {
			t.Fatalf("doc %s: want error containing %q, got %v", tc.doc, tc.wantErr, errs)
		}
	}
}

func TestOneOfExactlyOne(t *testing.T) {
	s, _ := Parse([]byte(`{"oneOf": [{"type": "string"}, {"type": ["string", "number"]}]}`))
	if errs := s.Validate("both match"); len(errs) == 0 {
		t.Fatal("oneOf with two matches accepted")
	}
	if errs := s.Validate(4.0); len(errs) != 0 {
		t.Fatalf("single oneOf match rejected: %v", errs)
	}
}

func TestBadRef(t *testing.T) {
	s, _ := Parse([]byte(`{"$ref": "#/$defs/absent"}`))
	if errs := s.Validate("x"); len(errs) == 0 {
		t.Fatal("dangling $ref accepted")
	}
}
