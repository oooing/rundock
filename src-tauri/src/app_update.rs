//! Download only official RunDock assets; verify the published SHA-256 before
//! handing the installer to Windows. No URL or local executable comes from IPC.
use reqwest::blocking::Client;
use semver::Version;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    fs::{self, File},
    io::{Read, Write},
    path::{Path, PathBuf},
    sync::Mutex,
    time::Duration,
};
use tauri::{ipc::Channel, Manager};

const REPOSITORY: &str = "https://github.com/oooing/rundock";
const MAX_INSTALLER: u64 = 512 * 1024 * 1024;

#[derive(Clone, Deserialize)]
struct Asset {
    name: String,
    browser_download_url: String,
    size: u64,
}
#[derive(Deserialize)]
struct Release {
    tag_name: String,
    draft: bool,
    prerelease: bool,
    body: Option<String>,
    assets: Vec<Asset>,
}
#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateInfo {
    version: String,
    notes: String,
    url: String,
    size: u64,
    #[serde(rename = "installedLocally")]
    installed_locally: bool,
}
#[derive(Clone)]
struct Candidate {
    info: UpdateInfo,
    installer: Asset,
    checksum: Asset,
}
#[derive(Clone, Serialize, Deserialize)]
pub(crate) struct Ready {
    path: PathBuf,
    hash: String,
    size: u64,
}
#[derive(Default)]
struct Session {
    candidate: Option<Candidate>,
    ready: Option<Ready>,
    installed: Option<(String, bool)>,
    plan: Option<crate::update_transaction::Plan>,
    installer_identity: Option<crate::installer_process::Identity>,
}
#[derive(Default)]
pub struct UpdateState(Mutex<Session>);
#[derive(Clone, Serialize)]
pub struct Progress {
    downloaded: u64,
    total: u64,
}

fn client(timeout: u64) -> Result<Client, String> {
    Client::builder()
        .user_agent(concat!("RunDock/", env!("CARGO_PKG_VERSION")))
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(timeout))
        .https_only(true)
        .build()
        .map_err(|e| e.to_string())
}
fn asset_for(release: &Release, name: &str) -> Option<Asset> {
    let expected = format!("{REPOSITORY}/releases/download/{}/{name}", release.tag_name);
    release
        .assets
        .iter()
        .find(|a| a.name == name && a.browser_download_url == expected && a.size > 0)
        .cloned()
}
fn select_update(releases: Vec<Release>, current: &Version, msi: bool) -> Option<Candidate> {
    releases
        .into_iter()
        .filter_map(|r| {
            if r.draft || r.prerelease {
                return None;
            }
            let version = Version::parse(r.tag_name.strip_prefix('v')?).ok()?;
            if !version.pre.is_empty() || version <= *current || !version.build.is_empty() {
                return None;
            }
            let name = if msi {
                format!("RunDock_{version}_x64_en-US.msi")
            } else {
                format!("RunDock_{version}_x64-setup.exe")
            };
            let installer = asset_for(&r, &name)?;
            let checksum = asset_for(&r, "SHA256SUMS.txt")?;
            if installer.size > MAX_INSTALLER || checksum.size > 65536 {
                return None;
            }
            Some((
                version.clone(),
                Candidate {
                    info: UpdateInfo {
                        version: version.to_string(),
                        notes: r.body.unwrap_or_default(),
                        url: format!("{REPOSITORY}/releases/tag/v{version}"),
                        size: installer.size,
                        installed_locally: false,
                    },
                    installer,
                    checksum,
                },
            ))
        })
        .max_by(|a, b| a.0.cmp(&b.0))
        .map(|(_, c)| c)
}
fn installer_is_msi() -> Result<bool, String> {
    let exe = std::env::current_exe().map_err(|e| e.to_string())?;
    // Tauri's NSIS bundle installs uninstall.exe next to the application.
    Ok(!exe
        .parent()
        .ok_or("无法找到应用目录")?
        .join("uninstall.exe")
        .is_file())
}
fn fetch_candidate(current: &Version, msi: bool) -> Result<Option<Candidate>, String> {
    let client = client(25)?;
    let response = client
        .get("https://api.github.com/repos/oooing/rundock/releases?per_page=100")
        .send()
        .map_err(|e| format!("无法连接 GitHub，请检查网络后重试：{e}"))?;
    if response.status().as_u16() == 403 || response.status().as_u16() == 429 {
        // Public API limits must not prevent users sharing an IP from updating.
        let response = client
            .get(format!("{REPOSITORY}/releases/latest"))
            .send()
            .and_then(|r| r.error_for_status())
            .map_err(|e| format!("GitHub 暂时限制了检查更新，请稍后重试或打开版本下载页：{e}"))?;
        let version = Version::parse(
            response
                .url()
                .as_str()
                .strip_prefix(&format!("{REPOSITORY}/releases/tag/v"))
                .ok_or("版本信息无效")?,
        )
        .map_err(|_| "版本信息无效")?;
        if !version.pre.is_empty() || !version.build.is_empty() {
            return Err("版本信息无效".into());
        }
        let tag = format!("v{version}");
        let name = if msi {
            format!("RunDock_{version}_x64_en-US.msi")
        } else {
            format!("RunDock_{version}_x64-setup.exe")
        };
        let installer_url = format!("{REPOSITORY}/releases/download/{tag}/{name}");
        let checksum_url = format!("{REPOSITORY}/releases/download/{tag}/SHA256SUMS.txt");
        let mut sums = String::new();
        client
            .get(&checksum_url)
            .send()
            .and_then(|r| r.error_for_status())
            .map_err(|e| format!("最新版本尚无完整安装包，请稍后重试：{e}"))?
            .take(65536)
            .read_to_string(&mut sums)
            .map_err(|e| e.to_string())?;
        expected_hash(&sums, &name)?;
        let response = client
            .head(&installer_url)
            .send()
            .and_then(|r| r.error_for_status())
            .map_err(|e| format!("无法检查安装包：{e}"))?;
        let size = response
            .headers()
            .get(reqwest::header::CONTENT_LENGTH)
            .and_then(|h| h.to_str().ok())
            .and_then(|h| h.parse::<u64>().ok())
            .ok_or("无法读取安装包大小")?;
        let assets = vec![
            Asset {
                name,
                size,
                browser_download_url: installer_url,
            },
            Asset {
                name: "SHA256SUMS.txt".into(),
                size: sums.len() as u64,
                browser_download_url: checksum_url,
            },
        ];
        let candidate = select_update(
            vec![Release {
                tag_name: tag,
                draft: false,
                prerelease: false,
                body: None,
                assets,
            }],
            &Version::new(0, 0, 0),
            msi,
        )
        .ok_or("最新版本没有可用安装包，请稍后重试")?;
        return Ok((version > *current).then_some(candidate));
    }
    let response = response
        .error_for_status()
        .map_err(|e| format!("无法检查更新，请稍后重试：{e}"))?;
    let releases: Vec<Release> = serde_json::from_reader(response.take(4 * 1024 * 1024))
        .map_err(|e| format!("无法读取版本信息：{e}"))?;
    Ok(select_update(releases, current, msi))
}
fn expected_hash(text: &str, name: &str) -> Result<String, String> {
    let matches: Vec<_> = text
        .lines()
        .filter_map(|line| {
            let (hash, file) = line.split_once(char::is_whitespace)?;
            (file.trim().trim_start_matches('*') == name).then_some(hash)
        })
        .collect();
    if matches.len() != 1
        || matches[0].len() != 64
        || !matches[0].bytes().all(|b| b.is_ascii_hexdigit())
    {
        return Err("安装包缺少有效校验信息，请稍后重试".into());
    }
    Ok(matches[0].to_ascii_lowercase())
}
fn copy_verified(
    mut input: impl Read,
    mut output: impl Write,
    size: u64,
    hash: &str,
    mut progress: impl FnMut(u64),
) -> Result<(), String> {
    let mut digest = Sha256::new();
    let mut buffer = [0; 65536];
    let mut received = 0;
    loop {
        let n = input
            .read(&mut buffer)
            .map_err(|e| format!("下载中断：{e}"))?;
        if n == 0 {
            break;
        }
        received += n as u64;
        if received > size || received > MAX_INSTALLER {
            return Err("安装包大小与发布信息不一致".into());
        }
        output
            .write_all(&buffer[..n])
            .map_err(|e| format!("无法保存安装包：{e}"))?;
        digest.update(&buffer[..n]);
        progress(received);
    }
    if received != size || format!("{:x}", digest.finalize()) != hash {
        return Err("安装包校验失败，请重新下载".into());
    }
    Ok(())
}
fn verify_ready(ready: &Ready) -> Result<(), String> {
    let file = File::open(&ready.path).map_err(|_| "安装包已丢失，请重新下载".to_string())?;
    copy_verified(file, std::io::sink(), ready.size, &ready.hash, |_| {})
}
fn download(
    candidate: &Candidate,
    directory: &Path,
    progress: impl FnMut(u64),
) -> Result<Ready, String> {
    let client = client(600)?;
    let mut response = client
        .get(&candidate.checksum.browser_download_url)
        .send()
        .and_then(|r| r.error_for_status())
        .map_err(|e| format!("无法获取安装包校验信息：{e}"))?
        .take(65537);
    let mut sums = String::new();
    response
        .read_to_string(&mut sums)
        .map_err(|e| e.to_string())?;
    if sums.len() > 65536 {
        return Err("校验文件过大".into());
    }
    let hash = expected_hash(&sums, &candidate.installer.name)?;
    fs::create_dir_all(directory).map_err(|e| e.to_string())?;
    let path = directory.join(&candidate.installer.name);
    let partial = directory.join(format!("{}.part", candidate.installer.name));
    let result = (|| {
        let response = client
            .get(&candidate.installer.browser_download_url)
            .send()
            .and_then(|r| r.error_for_status())
            .map_err(|e| format!("无法下载安装包：{e}"))?;
        let mut file = File::create(&partial).map_err(|e| e.to_string())?;
        copy_verified(
            response,
            &mut file,
            candidate.installer.size,
            &hash,
            progress,
        )?;
        file.sync_all().map_err(|e| e.to_string())?;
        drop(file);
        if path.exists() {
            fs::remove_file(&path).map_err(|e| e.to_string())?;
        }
        fs::rename(&partial, &path).map_err(|e| e.to_string())?;
        Ok(Ready {
            path,
            hash,
            size: candidate.installer.size,
        })
    })();
    if result.is_err() {
        let _ = fs::remove_file(partial);
    }
    result
}

#[tauri::command]
pub async fn check_app_update(app: tauri::AppHandle) -> Result<Option<UpdateInfo>, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let mut session = state.0.try_lock().map_err(|_| "正在处理更新，请稍后重试")?;
        ensure_staging_resolved(&update_dir()?)?;
        let current = Version::parse(env!("CARGO_PKG_VERSION")).map_err(|e| e.to_string())?;
        #[cfg(windows)]
        if !cfg!(debug_assertions) {
            let target = crate::desktop_restart::installed_executable(
                &std::env::current_exe().map_err(|e| e.to_string())?,
            )?;
            let installed = crate::desktop_restart::file_version(&target)?;
            let disk_version = Version::parse(&installed).map_err(|e| e.to_string())?;
            let backend_older = crate::update_transaction::request(
                crate::SIDECAR_PORT.parse().unwrap(), "/api/health", None,
            ).ok().and_then(|h| h["version"].as_str().and_then(|v| Version::parse(v).ok()))
                .is_some_and(|v| v < disk_version);
            if disk_version > current || (disk_version == current && backend_older) {
                verify_installed_version(&installed)?;
                session.installed = Some((installed.clone(), false));
                return Ok(Some(UpdateInfo {
                    version: installed,
                    notes: String::new(),
                    url: String::new(),
                    size: 0,
                    installed_locally: true,
                }));
            }
        }
        session.candidate = fetch_candidate(&current, installer_is_msi()?)?;
        session.ready = None;
        Ok(session.candidate.as_ref().map(|c| c.info.clone()))
    })
    .await
    .map_err(|e| e.to_string())?
}
#[tauri::command]
pub async fn download_app_update(
    app: tauri::AppHandle,
    on_progress: Channel<Progress>,
) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let mut session = state.0.try_lock().map_err(|_| "正在处理更新，请稍后重试")?;
        let candidate = session.candidate.clone().ok_or("请先检查更新")?;
        session.ready = None;
        let directory = app
            .path()
            .app_cache_dir()
            .map_err(|e| e.to_string())?
            .join("updates");
        session.ready = Some(download(&candidate, &directory, |downloaded| {
            let _ = on_progress.send(Progress {
                downloaded,
                total: candidate.installer.size,
            });
        })?);
        Ok(())
    })
    .await
    .map_err(|e| e.to_string())?
}
fn update_dir() -> Result<PathBuf, String> {
    crate::sidecar_data_dir().ok_or("无法确定更新记录目录".into())
}
fn production_only() -> Result<(), String> {
    if cfg!(debug_assertions) || !cfg!(target_os = "windows") {
        return Err("请在已安装的 Windows 正式版中更新".into());
    }
    Ok(())
}

#[tauri::command]
pub async fn update_install_plan(
    app: tauri::AppHandle,
) -> Result<crate::update_transaction::Plan, String> {
    production_only()?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let mut session = state.0.try_lock().map_err(|_| "正在处理更新")?;
        let plan = crate::update_transaction::plan(crate::SIDECAR_PORT.parse().unwrap())?;
        session.plan = Some(plan.clone());
        Ok(plan)
    })
    .await
    .map_err(|e| e.to_string())?
}
#[tauri::command]
pub async fn schedule_app_update(app: tauri::AppHandle) -> Result<(), String> {
    production_only()?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let session = state.0.try_lock().map_err(|_| "正在处理更新")?;
        let (version, installer) = update_input(&session)?;
        let p = crate::update_transaction::Pending {
            version,
            installer,
            phase: "scheduled".into(),
            projects: vec![],
            remaining: vec![],
            error: String::new(),
            failures: vec![],
        };
        crate::update_transaction::save(&update_dir()?, &p)
    })
    .await
    .map_err(|e| e.to_string())?
}
fn update_input(session: &Session) -> Result<(String, Option<Ready>), String> {
    ensure_staging_resolved(&update_dir()?)?;
    if let Some((version, reboot)) = &session.installed {
        if *reboot {
            return Err("此更新需要重启电脑，请保存工作后手动重启".into());
        }
        return Ok((version.clone(), None));
    }
    if let Some(ready) = &session.ready {
        verify_ready(ready)?;
        return Ok((
            session
                .candidate
                .as_ref()
                .ok_or("更新记录已失效")?
                .info
                .version
                .clone(),
            Some(ready.clone()),
        ));
    }
    let pending = crate::update_transaction::load(&update_dir()?)?.ok_or("请先下载安装包")?;
    if !matches!(pending.phase.as_str(), "scheduled" | "staged") {
        return Err("请先下载安装包".into());
    }
    if pending.phase == "staged" {
        verify_installed_version(&pending.version)?;
        return Ok((pending.version, None));
    }
    if let Some(ready) = &pending.installer {
        verify_ready(ready)?
    }
    Ok((pending.version, pending.installer))
}
#[tauri::command]
pub async fn app_update_status(
    app: tauri::AppHandle,
) -> Result<Option<crate::update_transaction::Status>, String> {
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let directory = update_dir()?;
        // The worker owns the transition until it finishes or hands off after
        // its bounded wait. Never race the spawn/persist window.
        if let Ok(mut session) = state.0.try_lock() {
            reconcile_staging_with_identity(&directory, &mut session.installer_identity)?;
            if let Some(p) = crate::update_transaction::load(&directory)? {
                if p.phase == "staged" {
                    session.installed = Some((p.version, false));
                    session.ready = None;
                }
            }
            // Keep the session lock through reconciliation: otherwise an
            // activation can replace the record while this read is in flight.
            return crate::update_transaction::status(&directory, crate::SIDECAR_PORT.parse().unwrap(), env!("CARGO_PKG_VERSION"));
        }
        Ok(crate::update_transaction::load(&directory)?.map(|pending| crate::update_transaction::Status {
            pending, current_version: env!("CARGO_PKG_VERSION").into(),
            backend_version: None, version_active: false, status_error: String::new(),
        }))
    })
    .await
    .map_err(|e| e.to_string())?
}

fn verify_installed_version(version: &str) -> Result<(), String> {
    #[cfg(windows)]
    {
        let current = std::env::current_exe().map_err(|e| e.to_string())?;
        let target = crate::desktop_restart::installed_executable(&current)?;
        if target
            .parent()
            .unwrap()
            .join(".rundock-install-backup.json")
            .exists()
        {
            return Err("安装尚未完成，原程序和项目保持运行。请查看安装记录后重试。".into());
        }
        if crate::desktop_restart::file_version(&target)? != version {
            return Err("安装后的版本不匹配，原程序和项目保持运行。请重新下载安装包。".into());
        }
        Ok(())
    }
    #[cfg(not(windows))]
    {
        let _ = version;
        Err("仅支持 Windows".into())
    }
}

fn installer_identity_path(directory: &Path) -> PathBuf {
    directory.join("desktop-update-installer.json")
}
fn finish_staging(
    directory: &Path,
    p: &mut crate::update_transaction::Pending,
    result: Result<(), String>,
) -> Result<(), String> {
    p.phase = if result.is_ok() { "staged" } else { "finished" }.into();
    p.error = result.as_ref().err().cloned().unwrap_or_default();
    crate::update_transaction::save(directory, p)?;
    let _ = fs::remove_file(installer_identity_path(directory));
    Ok(())
}
fn reconcile_staging(directory: &Path) -> Result<(), String> {
    reconcile_staging_with_identity(directory, &mut None)
}

fn ensure_staging_resolved(directory: &Path) -> Result<(), String> {
    if let Some(p) = crate::update_transaction::load(directory)? {
        if p.phase == "staging" {
            return Err(
                "安装尚未确认完成，暂不能继续更新或立即生效。可以关闭窗口，项目保持运行。".into(),
            );
        }
    }
    Ok(())
}

fn reconcile_staging_with_identity(
    directory: &Path,
    fallback: &mut Option<crate::installer_process::Identity>,
) -> Result<(), String> {
    reconcile_staging_checked(directory, fallback, verify_installed_version)
}

fn reconcile_staging_checked(
    directory: &Path,
    fallback: &mut Option<crate::installer_process::Identity>,
    verify: impl FnOnce(&str) -> Result<(), String>,
) -> Result<(), String> {
    let Some(mut p) = crate::update_transaction::load(directory)? else {
        return Ok(());
    };
    if p.phase != "staging" {
        *fallback = None;
        return Ok(());
    }
    // Only this session's captured identity can substitute for a missing record.
    // After a restart, unknown identity must never mean the installer has exited.
    let identity = fallback
        .clone()
        .map(Ok)
        .unwrap_or_else(|| crate::installer_process::load(&installer_identity_path(directory)));
    match identity.and_then(|id| crate::installer_process::alive(&id)) {
        Ok(true) => return Ok(()),
        Ok(false) => {}
        Err(error) => {
            p.error = format!("无法确认安装是否结束：{error}。请保留安装记录；暂不能继续安装或立即生效。可以关闭窗口，项目保持运行。");
            return crate::update_transaction::save(directory, &p);
        }
    }
    let result = verify(&p.version);
    // Persist the failure for the UI; no stop, restart or unattended retry.
    finish_staging(directory, &mut p, result)?;
    *fallback = None;
    Ok(())
}

fn track_and_wait_installer(
    directory: &Path,
    p: &mut crate::update_transaction::Pending,
    child: &mut std::process::Child,
    fallback: &mut Option<crate::installer_process::Identity>,
    timeout: Duration,
) -> Result<bool, String> {
    let tracking = crate::installer_process::identify(child).and_then(|identity| {
        *fallback = Some(identity.clone());
        crate::installer_process::save(&installer_identity_path(directory), &identity)
    });
    match crate::installer_process::wait_for_exit(child, timeout) {
        Ok(Some(status)) if status.success() => Ok(true),
        Ok(Some(status)) => Err(format!(
            "安装未完成（退出码 {:?}）。项目保持运行，请重试安装。",
            status.code()
        )),
        waiting => {
            let reason = match waiting {
                Err(error) => format!("无法确认安装进程状态：{error}"),
                _ => "安装超过等待时限，仍可能在后台进行".into(),
            };
            p.error = match tracking {
                Err(error) => format!("无法保存安装进程身份：{error}。{reason}；请保留当前安装记录，稍后查看结果。可以关闭窗口，项目保持运行。"),
                Ok(()) => format!("{reason}；请稍后查看结果。可以关闭窗口，项目保持运行。"),
            };
            Ok(false)
        }
    }
}

pub(crate) fn stage_files(
    directory: &Path,
    p: &mut crate::update_transaction::Pending,
) -> Result<(), String> {
    stage_files_with_identity(directory, p, &mut None)
}

fn stage_files_with_identity(
    directory: &Path,
    p: &mut crate::update_transaction::Pending,
    fallback: &mut Option<crate::installer_process::Identity>,
) -> Result<(), String> {
    // A prior transaction's identity cannot be evidence for a new installer.
    match fs::remove_file(installer_identity_path(directory)) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
        Err(e) => return Err(e.to_string()),
    }
    *fallback = None;
    crate::update_transaction::save(directory, p)?;
    let result = (|| {
        if let Some(ready) = &p.installer {
            verify_ready(ready)?;
            let mut child =
                installer_command(&ready.path, std::env::var_os("SystemRoot").as_deref())?
                    .spawn()
                    .map_err(|e| format!("无法启动安装：{e}。项目保持运行。"))?;
            if !track_and_wait_installer(
                directory,
                p,
                &mut child,
                fallback,
                Duration::from_secs(180),
            )? {
                return Ok(false);
            }
        }
        verify_installed_version(&p.version)?;
        Ok(true)
    })();
    match result {
        Ok(false) => {
            crate::update_transaction::save(directory, p)?;
            Err(p.error.clone())
        }
        result => {
            let result = result.map(|_| ());
            finish_staging(directory, p, result.clone())?;
            *fallback = None;
            result
        }
    }
}

/// Install new files only. No project plan, stop, shutdown or shell restart.
#[tauri::command]
pub async fn stage_app_update(app: tauri::AppHandle) -> Result<(), String> {
    production_only()?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let mut session = state.0.try_lock().map_err(|_| "正在处理更新，请稍后重试")?;
        let directory = update_dir()?;
        reconcile_staging_with_identity(&directory, &mut session.installer_identity)?;
        if let Some(p) = crate::update_transaction::load(&directory)? {
            if matches!(p.phase.as_str(), "staging" | "installing" | "restoring") || !p.remaining.is_empty() {
                return Err("上次更新尚未结束，请先查看更新结果".into());
            }
            if p.phase == "staged" { return verify_installed_version(&p.version); }
        }
        let (version, installer) = update_input(&session)?;
        if let Some(ready) = &installer {
            if installer_is_msi()? || ready.path.extension().and_then(|s| s.to_str()) != Some("exe") {
                return Err("此安装方式不支持后台安装。请先停止项目并完整退出 RunDock，再手动安装；不会自动停止项目。".into());
            }
        }
        let mut p = crate::update_transaction::Pending {
            version, installer, phase: "staging".into(), projects: vec![], remaining: vec![], error: String::new(), failures: vec![],
        };
        stage_files_with_identity(&directory, &mut p, &mut session.installer_identity)?;
        session.installed = Some((p.version, false));
        session.ready = None;
        Ok(())
    }).await.map_err(|e| e.to_string())?
}
#[tauri::command]
pub async fn cancel_scheduled_update(keep_current_state: Option<bool>) -> Result<(), String> {
    tauri::async_runtime::spawn_blocking(move || {
        crate::update_transaction::acknowledge(&update_dir()?, keep_current_state == Some(true))
    }).await.map_err(|e| e.to_string())?
}
#[tauri::command]
pub async fn retry_update_restore() -> Result<(), String> {
    production_only()?;
    let d = update_dir()?;
    tauri::async_runtime::spawn_blocking(move || {
        crate::update_transaction::begin_restore(&d)?;
        crate::update_transaction::restore(&d, crate::SIDECAR_PORT.parse().unwrap())
    })
    .await
    .map_err(|e| e.to_string())?
}

fn recover_projects(
    directory: &Path,
    port: u16,
    p: &mut crate::update_transaction::Pending,
    error: String,
) -> Result<(), String> {
    p.error = error.clone();
    p.phase = "restoring".into();
    crate::update_transaction::save(directory, p)?;
    if !crate::update_transaction::connected(port) {
        let d = directory.to_path_buf();
        if crate::spawn_sidecar(&d).is_ok() {
            let _ = crate::wait_for_sidecar(&d, 15);
        }
    }
    let _ = crate::update_transaction::restore(directory, port);
    Err(error)
}
fn perform_update(
    app: &tauri::AppHandle,
    directory: &Path,
    port: u16,
    mut p: crate::update_transaction::Pending,
) -> Result<(), String> {
    // Old deferred records must not silently enter the removed stop-then-install
    // path. Restore their saved projects and offer the new safe installation flow.
    if p.installer.is_some() {
        return recover_projects(
            directory,
            port,
            &mut p,
            "旧版安装安排已取消。项目恢复后，请重新检查更新并后台安装。".into(),
        );
    }
    p.phase = "installing".into();
    if let Err(e) = crate::update_transaction::save(directory, &p) {
        return recover_projects(directory, port, &mut p, e);
    }
    let result = (|| {
        #[cfg(windows)]
        {
            let current = std::env::current_exe().map_err(|e| e.to_string())?;
            let target = crate::desktop_restart::installed_executable(&current)?;
            if crate::desktop_restart::file_version(&target)? != p.version {
                return Err("安装后的版本不匹配，已尝试恢复项目，请重新下载安装包".into());
            }
            p.phase = "restoring".into();
            crate::update_transaction::save(directory, &p)?;
            crate::desktop_restart::prepare(&current, &p.version)?;
        }
        Ok(())
    })();
    if let Err(e) = result {
        return recover_projects(directory, port, &mut p, e);
    }
    app.exit(0);
    Ok(())
}

#[tauri::command]
pub async fn install_app_update(app: tauri::AppHandle, fingerprint: String) -> Result<(), String> {
    production_only()?;
    tauri::async_runtime::spawn_blocking(move || {
        let state = app.state::<UpdateState>();
        let mut session = state.0.try_lock().map_err(|_| "正在处理更新")?;
        let (version, installer) = update_input(&session)?;
        if installer.is_some() {
            return Err("请先完成后台安装，再选择立即生效；项目尚未停止。".into());
        }
        verify_installed_version(&version)?;
        let plan = session.plan.take().ok_or("请先查看将停止的项目")?;
        if plan.fingerprint != fingerprint {
            return Err("确认已失效，请重新查看项目清单".into());
        }
        let d = update_dir()?;
        let port = crate::SIDECAR_PORT.parse().unwrap();
        let mut p = crate::update_transaction::Pending {
            version,
            installer,
            phase: "restoring".into(),
            remaining: plan.projects.clone(),
            projects: plan.projects.clone(),
            error: String::new(),
            failures: vec![],
        };
        crate::update_transaction::save(&d, &p)?;
        if let Err((e, restore)) = crate::update_transaction::stop_confirmed(port, &plan) {
            if restore {
                return recover_projects(&d, port, &mut p, e);
            }
            p.phase = "finished".into();
            p.error = e.clone();
            p.remaining.clear();
            crate::update_transaction::save(&d, &p)?;
            return Err(e);
        }
        perform_update(&app, &d, port, p)
    })
    .await
    .map_err(|e| e.to_string())?
}

/// Called once in desktop setup, BEFORE creating the backend. A retained backend
/// (including tray/keep-projects mode) always postpones a scheduled installation.
pub fn startup(app: &tauri::AppHandle, directory: &Path, port: u16) -> Result<bool, String> {
    if cfg!(debug_assertions) || !cfg!(target_os = "windows") {
        return Ok(false);
    }
    reconcile_staging(directory)?;
    let Some(mut p) = crate::update_transaction::load(directory)? else {
        return Ok(false);
    };
    if p.phase == "staged" {
        // A retained backend must not be called updated. Keep the staged status
        // until a complete start uses both new shell and new backend.
        if !crate::update_transaction::connected(port) && p.version == env!("CARGO_PKG_VERSION") {
            verify_installed_version(&p.version)?;
            p.phase = "restoring".into();
            crate::update_transaction::save(directory, &p)?;
        }
        return Ok(false);
    }
    if !crate::update_transaction::can_install_on_start(&p, port) {
        return Ok(false);
    }
    if let Err(e) = perform_update(app, directory, port, p.clone()) {
        // Do not retry an unattended failed installation on every launch.
        p = crate::update_transaction::load(directory)?.unwrap_or(p);
        if p.phase == "scheduled" {
            p.phase = "finished".into();
            p.error = e;
            crate::update_transaction::save(directory, &p)?;
        }
        return Ok(false);
    }
    Ok(true)
}

// Keep argument construction separate so tests never execute an installer.
fn installer_command(
    path: &Path,
    system_root: Option<&std::ffi::OsStr>,
) -> Result<std::process::Command, String> {
    match path.extension().and_then(|extension| extension.to_str()) {
        Some("exe") => {
            let mut command = std::process::Command::new(path);
            // /UPDATE skips uninstall; /S is silent. Hooks replace files while
            // loaded images keep running, and /P avoids a finish-page relaunch.
            command.args(["/S", "/UPDATE", "/P"]);
            Ok(command)
        }
        Some("msi") => {
            let root = system_root.ok_or("无法找到 Windows 安装服务")?;
            let mut command =
                std::process::Command::new(PathBuf::from(root).join("System32/msiexec.exe"));
            command.arg("/i").arg(path).args([
                "/quiet",
                "/norestart",
                "MSIRESTARTMANAGERCONTROL=Disable",
            ]);
            Ok(command)
        }
        _ => Err("安装包格式不受支持，请重新检查更新".into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(windows)]
    fn staging_record() -> crate::update_transaction::Pending {
        crate::update_transaction::Pending {
            version: "99.0.0".into(),
            installer: None,
            phase: "staging".into(),
            projects: vec![],
            remaining: vec![],
            error: String::new(),
            failures: vec![],
        }
    }

    #[test]
    #[cfg(windows)]
    fn missing_or_corrupt_identity_never_verifies_or_completes_staging() {
        for corrupt in [false, true] {
            let directory = crate::installer_process::tests::fixture_dir("unknown");
            crate::update_transaction::save(&directory, &staging_record()).unwrap();
            if corrupt {
                fs::write(installer_identity_path(&directory), b"{\"pid\":").unwrap();
            }
            reconcile_staging_checked(&directory, &mut None, |_| {
                panic!("unknown installer identity must not verify installed files")
            })
            .unwrap();
            let pending = crate::update_transaction::load(&directory)
                .unwrap()
                .unwrap();
            assert_eq!(pending.phase, "staging");
            assert!(pending.error.contains("无法确认安装是否结束"));
            assert!(pending.error.contains("可以关闭窗口"));
            assert!(ensure_staging_resolved(&directory).is_err());
        }
    }

    #[test]
    #[cfg(windows)]
    fn tracking_write_failure_is_bounded_and_memory_identity_recovers_after_exit() {
        let directory = crate::installer_process::tests::fixture_dir("tracking-failure");
        let mut pending = staging_record();
        crate::update_transaction::save(&directory, &pending).unwrap();
        // Block only the identity temporary file; the pending journal is writable.
        fs::create_dir(installer_identity_path(&directory).with_extension("json.tmp")).unwrap();
        let mut child = crate::installer_process::tests::fixture();
        let mut identity = None;
        let start = std::time::Instant::now();
        assert!(!track_and_wait_installer(
            &directory,
            &mut pending,
            &mut child.0,
            &mut identity,
            Duration::from_millis(40)
        )
        .unwrap());
        assert!(start.elapsed() < Duration::from_secs(1));
        assert!(pending.error.contains("无法保存安装进程身份"));
        assert!(pending.error.contains("可以关闭窗口"));
        assert!(crate::installer_process::alive(identity.as_ref().unwrap()).unwrap());
        assert!(!installer_identity_path(&directory).exists());
        crate::update_transaction::save(&directory, &pending).unwrap();
        reconcile_staging_checked(&directory, &mut identity, |_| {
            panic!("a live installer must not be declared complete")
        })
        .unwrap();
        // A different session has no fallback and must remain fail-closed.
        reconcile_staging_checked(&directory, &mut None, |_| {
            panic!("missing durable identity must not be treated as an exited installer")
        })
        .unwrap();
        assert!(ensure_staging_resolved(&directory).is_err());
        assert!(
            crate::installer_process::wait_for_exit(&mut child.0, Duration::from_secs(6))
                .unwrap()
                .unwrap()
                .success()
        );
        reconcile_staging_checked(&directory, &mut identity, |_| Ok(())).unwrap();
        assert_eq!(
            crate::update_transaction::load(&directory)
                .unwrap()
                .unwrap()
                .phase,
            "staged"
        );
        assert!(identity.is_none());
        assert!(ensure_staging_resolved(&directory).is_ok());
    }

    #[test]
    #[cfg(windows)]
    fn timeout_keeps_installer_alive_and_durable_identity_reconciles_after_exit() {
        let directory = crate::installer_process::tests::fixture_dir("timeout");
        let mut pending = staging_record();
        crate::update_transaction::save(&directory, &pending).unwrap();
        let mut child = crate::installer_process::tests::fixture();
        let mut identity = None;
        assert!(!track_and_wait_installer(
            &directory,
            &mut pending,
            &mut child.0,
            &mut identity,
            Duration::from_millis(40)
        )
        .unwrap());
        assert_eq!(
            crate::installer_process::load(&installer_identity_path(&directory)).unwrap(),
            identity.clone().unwrap()
        );
        crate::update_transaction::save(&directory, &pending).unwrap();
        let mut restarted_session = None;
        reconcile_staging_checked(&directory, &mut restarted_session, |_| {
            panic!("a timed out but live installer must remain staging")
        })
        .unwrap();
        assert!(ensure_staging_resolved(&directory).is_err());
        assert!(
            crate::installer_process::wait_for_exit(&mut child.0, Duration::from_secs(6))
                .unwrap()
                .unwrap()
                .success()
        );
        reconcile_staging_checked(&directory, &mut restarted_session, |_| Ok(())).unwrap();
        assert_eq!(
            crate::update_transaction::load(&directory)
                .unwrap()
                .unwrap()
                .phase,
            "staged"
        );
        assert!(!installer_identity_path(&directory).exists());
    }

    #[test]
    fn nsis_handoff_is_a_silent_in_place_update_not_an_uninstall() {
        let path = Path::new("C:/test cache/中文/RunDock_2.0.27_x64-setup.exe");
        let command = installer_command(path, None).unwrap();
        assert_eq!(command.get_program(), path.as_os_str());
        assert_eq!(
            command.get_args().collect::<Vec<_>>(),
            ["/S", "/UPDATE", "/P"]
        );
    }

    #[test]
    fn msi_handoff_uses_windows_installer_without_reboot_or_uninstall() {
        let path = Path::new("C:/test cache/中文/RunDock_2.0.27_x64_en-US.msi");
        let command = installer_command(path, Some(std::ffi::OsStr::new("C:/Windows"))).unwrap();
        assert_eq!(
            Path::new(command.get_program()),
            Path::new("C:/Windows/System32/msiexec.exe")
        );
        assert_eq!(
            command.get_args().collect::<Vec<_>>(),
            [
                std::ffi::OsStr::new("/i"),
                path.as_os_str(),
                std::ffi::OsStr::new("/quiet"),
                std::ffi::OsStr::new("/norestart"),
                std::ffi::OsStr::new("MSIRESTARTMANAGERCONTROL=Disable"),
            ]
        );
        assert!(installer_command(path, None).is_err());
        assert!(installer_command(Path::new("C:/unexpected.cmd"), None).is_err());
    }

    fn release(version: &str) -> Release {
        let tag = format!("v{version}");
        Release {
            tag_name: tag.clone(),
            draft: false,
            prerelease: false,
            body: Some("notes".into()),
            assets: [
                format!("RunDock_{version}_x64-setup.exe"),
                format!("RunDock_{version}_x64_en-US.msi"),
                "SHA256SUMS.txt".into(),
            ]
            .into_iter()
            .map(|name| Asset {
                browser_download_url: format!("{REPOSITORY}/releases/download/{tag}/{name}"),
                name,
                size: 10,
            })
            .collect(),
        }
    }
    #[test]
    fn selects_newest_complete_stable_installer_not_source_only_or_prerelease() {
        let mut preview = release("9.0.0");
        preview.prerelease = true;
        let mut source = release("8.0.0");
        source.assets.clear();
        let selected = select_update(
            vec![release("2.0.9"), source, preview, release("2.0.12")],
            &Version::parse("2.0.10").unwrap(),
            true,
        )
        .unwrap();
        assert_eq!(selected.info.version, "2.0.12");
        assert!(selected.installer.name.ends_with(".msi"));
        assert!(select_update(
            vec![release("2.0.9")],
            &Version::parse("2.0.10").unwrap(),
            false
        )
        .is_none());
    }
    #[test]
    fn rejects_untrusted_asset_and_duplicate_or_missing_checksum() {
        let mut r = release("3.0.0");
        r.assets[0].browser_download_url = "https://evil.example/setup.exe".into();
        assert!(select_update(vec![r], &Version::parse("2.0.0").unwrap(), false).is_none());
        assert!(expected_hash("abcd setup.exe", "setup.exe").is_err());
        let line = format!("{}  setup.exe", "a".repeat(64));
        assert!(expected_hash(&format!("{line}\n{line}"), "setup.exe").is_err());
    }
    #[test]
    fn verifies_bytes_and_rejects_corruption_and_truncation() {
        let data = b"test installer";
        let hash = format!("{:x}", Sha256::digest(data));
        let mut result = Vec::new();
        copy_verified(&data[..], &mut result, data.len() as u64, &hash, |_| {}).unwrap();
        assert_eq!(result, data);
        assert!(copy_verified(
            &data[..3],
            std::io::sink(),
            data.len() as u64,
            &hash,
            |_| {}
        )
        .is_err());
        assert!(copy_verified(&data[..], std::io::sink(), 3, &hash, |_| {}).is_err());
        assert!(copy_verified(
            &data[..],
            std::io::sink(),
            data.len() as u64,
            &"0".repeat(64),
            |_| {}
        )
        .is_err());
    }

    #[test]
    #[ignore = "Downloads the public installer from GitHub; never executes it"]
    fn github_download_smoke() {
        let candidate = fetch_candidate(&Version::new(0, 0, 0), false)
            .unwrap()
            .expect("published Windows release");
        let directory =
            std::env::temp_dir().join(format!("rundock-update-smoke-{}", std::process::id()));
        let ready = download(&candidate, &directory, |_| {}).unwrap();
        verify_ready(&ready).unwrap();
        println!(
            "Verified GitHub installer v{} ({} bytes)",
            candidate.info.version, ready.size
        );
        fs::remove_file(&ready.path).unwrap();
        fs::remove_dir(&directory).unwrap();
    }
}
