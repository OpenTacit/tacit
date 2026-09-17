// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

// Tokenization and pooling for the ONNX embedder. These are pure Go with no
// native dependency, so they compile and are unit-tested in every build — only
// the ONNX Runtime session glue (onnx.go) is behind the `onnx` build tag.
//
// The tokenizer is a BERT WordPiece implementation (uncased): whitespace and
// punctuation splitting, lowercasing, then greedy longest-match subword lookup
// against a vocab.txt. It matches the sentence-transformers/all-MiniLM-L6-v2
// preprocessing closely enough for retrieval-quality embeddings. Sentence
// embeddings use attention-masked mean pooling over the token hidden states,
// then L2 normalization — the standard sentence-transformers recipe.

package embed

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// wordPiece is a loaded BERT WordPiece vocabulary plus the special-token ids.
type wordPiece struct {
	vocab         map[string]int
	unkID         int
	clsID         int
	sepID         int
	padID         int
	maxInputChars int // words longer than this map straight to [UNK]
}

// loadWordPiece reads a BERT vocab.txt (one token per line; line number is the
// token id) and resolves the required special tokens.
func loadWordPiece(path string) (*wordPiece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	vocab := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	i := 0
	for sc.Scan() {
		vocab[strings.TrimRight(sc.Text(), "\r")] = i
		i++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(vocab) == 0 {
		return nil, fmt.Errorf("empty vocab: %s", path)
	}
	w := &wordPiece{vocab: vocab, maxInputChars: 200}
	for tok, dst := range map[string]*int{
		"[UNK]": &w.unkID, "[CLS]": &w.clsID, "[SEP]": &w.sepID, "[PAD]": &w.padID,
	} {
		id, ok := vocab[tok]
		if !ok {
			return nil, fmt.Errorf("vocab %s missing required token %q", path, tok)
		}
		*dst = id
	}
	return w, nil
}

// basicTokenize lowercases and splits on whitespace, emitting each punctuation
// or symbol rune as its own token — BERT's basic tokenizer for uncased models.
func basicTokenize(text string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsSpace(r):
			flush()
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			cur.WriteRune(r)
		default: // punctuation / symbol -> a standalone token
			flush()
			toks = append(toks, string(r))
		}
	}
	flush()
	return toks
}

// wordpiece greedily splits one basic token into subword ids (## continuation
// prefix for non-initial pieces). An unmatchable word becomes a single [UNK].
func (w *wordPiece) wordpiece(token string) []int {
	runes := []rune(token)
	if len(runes) > w.maxInputChars {
		return []int{w.unkID}
	}
	var out []int
	start := 0
	for start < len(runes) {
		end := len(runes)
		id := -1
		for start < end {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if v, ok := w.vocab[sub]; ok {
				id = v
				break
			}
			end--
		}
		if id == -1 { // no prefix of the remainder is in the vocab
			return []int{w.unkID}
		}
		out = append(out, id)
		start = end
	}
	return out
}

// encode turns text into ([CLS] ... [SEP]) input ids and an all-ones attention
// mask, truncating the body to maxLen-2 subword pieces.
func (w *wordPiece) encode(text string, maxLen int) (ids, mask []int64) {
	var pieces []int
	for _, bt := range basicTokenize(text) {
		pieces = append(pieces, w.wordpiece(bt)...)
	}
	if body := maxLen - 2; body >= 0 && len(pieces) > body {
		pieces = pieces[:body]
	}
	ids = make([]int64, 0, len(pieces)+2)
	ids = append(ids, int64(w.clsID))
	for _, p := range pieces {
		ids = append(ids, int64(p))
	}
	ids = append(ids, int64(w.sepID))
	mask = make([]int64, len(ids))
	for i := range mask {
		mask[i] = 1
	}
	return ids, mask
}

// meanPool averages the per-token hidden states (row-major [seq, dim]) over the
// positions the attention mask keeps — the sentence-transformers pooling step.
func meanPool(hidden []float32, mask []int64, dim int) []float32 {
	out := make([]float32, dim)
	var n float32
	for t := 0; t < len(mask); t++ {
		if mask[t] == 0 {
			continue
		}
		n++
		base := t * dim
		for d := 0; d < dim; d++ {
			out[d] += hidden[base+d]
		}
	}
	if n > 0 {
		for d := range out {
			out[d] /= n
		}
	}
	return out
}
