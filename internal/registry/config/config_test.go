// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opentacit/tacit/internal/product"
)

// The precedence contract: process environment wins over registry.env, which
// wins over compiled-in defaults — so systemd EnvironmentFile= deployments and
// wizard-written files coexist without surprises.
func TestLoadFileFallbackAndPrecedence(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte(
		"TACIT_API_KEY=from-file\n"+
			"# comments and blanks are tolerated\n\n"+
			"TACIT_PORT=9999\n"+
			"TACIT_EXTERNAL_URL=\"https://quoted.example.com\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TACIT_PORT", "7777") // process env must beat the file
	cfg := Load()
	if cfg.APIKey != "from-file" {
		t.Fatalf("APIKey = %q, want the file's value", cfg.APIKey)
	}
	if cfg.Port != 7777 {
		t.Fatalf("Port = %d, want 7777 — process env must win over the file", cfg.Port)
	}
	if cfg.ExternalURL != "https://quoted.example.com" {
		t.Fatalf("ExternalURL = %q — quotes in env files should be stripped", cfg.ExternalURL)
	}
	if cfg.FirstRun {
		t.Fatal("FirstRun with a registry.env present")
	}
}

// Float knobs must honor registry.env with the same precedence as every other
// setting: process environment first, then the file, then the compiled default.
func TestLoadFloatPrecedence(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	if err := os.WriteFile(envPath, []byte(
		"TACIT_AUTONOMY_MIN_HELPED_RATE=0.65\n"+
			"TACIT_AUTO_PROMOTE_MIN_FIT=0.45\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Load()
	if cfg.AutonomyMinHelpedRate != 0.65 {
		t.Fatalf("AutonomyMinHelpedRate = %v, want the file's 0.65", cfg.AutonomyMinHelpedRate)
	}
	if cfg.AutoPromoteMinFit != 0.45 {
		t.Fatalf("AutoPromoteMinFit = %v, want the file's 0.45", cfg.AutoPromoteMinFit)
	}

	t.Setenv("TACIT_AUTONOMY_MIN_HELPED_RATE", "0.9") // process env must beat the file
	if got := Load().AutonomyMinHelpedRate; got != 0.9 {
		t.Fatalf("AutonomyMinHelpedRate = %v, want 0.9 — process env must win over the file", got)
	}
}

func TestFirstRunDetection(t *testing.T) {
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "absent.env"))
	if !Load().FirstRun {
		t.Fatal("no file, no key env — want FirstRun")
	}
	t.Setenv("TACIT_API_KEY", "explicitly-configured-elsewhere")
	if Load().FirstRun {
		t.Fatal("an operator-supplied key env means the registry IS configured")
	}
}

// The product name is read far from any Config — by template code with no
// server in hand — so Load has to put a file-sourced name back into the process
// environment for internal/product to find. Without that, PRODUCT_NAME would
// work under systemd (EnvironmentFile=) and silently do nothing under a bare
// `tacit serve` that keeps its settings in registry.env.
func TestProductNameFromFileReachesTheEnvironment(t *testing.T) {
	envPath := filepath.Join(t.TempDir(), "registry.env")
	t.Setenv("TACIT_REGISTRY_ENV", envPath)
	t.Setenv(product.EnvKey, "") // an empty value must not shadow the file
	if err := os.WriteFile(envPath, []byte("PRODUCT_NAME=Renamed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Load()
	if cfg.ProductName != "Renamed" {
		t.Fatalf("ProductName = %q, want the file's Renamed", cfg.ProductName)
	}
	if got := product.Name(); got != "Renamed" {
		t.Fatalf("product.Name() = %q — Load did not materialize the file value", got)
	}
	// And the feed's provider name, which the operator sees on published
	// entries, follows the product rather than a frozen brand.
	if cfg.FeedProviderName != "Renamed registry" {
		t.Fatalf("FeedProviderName = %q, want it derived from the product name", cfg.FeedProviderName)
	}

	t.Setenv(product.EnvKey, "FromEnv") // process env still wins
	if got := Load().ProductName; got != "FromEnv" {
		t.Fatalf("ProductName = %q, want the process environment to win", got)
	}
}

// Unset everywhere, the compiled-in name stands.
func TestProductNameDefaults(t *testing.T) {
	t.Setenv("TACIT_REGISTRY_ENV", filepath.Join(t.TempDir(), "absent.env"))
	t.Setenv(product.EnvKey, "")
	if got := Load().ProductName; got != product.Default {
		t.Fatalf("ProductName = %q, want the compiled default %q", got, product.Default)
	}
}
