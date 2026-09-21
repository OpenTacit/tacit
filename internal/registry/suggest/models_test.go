// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package suggest

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Both providers' /models endpoints return {"data":[{"id":...}]}; ListModels
// parses that into sorted ModelInfo, hitting the provider-correct path and auth
// and mapping whatever descriptive fields the provider supplies.
func TestListModelsParsesAndSorts(t *testing.T) {
	for _, tc := range []struct {
		name     string
		client   func(base string) Client
		wantPath string
		wantAuth [2]string   // header name, value
		body     string      // the provider's response
		want     []ModelInfo // expected parse (sorted, blanks dropped)
	}{
		{
			name:     "anthropic uses display_name",
			client:   func(base string) Client { return &Anthropic{Key: "k", Base: base, Version: "2023-06-01"} },
			wantPath: "/v1/models",
			wantAuth: [2]string{"x-api-key", "k"},
			body:     `{"data":[{"id":"zeta","display_name":"Zeta"},{"id":"alpha","display_name":"Alpha"},{"id":""}]}`,
			want:     []ModelInfo{{ID: "alpha", Name: "Alpha"}, {ID: "zeta", Name: "Zeta"}},
		},
		{
			name:     "openrouter uses name+description",
			client:   func(base string) Client { return &OpenAI{Key: "k", Base: base} },
			wantPath: "/models",
			wantAuth: [2]string{"Authorization", "Bearer k"},
			body:     `{"data":[{"id":"a/b","name":"A B","description":"A capable model."}]}`,
			want:     []ModelInfo{{ID: "a/b", Name: "A B", Description: "A capable model."}},
		},
		{
			name:     "plain openai gives id only",
			client:   func(base string) Client { return &OpenAI{Key: "k", Base: base} },
			wantPath: "/models",
			wantAuth: [2]string{"Authorization", "Bearer k"},
			body:     `{"data":[{"id":"gpt-4o"}]}`,
			want:     []ModelInfo{{ID: "gpt-4o"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get(tc.wantAuth[0])
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			got, err := tc.client(srv.URL).ListModels()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("models = %+v, want %+v", got, tc.want)
			}
			if gotPath != tc.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotAuth != tc.wantAuth[1] {
				t.Fatalf("auth = %q, want %q", gotAuth, tc.wantAuth[1])
			}
		})
	}
}
