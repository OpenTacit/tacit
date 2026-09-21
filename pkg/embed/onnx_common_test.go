// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package embed

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeVocab builds a minimal BERT vocab.txt whose line numbers are the ids.
func writeVocab(t *testing.T, tokens []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "vocab.txt")
	var body []byte
	for _, tok := range tokens {
		body = append(body, tok...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadWordPieceRequiresSpecials(t *testing.T) {
	// Missing [SEP] -> error, not a silent zero id.
	path := writeVocab(t, []string{"[PAD]", "[UNK]", "[CLS]", "hello"})
	if _, err := loadWordPiece(path); err == nil {
		t.Fatal("expected error for vocab missing [SEP]")
	}
}

func TestBasicTokenizeLowercasesAndSplitsPunct(t *testing.T) {
	got := basicTokenize("Query the Warehouse, please!")
	want := []string{"query", "the", "warehouse", ",", "please", "!"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("basicTokenize = %v, want %v", got, want)
	}
}

func TestWordPieceGreedySplitAndUnk(t *testing.T) {
	// ids: 0..3 specials, then play=4, ##ing=5, ##ful=6.
	path := writeVocab(t, []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "play", "##ing", "##ful"})
	w, err := loadWordPiece(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.wordpiece("playing"); !reflect.DeepEqual(got, []int{4, 5}) {
		t.Fatalf("playing -> %v, want [4 5]", got)
	}
	if got := w.wordpiece("playful"); !reflect.DeepEqual(got, []int{4, 6}) {
		t.Fatalf("playful -> %v, want [4 6]", got)
	}
	// "zzz" has no matchable prefix -> a single [UNK] (id 1).
	if got := w.wordpiece("zzz"); !reflect.DeepEqual(got, []int{w.unkID}) {
		t.Fatalf("zzz -> %v, want [%d]", got, w.unkID)
	}
}

func TestEncodeWrapsAndTruncates(t *testing.T) {
	path := writeVocab(t, []string{"[PAD]", "[UNK]", "[CLS]", "[SEP]", "play", "##ing"})
	w, err := loadWordPiece(path)
	if err != nil {
		t.Fatal(err)
	}
	ids, mask := w.encode("playing playing", 4) // body capped at maxLen-2 = 2 pieces
	// [CLS] play ##ing [SEP]  -> the two body slots are the first word's pieces.
	want := []int64{int64(w.clsID), 4, 5, int64(w.sepID)}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if len(mask) != len(ids) {
		t.Fatalf("mask len %d != ids len %d", len(mask), len(ids))
	}
	for i, m := range mask {
		if m != 1 {
			t.Fatalf("mask[%d] = %d, want 1", i, m)
		}
	}
}

func TestMeanPoolMasksAndAverages(t *testing.T) {
	// seq=3, dim=2. Third position is masked out and must not affect the mean.
	hidden := []float32{
		1, 2, // t0
		3, 4, // t1
		100, 100, // t2 (masked)
	}
	mask := []int64{1, 1, 0}
	got := meanPool(hidden, mask, 2)
	want := []float32{2, 3} // (1+3)/2, (2+4)/2
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("meanPool = %v, want %v", got, want)
	}
}
