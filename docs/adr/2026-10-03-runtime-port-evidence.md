# Runtime port evidence for startup checks

Status: accepted, 2026-10-03

## Context

RunDock keeps the latest discovered service for each port so stopped projects
retain useful addresses and manually assigned roles. A project may change its
ports later. Treating those historical rows or `LastURL` as required bind ports
causes unrelated services (including databases) to block startup before the
current script runs.

## Decision

Startup conflict detection uses current entry-script listen declarations and
an explicitly configured `HealthURL`. Historical services and `LastURL` remain
display/recovery metadata only. A verified process belonging to the project
still reports `running` regardless of which port it listens on; an unknown or
unrelated owner of a currently declared port still reports `conflict`.

## Consequences and rollback

Scripts that hide their listen ports in another file should declare them with
`rundock:ready` or set a `HealthURL`. Without such a declaration, the app's own
bind failure is reported after launch rather than by the preflight. Reverting
this decision would restore false conflicts for changed ports; the previous
logic is available in Git history. The isolated E2E fixture in
`scripts/acceptance/runtime-stale-ports.mjs` exercises both sides of the rule.
