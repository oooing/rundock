# Windows installer upgrade policy

installer.nsi is the official Tauri CLI **2.11.4** NSIS template with narrowly
marked RUNDOCK blocks. Source URL and upstream Git blob hash are in its header;
the upstream MIT license is retained alongside it.

- An existing same-version or older NSIS installation goes straight to in-place
  installation. This applies to double-clicking the EXE, not only /UPDATE.
- The maintenance/uninstall choice is skipped. Installation still restores the
  existing directory, closes only the installed RunDock processes using the
  existing hook, and replaces program files. It does not run the old uninstaller
  or delete application configuration/project data.
- Ordinary installation deliberately does **not** set UpdateMode: missing
  WebView2 and shortcuts can still be repaired using the official install logic.
- MSI-to-NSIS migration and downgrade maintenance retain upstream handling.
  The existing explicit legacy Launcher migration guard is unchanged.
- Explicit uninstall through Windows Apps/the uninstaller remains available.

When upgrading Tauri CLI, rebase the marked blocks onto the matching official
template and update the upstream hash/version test. Do not copy a rendered
target/.../installer.nsi as the source template.

Validation (PowerShell, from code):

    $env:RUNDOCK_MAKENSIS = "$env:LOCALAPPDATA\tauri\NSIS\makensis.exe"
    node --test scripts/tests/installer-upgrade.test.mjs scripts/tests/installer-language.test.mjs
    $env:RUNDOCK_TEST_BUNDLE = "1"
    node --test scripts/tests/installer-bundle.test.mjs

The first check runs only isolated policy/language executables. The second
compiles a separate-identity no-op installer and **never launches it**. Neither
test installs/uninstalls the user's RunDock. Actual GUI upgrade acceptance should
be done in a disposable Windows profile/VM, with an older fixture installation
and sentinel configuration/project data.

This change applies to subsequently built installers; already distributed EXEs
are immutable and will retain their old behavior.
