<p align="center"><a href="./README.md">简体中文</a> · <strong>English</strong></p>

<h1 align="center">RunDock</h1>

<p align="center">
  <a href="./docs/media/hero-rundock.en.png"><img src="./docs/media/hero-rundock.en.webp" alt="RunDock — Every project. Under control. Launch · Logs · Git releases" width="720" /></a>
</p>

<p align="center"><strong>A Windows launcher that makes AI-built tools and dev projects easier to manage.</strong></p>

<p align="center">
  <a href="https://github.com/oooing/rundock/releases"><img src="./docs/media/download.en.svg" alt="Download for Windows" width="236" height="46" /></a>
</p>
<p align="center"><a href="./docs/guide.en.md">User guide</a> · <a href="https://github.com/oooing/rundock/actions/workflows/release.yml">Build status</a> · <a href="https://github.com/oooing/rundock/issues">Feedback</a></p>

<br />

## Less terminal juggling. More control.

AI makes building tools easier. But many still start with `start.bat` or `run.bat`—no installer, no convenient shortcut.

- **AI-built tools and one-off scripts**: no installer to build. Drop in a startup script and get a project card you can launch with a click.
- **Several tools running at once**: scripts run in the background without a desktop full of look-alike console windows. Read each project's logs separately.
- **Multiple projects to maintain**: organize them into groups, start or restart with a click, and manage status, service URLs, and Git releases in one place.

<p align="center">
  <a href="./docs/media/dashboard.png"><img src="./docs/media/dashboard.webp" alt="Actual RunDock dashboard showing six demo projects, grouped cards, process status, ports, and launch controls" width="720" /></a>
</p>
<p align="center"><sub>Actual interface · Demo data · UI shown in Chinese · Click to enlarge</sub></p>

<br />

## Web today. Desktop tomorrow. Your call.

Mix configured targets or just commit code. **Tags are optional. Version groups can advance independently.**

<p align="center">
  <a href="./docs/media/release-panel.png"><img src="./docs/media/release-panel.webp" alt="Actual release panel with a selected PC target, version tag, and file selection" width="440" /></a>
</p>
<p align="center"><sub>Actual interface · Demo configuration; each project needs its own build and deployment setup.</sub></p>

RunDock's Windows installers are built in the cloud with **GitHub Actions**. After pushing, follow the progress link to check the build and release.

<br />

## Three steps to get going

**① Install RunDock　→　② Drop in a script　→　③ Hit Start**

Works with `.bat` · `.cmd` · `.ps1`. Keep the startup scripts you already use.

> Windows 10/11 x64 · Installers are unsigned and may trigger SmartScreen warnings. Verify the download source and checksums.

> Upgrading from Launcher: MSI retains the upgrade identity. EXE users should uninstall the old Launcher while keeping application data, then install RunDock. Project data paths are unchanged.

<details>
<summary>Scope and data safety</summary>

- Drag-and-drop works in the desktop app; browser development mode imports full paths.
- A successful push is not a completed cloud build. Publishing a Release does not update installed clients automatically.
- Git uses existing system credentials. No GitHub passwords or personal tokens are stored. Project diagnostics stay local by default; check for sensitive data before sharing.
- Scripts execute local code. Only run projects you trust.

</details>

---

<p align="center"><strong>More time for the project itself.</strong></p>
<p align="center"><a href="https://github.com/oooing/rundock/releases">Get RunDock</a> · <a href="./docs/guide.en.md#local-development">Build from source</a></p>
<p align="center"><sub>Tauri 2 · Vue 3 · Go · SQLite</sub></p>
