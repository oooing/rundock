# Automatic startup selection in project import

Status: accepted, 2026-10-03

## Goal and domain boundaries

Adding a project must not require a beginner to understand batch scripts versus
package-manager commands. Backend discovery remains read-only and owns ranking;
the frontend automatically adopts its recommended entry and imports its static
candidate. Adding persists the selected configuration, never runs the project.
Existing alternate entries remain available under collapsed advanced settings.
No ranking rule, project file or startup command is rewritten by this UI change.

## Failure modes considered before implementation

- A path changes while discovery is pending and an old result is applied.
- An earlier candidate request completes after a different entry is selected.
- A debounce, paste, Enter or native drop triggers duplicate recognition.
- A closed/unmounted dialog leaves a timer or stale request alive.
- A changed path leaves an old candidate temporarily eligible for submission.
- Saving is interrupted by a source/entry change or a second submit.
- No entry, an ambiguous low-confidence entry, a truncated scan or a network
  error is presented as successful recognition with no available next step.
- A known entry is hidden with its alternatives and the user cannot change it.
- Recognition starts a script, installs dependencies or writes to the project.
- An existing project entry can be added a second time without clear feedback.

## Interaction

Pasting, dropping or completing an absolute path starts bounded automatic
recognition. The main flow shows a short detected-entry summary and editable
project name, then confirmation. Advanced startup choices and command details
stay collapsed. No recommendation plus multiple low-confidence options must not
silently choose an arbitrary entry: show concise guidance to provide the actual
startup script or choose an entry in advanced settings. Keep explicit retry for
failed recognition and readable loading/empty/duplicate/error feedback.
An empty scan has an explicit rescan action after the user supplies a script.
Focus enters the dialog on opening, stays inside while tabbing, and returns to
the opener on close; detection never disables the user's ability to close.

## Verification and rollback

Use an isolated real-backend/browser acceptance with disposable projects/data.
Cover automatic path detection, default script preference, alternate selection,
package-only projects, ambiguous/empty/error results, changed pending inputs,
unmount, empty-result rescan, keyboard focus and duplicate submission.
Run `scripts/acceptance/automatic-startup.mjs` with the current sidecar executable
in `RUNDOCK_SIDECAR`, the installed Playwright module in
`RUNDOCK_PLAYWRIGHT_MODULE`, and an available browser channel in
`RUNDOCK_BROWSER_CHANNEL`. It writes uniquely named evidence directories under
`outputs/acceptance/automatic-startup-*` and isolates all fixtures and data.
Retain JSON, screenshots and a browser trace;
verify no fixture entry executed and no user project was created or modified.
Reverting the frontend change requires no database or project migration.
