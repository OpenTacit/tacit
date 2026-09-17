---
name: tacit-test
description: Run an end-to-end test of the Tacit pipeline (registry, retrieval, hooks) and produce a paste-ready trigger prompt. Use when the member invokes $tacit-test or asks to verify Tacit works.
---

## First: resolve the registry address

Run this once. Each command block starts a fresh shell, so substitute the
**resolved values** into every command and put the resolved URL (`$DASH`) in
member-facing links:

```bash
eval "$(tacit env)"   # sets REG (API base), KEY (X-Tacit-Key), DASH (dashboard URL)
```

Run an end-to-end test of the Tacit pipeline and report PASS/FAIL per stage.
Optional focus topic: whatever the member wrote after the $tacit-test mention

## Stages

1. **Registry up**: `curl -sf --max-time 3 $REG/v1/health`.

2. **Pick a target technique**: `GET $REG/v1/techniques` with header
   `X-Tacit-Key: $KEY`. Choose one `stable` technique (matching
   the focus topic if one was given, otherwise any with a concrete
   `applies_when`).

3. **Retrieval check**: POST to `/v1/evidence` (same auth) with
   `{"summary_text": "<a plain-words sentence describing matching work,
   paraphrased from the target technique's applies_when>"}`.
   PASS if the target technique comes back among the candidates; report its rank.

4. **Hook daemon**: `curl -sf --max-time 3 http://127.0.0.1:8787/v1/hooks/health`.
   If down, note it is on-demand (the relay spawns it on the next hook). Mark
   this stage FAIL when a hook fired recently and the daemon remains down.

5. **In-flow trigger prompt**: compose a short, realistic user prompt that
   should trigger the target technique's ◆ Tacit suggestion block, and present it
   paste-ready in a fenced block. Tell the member: paste it as a fresh message
   (ideally a fresh session), expect the ◆ Tacit block after the reply, and
   verify with `curl -s http://127.0.0.1:8787/v1/hooks/stats`: `shown` should
   increment. This stage tests retrieval AND delivery at once, so a silent
   result is ambiguous — $tacit-testfeedback tests delivery alone and always
   fires.

## Report

A stage-by-stage PASS/FAIL list with one line of evidence each, then the
trigger prompt. If any stage fails, state the likely cause and the exact
command to fix it before suggesting the member re-run $tacit-test.
