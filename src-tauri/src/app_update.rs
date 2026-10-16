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
        let current = Version::parse(env!("CARGO_PKG_VERSION")).map_err(|e| e.to_string())?;
        #[cfg(windows)]
        if !cfg!(debug_assertions) {
            let target = crate::desktop_restart::installed_executable(
                &std::env::current_exe().map_err(|e| e.to_string())?,
            )?;
            let installed = crate::desktop_restart::file_version(&target)?;
            if Version::parse(&installed).map_err(|e| e.to_string())? > current {
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
    if pending.phase != "scheduled" {
        return Err("请先下载安装包".into());
    }
    if let Some(ready) = &pending.installer {
        verify_ready(ready)?
    }
    Ok((pending.version, pending.installer))
}
#[tauri::command]
pub fn app_update_status() -> Result<Option<crate::update_transaction::Pending>, String> {
    crate::update_transaction::load(&update_dir()?)
}
#[tauri::command]
pub fn cancel_scheduled_update() -> Result<(), String> {
    let d = update_dir()?;
    if let Some(p) = crate::update_transaction::load(&d)? {
        if p.phase != "scheduled" && p.phase != "finished" {
            return Err("更新正在执行，请稍后重试".into());
        }
        fs::remove_file(crate::update_transaction::path(&d)).map_err(|e| e.to_string())?;
    }
    Ok(())
}
#[tauri::command]
pub async fn retry_update_restore() -> Result<(), String> {
    production_only()?;
    let d = update_dir()?;
    let mut p = crate::update_transaction::load(&d)?.ok_or("没有待恢复的项目")?;
    if p.phase != "finished" {
        return Err("正在恢复项目，请稍候".into());
    }
    p.phase = "restoring".into();
    crate::update_transaction::save(&d, &p)?;
    tauri::async_runtime::spawn_blocking(move || {
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
    // Verify before stopping anything; write intent before each destructive phase.
    if let Some(ready) = &p.installer {
        if let Err(e) = verify_ready(ready) {
            return recover_projects(directory, port, &mut p, e);
        }
    }
    p.phase = "installing".into();
    if let Err(e) = crate::update_transaction::save(directory, &p) {
        return recover_projects(directory, port, &mut p, e);
    }
    let result = (|| {
        if let Some(ready) = &p.installer {
            let status = installer_command(&ready.path, std::env::var_os("SystemRoot").as_deref())?
                .spawn()
                .map_err(|e| format!("无法启动安装：{e}"))?
                .wait()
                .map_err(|e| e.to_string())?;
            if status.code() == Some(3010) {
                return Err("安装需要重启电脑。请保存工作后手动重启；项目恢复情况见下方".into());
            }
            if !status.success() {
                return Err(format!(
                    "安装未完成（退出码 {:?}），已尝试恢复原先运行的项目",
                    status.code()
                ));
            }
        }
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
        if let Err((e, restore)) = crate::update_transaction::stop(port, &plan) {
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
    let Some(mut p) = crate::update_transaction::load(directory)? else {
        return Ok(false);
    };
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
            // The transaction already stopped managed projects and the backend.
            // Keep the old shell available to show a failed-install report.
            command.args(["/UPDATE", "/P"]);
            Ok(command)
        }
        Some("msi") => {
            let root = system_root.ok_or("无法找到 Windows 安装服务")?;
            let mut command =
                std::process::Command::new(PathBuf::from(root).join("System32/msiexec.exe"));
            command.arg("/i").arg(path).args([
                "/passive",
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
    #[test]
    fn nsis_handoff_is_a_visible_in_place_update_not_an_uninstall() {
        let path = Path::new("C:/test cache/中文/RunDock_2.0.27_x64-setup.exe");
        let command = installer_command(path, None).unwrap();
        assert_eq!(command.get_program(), path.as_os_str());
        assert_eq!(command.get_args().collect::<Vec<_>>(), ["/UPDATE", "/P"]);
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
                std::ffi::OsStr::new("/passive"),
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
