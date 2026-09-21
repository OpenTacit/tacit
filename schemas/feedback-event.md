# Feedback Event schema

A feedback event records one step in the lifecycle of a suggestion: it was **shown**,
then maybe **adopted**, then maybe **helped**. These stage 4 events produce the
aggregate `outcomes` statistics used in retrieval ranking.

Each state transition produces one event, keeping the funnel reconstructable.

```yaml
event_id: evt_01HZX...                   # unique
audit_id: aud_01HZW...                   # ties events from the same audit together
technique_id: read-images-directly       # which technique this suggestion was for
technique_version: 3                     # technique version shown (recipes change)

stage: helped                            # shown | adopted | helped | dismissed | declined
# shown: suggestion was surfaced to the user
# adopted: user copied or used the recipe
# helped: user confirmed, explicitly or by signal, that it improved the outcome
# dismissed: user rejected or ignored it
# declined: fit-check judged the retrieved candidate inapplicable
#             here; this stays off the funnel and records retrieval quality

value: true                              # for stage=helped: true/false; for dismissed: reason enum

# --- categorical attribution for segmented stats ---
segment:                                 # the cohort this user/conversation belongs to
  role: marketer
  domain: ecommerce
  harness: chatgpt
  surface: mobile
task_type: data-extraction
rank_shown: 1                            # position the suggestion held in the audit (1 = top)

# --- provenance (metadata fields keep the event payload private) ---
confidence: explicit                     # explicit (user tapped "this helped") | inferred
timestamp: 2026-06-02T14:03:00Z
```

## Field notes

- **`stage`** models the funnel: `shown → adopted → helped`, with `dismissed` as the
  off-ramp. Aggregating gives `adoption_rate = adopted/shown` and
  `helped_rate = helped/adopted` per technique and per `segment`.
- **`segment`** is the join key to a cohort. It carries categorical features and
  excludes raw conversation content.
- **`rank_shown`** lets you detect position bias (top suggestions get adopted more
  regardless of quality) and correct ranking for it.
- **`confidence: inferred`** covers indirect signals, such as a user applying the
  recipe and continuing the chat. It is weighted below `explicit`.
- **`dismissed` with a reason** ("not relevant", "already knew", "didn't work")
  supports retrieval tuning and decay detection.

## How events become technique outcomes

```
events (stage, segment, technique_id)
   │  aggregate per technique_id × segment
   ▼
outcomes.by_segment[].{adoption_rate, helped_rate, sample_size}   →  technique.md
   │  sharp drop in helped_rate across users
   ▼
freshness.decay_signal = true  →  status = decayed  →  suppressed from future audits
```
