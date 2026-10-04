# Explicit port owner resolution

Status: accepted, 2026-10-03

## Goal and boundaries

Users keep each project's fixed ports and startup script. RunDock diagnoses
current listen declarations and explicit health endpoints, identifies an owner,
and closes that owner only after the user confirms the displayed application.
Windows excluded port ranges are system reservations, not killable processes.
There is no universal safe fixed port range across Windows installations.

## Failure modes considered before implementation

- A historical service or outbound dependency is mistaken for a required port.
- A Windows excluded port has no listening PID and is reported as an application.
- A dual-stack or exclusive IPv6 wildcard listener is mistaken for a system
  reservation; an IPv6-only listener is incorrectly treated as an IPv4 owner.
- A PID is reused, an owner changes, or project/script configuration changes
  after the confirmation opens.
- A double click, concurrent start or repeated confirmation starts twice.
- The occupying application is a managed project and needs normal stop cleanup.
- Termination reaches the serving backend, its process family, another account,
  a protected system process, or a program the user did not confirm.
- A website invokes the local operation through permissive existing CORS.
- A stop fails, permissions deny termination, or a new owner immediately takes
  the port; the target must not start on an unverified result.
- The UI is cancelled, unmounted or loses its request; no background termination
  may follow cancellation of the confirmation phase.

## Decision

Read-only runtime observations remain separate from management ownership.
Explicit resolution is a separate API operation: a fresh inspection returns a
short-lived server-issued confirmation token for exact verified owners and the
current project/script configuration. The confirmed request rechecks identity,
validates the startup script, and consumes the token under the existing startup
operation lock. Tokens are bounded and single-use.

A managed project is stopped through its launcher lifecycle. An external owner
is limited to the exact confirmed listener process, in the same user/session;
the process handle and creation time are checked again before termination.
RunDock does not terminate all programs sharing an executable name or infer
permission from logs. System/protected processes cannot be closed by this flow.

Binding evidence preserves the requested local address and protocol family.
An IPv6 wildcard is a potential IPv4 owner, not proof: actual IPv4 bind failure
and a second listener snapshot must corroborate the unique owner. Windows
access-denied errors alone do not identify a system reservation or authorize
termination of an application.

New resolution endpoints accept only local requests from explicitly trusted
RunDock UI origins, including an optional server-configured loopback origin.
They do not inherit arbitrary website access from the older API's CORS policy.

## UX and verification

The card offers `关闭占用程序并启动` for an application conflict. A compact
confirmation names programs, PIDs and ports, explains possible unsaved work loss,
and defaults focus to cancel. Reservations have an explanation and no close
action. Failures stay visible and require fresh inspection before another try.

The isolated acceptance script `scripts/acceptance/port-resolution.mjs` exercises
real owners, HTTP/API, the real Vue card/dialog, cancellation, changed owners,
managed stop, replay/concurrency, backend protection and Windows reservations.
It also covers real dual-stack, IPv6-only and Windows exclusive socket owners.
It retains JSON results, screenshots and a browser trace in its temporary data
directory. It never closes a user's project during acceptance.

## Compatibility and rollback

The new contracts and reservation fields are additive. Legacy same-project
`recover-ports` keeps its narrower authorization; explicit external termination
does not silently broaden that endpoint. No project port or source is rewritten.
Reverting this feature removes the new routes and UI actions; it does not require
database migration or modification of existing project data.

## Known limitation: PORT-IDENTITY-TEST-RUNS

During development activation, an independent Playwright run in the same source
directory was observed as the main project running, although its temporary
listeners were not the project's normal service. Folder ancestry proves source
ownership, not the identity of a particular launch instance. The current guard
conservatively blocks duplicate starts; activation waited for the independent
test to exit without closing it, then restored the original managed project.

Refine this observation in a separate follow-up using entry-script and current
endpoint evidence, without restoring historical-port termination authority.
Remove this limitation only after an E2E proves that an independent test can
coexist with a normal launch while a genuine duplicate launch remains blocked.
No force-start bypass or automatic termination of tests was added.
