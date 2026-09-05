<p align="center"><a href="./README.md">简体中文</a> · <strong>English</strong></p>

<p align="center">
  <a href="./docs/media/hero.en.png"><img src="./docs/media/hero.en.webp" alt="Launcher — Every project. Under control. Launch · Logs · Git releases" width="720" /></a>
</p>

<p align="center"><strong>A Windows project manager for script control, live logs, and Git releases.</strong></p>

<p align="center">
  <a href="https://github.com/oooing/projects-start-manager/releases"><img src="./docs/media/download.en.svg" alt="Download for Windows" width="236" height="46" /></a>
</p>
<p align="center"><a href="./docs/guide.en.md">User guide</a> · <a href="https://github.com/oooing/projects-start-manager/actions/workflows/release.yml">Build status</a> · <a href="https://github.com/oooing/projects-start-manager/issues">Feedback</a></p>

<br />

## Less terminal juggling. More control.

Drop in a startup script. Get a project card. Find controls, logs, and service URLs in one place.

<p align="center">
  <a href="./docs/media/dashboard.png"><img src="./docs/media/dashboard.webp" alt="Actual Launcher dashboard showing six demo projects, grouped cards, process status, ports, and launch controls" width="720" /></a>
</p>
<p align="center"><sub>Actual interface · Demo data · UI shown in Chinese · Click to enlarge</sub></p>

| One-click control | Instant visibility | Organized releases |
| :--- | :--- | :--- |
| Start, stop, and restart from one place. | Live logs, ports, and service URLs at a glance. | Select files, set versions, and review notes. |

<br />

## Web today. Desktop tomorrow. Your call.

Mix configured targets or just commit code. **Tags are optional. Version groups can advance independently.**

<p align="center">
  <a href="./docs/media/release-panel.png"><img src="./docs/media/release-panel.webp" alt="Actual release panel with a selected PC target, version tag, and file selection" width="440" /></a>
</p>
<p align="center"><sub>Actual interface · Demo configuration; each project needs its own build and deployment setup.</sub></p>

Launcher's Windows installers are built in the cloud with **GitHub Actions**. After pushing, follow the progress link to check the build and release.

<br />

## Three steps to get going

**① Install Launcher　→　② Drop in a script　→　③ Hit Start**

Works with `.bat` · `.cmd` · `.ps1`. Keep the startup scripts you already use.

> Windows 10/11 x64 · Installers are unsigned and may trigger SmartScreen warnings. Verify the download source and checksums.

<details>
<summary>Scope and data safety</summary>

- Drag-and-drop works in the desktop app; browser development mode imports full paths.
- A successful push is not a completed cloud build. Publishing a Release does not update installed clients automatically.
- Git uses existing system credentials. No GitHub passwords or personal tokens are stored. Project diagnostics stay local by default; check for sensitive data before sharing.
- Scripts execute local code. Only run projects you trust.

</details>

---

<p align="center"><strong>More time for the project itself.</strong></p>
<p align="center"><a href="https://github.com/oooing/projects-start-manager/releases">Get Launcher</a> · <a href="./docs/guide.en.md#local-development">Build from source</a></p>
<p align="center"><sub>Tauri 2 · Vue 3 · Go · SQLite</sub></p>
