// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Package jsonschema is a minimal JSON Schema (2020-12 subset) validator,
// implemented on the standard library so tacit's schemas/*.schema.json can be
// checked without a dependency.
//
// It is test tooling, and only that. Every caller is a _test.go file: the
// round-trip tests in pkg/contracts and pkg/feed, and the response-shape tests
// in the registry and the hook agent. Nothing validates against these schemas
// at run time — /v1/contribute and feed intake do their own field-by-field
// parsing and answer with their own messages, which is what lets them say
// something useful about a bad request rather than quote a schema path.
//
// This doc comment used to claim enforcement "at trust boundaries", which read
// as a runtime guarantee that was never there. If that guarantee is wanted, it
// is a product decision and a change to the intake handlers, not something to
// infer from this package existing.
//
// Supported keywords: type (string or array), properties, required,
// additionalProperties (bool or schema), items, enum, const, pattern,
// minLength, maxLength, minimum, maximum, minItems, maxItems, anyOf, oneOf,
// allOf, $ref (into #/$defs/... or #/definitions/...). "format" is treated
// as an annotation (per the spec's default behavior). Anything else present
// in a schema is ignored — the round-trip tests keep the schemas within this
// subset.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Schema is a parsed schema document.
type Schema struct {
	root map[string]any
}

// Parse parses a schema document.
func Parse(raw []byte) (*Schema, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("schema is not valid JSON: %w", err)
	}
	return &Schema{root: root}, nil
}

// Validate checks a decoded JSON instance (the result of json.Unmarshal into
// any) and returns every violation found.
func (s *Schema) Validate(instance any) []error {
	return s.validate(s.root, instance, "$")
}

// ValidateBytes unmarshals raw JSON and validates it.
func (s *Schema) ValidateBytes(raw []byte) []error {
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return []error{fmt.Errorf("instance is not valid JSON: %w", err)}
	}
	return s.Validate(instance)
}

func (s *Schema) resolveRef(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("only local $ref supported, got %q", ref)
	}
	node := any(s.root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q: path broken at %q", ref, part)
		}
		node, ok = m[part]
		if !ok {
			return nil, fmt.Errorf("$ref %q: %q not found", ref, part)
		}
	}
	m, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref %q does not resolve to a schema", ref)
	}
	return m, nil
}

func (s *Schema) validate(schema map[string]any, v any, path string) []error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...)))
	}

	if ref, ok := schema["$ref"].(string); ok {
		target, err := s.resolveRef(ref)
		if err != nil {
			return []error{err}
		}
		return s.validate(target, v, path)
	}

	if t, ok := schema["type"]; ok && !typeMatches(t, v) {
		fail("expected type %v, got %s", t, typeName(v))
		return errs // further keyword checks would just cascade
	}

	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if jsonEqual(e, v) {
				found = true
				break
			}
		}
		if !found {
			fail("value %v not in enum %v", v, enum)
		}
	}
	if c, ok := schema["const"]; ok && !jsonEqual(c, v) {
		fail("value %v != const %v", v, c)
	}

	for _, combKey := range []string{"anyOf", "oneOf"} {
		if raw, ok := schema[combKey].([]any); ok {
			matches := 0
			for _, sub := range raw {
				if m, ok := sub.(map[string]any); ok && len(s.validate(m, v, path)) == 0 {
					matches++
				}
			}
			if combKey == "anyOf" && matches == 0 {
				fail("matches no anyOf branch")
			}
			if combKey == "oneOf" && matches != 1 {
				fail("matches %d oneOf branches, want exactly 1", matches)
			}
		}
	}
	if raw, ok := schema["allOf"].([]any); ok {
		for _, sub := range raw {
			if m, ok := sub.(map[string]any); ok {
				errs = append(errs, s.validate(m, v, path)...)
			}
		}
	}

	switch val := v.(type) {
	case string:
		if min, ok := num(schema["minLength"]); ok && float64(len(val)) < min {
			fail("length %d < minLength %v", len(val), min)
		}
		if max, ok := num(schema["maxLength"]); ok && float64(len(val)) > max {
			fail("length %d > maxLength %v", len(val), max)
		}
		if p, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(p)
			if err != nil {
				fail("bad pattern %q", p)
			} else if !re.MatchString(val) {
				fail("%q does not match pattern %q", val, p)
			}
		}
	case float64:
		if min, ok := num(schema["minimum"]); ok && val < min {
			fail("%v < minimum %v", val, min)
		}
		if max, ok := num(schema["maximum"]); ok && val > max {
			fail("%v > maximum %v", val, max)
		}
	case []any:
		if min, ok := num(schema["minItems"]); ok && float64(len(val)) < min {
			fail("%d items < minItems %v", len(val), min)
		}
		if max, ok := num(schema["maxItems"]); ok && float64(len(val)) > max {
			fail("%d items > maxItems %v", len(val), max)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range val {
				errs = append(errs, s.validate(items, item, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case map[string]any:
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				name, _ := r.(string)
				if _, present := val[name]; !present {
					fail("missing required property %q", name)
				}
			}
		}
		for name, value := range val {
			if propSchema, ok := props[name].(map[string]any); ok {
				errs = append(errs, s.validate(propSchema, value, path+"."+name)...)
				continue
			}
			switch ap := schema["additionalProperties"].(type) {
			case bool:
				if !ap {
					fail("unexpected property %q", name)
				}
			case map[string]any:
				errs = append(errs, s.validate(ap, value, path+"."+name)...)
			}
		}
	}
	return errs
}

func typeMatches(t any, v any) bool {
	switch tt := t.(type) {
	case string:
		return typeIs(tt, v)
	case []any:
		for _, one := range tt {
			if name, ok := one.(string); ok && typeIs(name, v) {
				return true
			}
		}
	}
	return false
}

func typeIs(name string, v any) bool {
	switch name {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == float64(int64(f))
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "null":
		return v == nil
	}
	return false
}

func typeName(v any) string {
	switch v.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	}
	return fmt.Sprintf("%T", v)
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func jsonEqual(a, b any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return string(ra) == string(rb)
}
