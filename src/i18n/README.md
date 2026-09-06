# UI localization

- `zh-CN` is the default. `en` is explicitly selected in Settings.
- The preference is stored locally under `rundock.ui.locale`, independently of project settings. Storage failures do not block the UI.
- Chinese source strings are translation keys; `en.json` contains their English equivalents. `tr(key, values)` replaces numbered placeholders in one pass. Vue escapes rendered text.
- Read `tr()` inside renders, computed getters, or actions. Static label collections must be computed so switching languages does not require a reload.
- Do not translate project names, paths, commands, raw logs, or user-authored release notes. Known backend UI errors may be translated; unknown diagnostic details are preserved verbatim.
- `main.ts` synchronizes the document language and title, plus the desktop title and tray labels through `set_ui_language`. It does not restart processes or remount the app.

Run `npm run test:i18n` and `npm run build`. Browser regression checks should use an isolated backend fixture, including language persistence, help/status lists, release notes preservation, disabled-release guards, and narrow layouts. Never publish a real release for a localization test.
