// Copyright 2026 The OpenTacit Authors
// SPDX-License-Identifier: Apache-2.0

package hooks

import "github.com/opentacit/tacit/internal/product"

// Mark is the ◆ heading on everything this agent says to a member: the
// suggestion block, the @mention answer, the nudges, the self-test.
//
// One function because it was written out ten times across four files, all of
// them spelling the product "Tacit" — the name this project had before
// 2026-09-14. Those are the most-read words the product has, and every one of
// them named something else.
//
// It takes product.Name rather than a constant so a deployment that sets
// PRODUCT_NAME is answered in its own name. On a member's machine that variable
// is usually unset and the compiled-in name answers, which is the right default:
// the member sees what the software is called, not what one registry renamed it
// to without telling them.
//
// The mention path quotes this heading back at the model — "keep the ◆ OpenTacit
// heading" — so the instruction and the heading have to come from the same
// place or the model is told to preserve a string that is not there.
func Mark() string { return "◆ " + product.Name() }

// MarkBold is the same heading where the surface renders markdown.
func MarkBold() string { return "◆ **" + product.Name() + "**" }
