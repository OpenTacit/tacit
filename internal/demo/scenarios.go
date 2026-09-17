// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package demo

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed datasets/*.json
var shippedDatasets embed.FS

// Scenario is a named demonstration vertical. Prompt guides the LLM generator;
// Shipped names the pre-generated dataset file (if one is bundled) so the
// scenario loads instantly without an API key.
type Scenario struct {
	Key     string
	Title   string
	Summary string
	Prompt  string // the org brief the generator expands
	Shipped string // datasets/<file>, or "" if generate-only
}

// Scenarios is the built-in catalog. Software-vendor ships fully generated; the
// rest are generate-only until a dataset is bundled for them.
var Scenarios = []Scenario{
	{
		Key:     "software-vendor",
		Title:   "B2B software vendor",
		Summary: "A cloud data-platform company: monorepo, feature flags, internal RFCs, an incident stack, a service catalog.",
		Shipped: "datasets/software-vendor.json",
		Prompt: "A ~250-person B2B software vendor that sells a cloud data platform. It has a large Go/TypeScript monorepo built " +
			"with an internal build CLI, a feature-flag/config service, an RFC-and-service-catalog system, an internal data " +
			"warehouse with a query CLI, an incident-management stack with runbooks, a CI/CD system, a design system, a billing " +
			"service, and an internal docs RAG. Engineering is split across product, platform/infra, data, mobile, growth, " +
			"security and SRE teams. Most techniques should be about using THESE internal tools well from an AI coding agent.",
	},
	{
		Key:     "automotive-manufacturer",
		Title:   "Automotive manufacturer",
		Summary: "An OEM: embedded/vehicle software, manufacturing MES, supplier PLM, homologation and safety processes.",
		Prompt: "A large automotive manufacturer (OEM). It has embedded vehicle software (AUTOSAR/C++), a manufacturing execution " +
			"system, supplier/parts PLM, a homologation and functional-safety (ISO 26262) process, diagnostics tooling, and an " +
			"internal requirements-management system. Teams span powertrain, ADAS, infotainment, manufacturing IT, supplier " +
			"quality, and homologation. Techniques should center on these internal engineering and manufacturing systems.",
	},
	{
		Key:     "cpg-retailer",
		Title:   "CPG retailer",
		Summary: "Consumer goods & retail: demand forecasting, planogram/assortment, ecommerce, loyalty, supply chain.",
		Prompt: "A consumer packaged goods and retail company. It has a demand-forecasting and replenishment platform, a " +
			"planogram/assortment system, an ecommerce storefront, a loyalty/CRM platform, a supply-chain and logistics stack, " +
			"and internal merchandising analytics. Teams span merchandising, supply chain, ecommerce engineering, data science, " +
			"and store operations. Techniques should focus on these internal retail systems and processes.",
	},
	{
		Key:     "financial-institution",
		Title:   "Financial institution",
		Summary: "A bank: core banking, payments rails, risk & compliance, fraud, trading systems, regulatory reporting.",
		Prompt: "A large financial institution (retail + commercial bank). It has a core banking platform, payments rails, a " +
			"risk-and-compliance system, a fraud-detection stack, trading and market-data systems, and a regulatory-reporting " +
			"pipeline. Everything is heavily governed (change management, model risk, audit trails). Teams span payments, " +
			"lending, risk, fraud, markets technology, and compliance engineering. Techniques should reflect these internal, " +
			"tightly-governed systems and controls.",
	},
	{
		Key:     "telecommunications-provider",
		Title:   "Telecommunications provider",
		Summary: "A telco: OSS/BSS, network provisioning, 5G RAN/core, billing/charging, field operations.",
		Prompt: "A telecommunications provider. It has OSS/BSS platforms, a network-provisioning and inventory system, 5G RAN/core " +
			"software, a billing and charging system, a field-operations dispatch platform, and a customer-care stack. Teams " +
			"span network engineering, OSS/BSS, billing, digital/app, and field operations. Techniques should focus on these " +
			"internal telco systems and workflows.",
	},
	{
		Key:     "ai-vendor",
		Title:   "AI model vendor",
		Summary: "A frontier-AI company: training infra, eval harnesses, data pipelines, inference serving, safety review.",
		Prompt: "A frontier-AI company that trains and serves large models. It has a training-orchestration platform, an evaluation " +
			"harness, large data-curation pipelines, an inference-serving stack, a fine-tuning/product API, and a safety-review " +
			"process. Teams span pretraining, evals, data, inference/serving, product engineering, and safety. Techniques " +
			"should center on these internal ML-platform systems and review processes.",
	},
}

// ScenarioByKey returns the named scenario.
func ScenarioByKey(key string) (Scenario, bool) {
	for _, s := range Scenarios {
		if s.Key == key {
			return s, true
		}
	}
	return Scenario{}, false
}

// ScenarioKeys lists the catalog keys in stable order.
func ScenarioKeys() []string {
	keys := make([]string, 0, len(Scenarios))
	for _, s := range Scenarios {
		keys = append(keys, s.Key)
	}
	sort.Strings(keys)
	return keys
}

// ShippedDataset loads the pre-generated dataset embedded for a scenario, if one
// is bundled. found is false when the scenario is generate-only.
func ShippedDataset(key string) (d *Dataset, found bool, err error) {
	sc, ok := ScenarioByKey(key)
	if !ok {
		return nil, false, fmt.Errorf("unknown scenario %q (have: %s)", key, strings.Join(ScenarioKeys(), ", "))
	}
	if sc.Shipped == "" {
		return nil, false, nil
	}
	raw, err := fs.ReadFile(shippedDatasets, sc.Shipped)
	if err != nil {
		return nil, false, fmt.Errorf("read shipped dataset %s: %w", sc.Shipped, err)
	}
	d, err = ParseDataset(raw)
	return d, err == nil, err
}
