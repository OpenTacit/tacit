// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"reflect"
	"testing"
)

func TestEvalDispatchHelp(t *testing.T) {
	if got := cmdEval(nil); got != 2 {
		t.Fatalf("cmdEval(nil) = %d, want 2", got)
	}
	if got := cmdEval([]string{"other"}); got != 2 {
		t.Fatalf("unknown eval command = %d, want 2", got)
	}
}

func TestParseEvalFloors(t *testing.T) {
	got, err := parseEvalFloors("0.20, .25")
	if err != nil || !reflect.DeepEqual(got, []float64{0.2, 0.25}) {
		t.Fatalf("parse floors = %v, %v", got, err)
	}
	if _, err := parseEvalFloors("1.1"); err == nil {
		t.Fatal("accepted cosine floor above 1")
	}
	if _, err := parseEvalFloors("NaN"); err == nil {
		t.Fatal("accepted NaN cosine floor")
	}
	got, err = parseEvalFloors("0.201,0.204,0.201")
	if err != nil || !reflect.DeepEqual(got, []float64{0.201, 0.204}) || floorRankerName(got[0]) == floorRankerName(got[1]) {
		t.Fatalf("distinct floors collided: %v, %v", got, err)
	}
}

func TestEvalRejectsInvalidDimension(t *testing.T) {
	if got := cmdEvalRetrieval([]string{"--set", "unused", "--model", "hashing-v1", "--dim", "0"}); got != 2 {
		t.Fatalf("zero dimension exit = %d, want 2", got)
	}
}
