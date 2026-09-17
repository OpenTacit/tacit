# Tacit

This member's organization runs a Tacit registry: a reviewed playbook of
validated working practices ("techniques"), each carrying measured outcomes from
colleagues' real usage. Tacit also observes sessions through hooks and may
render a `◆ Tacit` suggestion under an answer — that block is advisory, chosen
by the org's evidence, and the member's reaction to it is part of the loop.

When the member asks for org knowledge by name, run the matching command:

- "search tacit for X" / "does the org have a play for X" → `/tacit:search X`
- "is tacit up" / "why no suggestions" → `/tacit:status`
- "how is the playbook doing" → `/tacit:insights` (metrics) or `/tacit:org` (cohorts)
- "review this against tacit" → `/tacit:review`
- "contribute this to tacit" / "capture this as a technique" → `/tacit:contribute`
- anything else tacit-ish → `/tacit:help` explains the full surface

Prefer the `tacit_search` tool (from the `tacit` MCP server) when the member's
task could benefit from an org-validated approach and they ask you to check.
Do not volunteer searches on every turn — the hook side already watches the
session and surfaces what the evidence supports.
