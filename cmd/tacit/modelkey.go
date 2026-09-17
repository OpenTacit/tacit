// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package main

// The model key, said once during setup.
//
// A key changes what the product does: with one, a candidate technique is
// fit-checked against the actual turn before anybody is shown it, adoption is
// judged rather than lexically matched, and the research pass can run at all.
// Without one, everything still works and everything is coarser.
//
// It was never asked for. Neither `tacit init` nor `tacit connect` mentioned
// it; the claim form listed it as an optional field among nineteen others; and
// the user guide put it in a section after "check the connection". So the
// common outcome was a member running degraded without knowing there was a
// setting, and reading the resulting quiet as OpenTacit having nothing to say.
//
// Printed rather than prompted, because the member writes the key into the file
// themselves — that is how it stays out of a transcript — and because a setup
// command that blocks on a secret cannot run in a script.
//
// The printing went one step too far. It offered the exact command to run,
// `echo 'TACIT_LLM_API_KEY=sk-…' >> ~/.tacit-key.env`, and a member who
// followed it put their key in the shell history of the machine — the file
// exists to keep the secret out of places like that. So a terminal is asked
// for the key directly, unechoed; a script is told where the file is and left
// alone.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	auditorconfig "github.com/opentacit/tacit/internal/auditor/config"
	"github.com/opentacit/tacit/internal/auditor/llm"
)

// reportModelKeyBrief answers the same question in one line, for a summary
// that has no room for six. Returns whether a key was found.
//
// Same facts, ranked: whether it is on, and what the member loses while it is
// not. Where the file lives and what a provider is billed belong in the long
// form, which `tacit connect` still prints.
func reportModelKeyBrief() bool {
	cfg := auditorconfig.Load()
	if llm.ResolveKey(cfg.LLMKeyFile) == "" {
		return false
	}
	fmt.Printf("model:     configured (%s)\n", cfg.LLMKeyFile)
	return true
}

// reportModelKey prints the state of the model key: one line when it is set,
// three when it is not. Returns whether a key was found, so a caller can decide
// what else to say.
func reportModelKey() bool {
	cfg := auditorconfig.Load()
	if llm.ResolveKey(cfg.LLMKeyFile) != "" {
		fmt.Printf("model:     configured (%s)\n", cfg.LLMKeyFile)
		return true
	}
	fmt.Printf("model:     none — suggestions are still retrieved and ranked, but not\n")
	fmt.Printf("           fit-checked against the turn before you see them.\n")
	fmt.Printf("           a key goes to your model provider, and its calls are billed to\n")
	fmt.Printf("           whoever owns it.\n")
	fmt.Printf("           to add one, put a %s= line in %s\n", llm.KeyEnv, cfg.LLMKeyFile)
	fmt.Printf("           it is read live, so nothing needs restarting.\n")
	return false
}

// OfferModelKey takes the key at the keyboard and stores it. Reports whether
// one was stored; false covers every way of declining, including having no
// keyboard to ask.
//
// A CALLER decides where this goes, and `tacit init` deliberately does not call
// it. reportModelKey sits in the middle of init's report — above the registry
// address, the API key and the owner sign-in link — so a question there stops
// the output at the point an operator is still waiting to be told how to open
// their new registry. hack/first_run.sh reads as broken when that happens, and
// it is right to.
//
// It belongs at the END of an interactive setup, which is where connect puts
// it: after the run has said everything it has to say.
func offerModelKey(keyFile string) bool {
	if !stdinIsTerminal() {
		return false
	}
	p := &prompter{sc: bufio.NewScanner(os.Stdin), interactive: true}
	if !p.confirm("add one now?") {
		return false
	}
	key := p.askSecret("paste the key (it will not echo)")
	if key == "" {
		return false
	}
	if err := storeModelKey(keyFile, key); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", keyFile, err)
		return false
	}
	fmt.Printf("model:     stored in %s — read live, so nothing needs restarting\n", keyFile)
	return true
}

// storeModelKey writes the key to the env file, replacing any line already
// setting it, at 0600 because the whole point of the file is that it is the
// private place.
func storeModelKey(path, key string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var kept []string
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		for line := range strings.SplitSeq(string(raw), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), llm.KeyEnv+"=") {
				kept = append(kept, line)
			}
		}
	case !os.IsNotExist(err):
		return err
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if body != "" {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body+llm.KeyEnv+"="+key+"\n"), 0o600)
}
