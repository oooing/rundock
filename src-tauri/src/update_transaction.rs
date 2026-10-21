//! Durable update intent and project restoration. No arbitrary paths from IPC.
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::Write,
    net::{SocketAddr, TcpStream},
    path::{Path, PathBuf},
    sync::Mutex,
    time::{Duration, Instant},
};

// Finished-result reconciliation must never overwrite a concurrent restore.
static RECOVERY_LOCK: Mutex<()> = Mutex::new(());

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Project {
    pub id: String,
    pub name: String,
    pub run_id: String,
    pub config_hash: String,
    #[serde(default)]
    pub temporary_port: u16,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Plan {
    pub instance_id: String,
    pub fingerprint: String,
    pub projects: Vec<Project>,
}
#[derive(Clone, Serialize, Deserialize)]
pub struct Pending {
    pub version: String,
    pub installer: Option<super::app_update::Ready>,
    pub phase: String,
    pub projects: Vec<Project>,
    pub remaining: Vec<Project>,
    pub error: String,
    #[serde(default)]
    pub failures: Vec<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    #[serde(flatten)]
    pub pending: Pending,
    pub current_version: String,
    pub backend_version: Option<String>,
    pub version_active: bool,
    pub status_error: String,
}

fn at_least(actual: &str, expected: &str) -> bool {
    match (semver::Version::parse(actual), semver::Version::parse(expected)) {
        (Ok(a), Ok(b)) => a >= b,
        _ => false,
    }
}

/// Keep the original evidence before reconciling or acknowledging an attempt.
/// Content-addressed names make repeated reads idempotent.
pub fn archive(directory: &Path, p: &Pending) -> Result<(), String> {
    use sha2::{Digest, Sha256};
    let bytes = serde_json::to_vec(p).map_err(|e| e.to_string())?;
    let history = directory.join("update-history");
    fs::create_dir_all(&history).map_err(|e| e.to_string())?;
    let target = history.join(format!("{:x}.json", Sha256::digest(&bytes)));
    if !target.exists() {
        fs::write(target, bytes).map_err(|e| e.to_string())?;
    }
    Ok(())
}

fn reconcile_finished(p: &mut Pending, active: bool, apps: &[serde_json::Value]) {
    if active {
        p.error.clear();
    }
    p.failures.clear();
    p.remaining.retain(|project| {
        let app = apps.iter().find(|a| a["id"].as_str() == Some(&project.id));
        let message = match app.and_then(|a| a["status"].as_str()) {
            Some("running") if app.and_then(|a| a["runId"].as_str()).is_some_and(|id| !id.is_empty()) => return false,
            Some("running") => "项目由其他方式运行，未接管；请在项目卡片确认。",
            Some("starting") => "项目正在启动，稍后刷新结果。",
            Some("stopped") => "项目已停止，可点击“恢复项目”重新启动。",
            Some("failed") => "项目启动失败，请在项目卡片查看原因后恢复。",
            Some("degraded") => "项目正在运行，但部分服务异常；请在项目卡片查看原因。",
            Some("stopping") => "项目正在停止，请等待停止完成后恢复。",
            None => "项目已删除或无法读取，请检查项目列表。",
            _ => "无法确认项目状态，请稍后刷新。",
        };
        p.failures.push(format!("{}：{message}", project.name));
        true
    });
}

/// A status read never starts or stops projects. Only verified running versions
/// can retire an old installation error; unresolved recovery remains explicit.
pub fn status(directory: &Path, port: u16, current: &str) -> Result<Option<Status>, String> {
    let Some(mut p) = load(directory)? else { return Ok(None) };
    let snapshot = serde_json::to_vec(&p).map_err(|e| e.to_string())?;
    let health = request_with_timeout(port, "/api/health", None, Duration::from_secs(3));
    let backend = health.ok().and_then(|v| v["version"].as_str().map(str::to_owned));
    let active = backend.as_deref().is_some_and(|v| at_least(v, &p.version))
        && at_least(current, &p.version);
    let mut status_error = String::new();
    if p.phase == "finished" {
        // Network reads must not hold the recovery lock: acknowledging a result
        // or retrying recovery must remain available while a status poll waits.
        let apps = request_with_timeout(port, "/api/apps", None, Duration::from_secs(5));
        if let Ok(_guard) = RECOVERY_LOCK.try_lock() {
            let Some(latest) = load(directory)? else { return Ok(None) };
            if serde_json::to_vec(&latest).map_err(|e| e.to_string())? != snapshot {
                // A user action won the race. Never resurrect a removed result
                // or replace a newer restore with this stale observation.
                let version_active = backend.as_deref().is_some_and(|v| at_least(v, &latest.version))
                    && at_least(current, &latest.version);
                return Ok(Some(Status { pending: latest, current_version: current.into(),
                    backend_version: backend, version_active, status_error }));
            }
            match apps {
            Ok(apps) if apps.is_array() && backend.is_some() => {
                let before = p.clone();
                reconcile_finished(&mut p, active, apps.as_array().unwrap());
                if serde_json::to_vec(&before).ok() != serde_json::to_vec(&p).ok() {
                    archive(directory, &before)?;
                    save(directory, &p)?;
                }
                // A fully resolved old attempt must not block future checks.
                if active && p.remaining.is_empty() && p.error.is_empty()
                    && current != p.version && backend.as_deref() == Some(current)
                {
                    archive(directory, &p)?;
                    fs::remove_file(path(directory)).map_err(|e| e.to_string())?;
                    return Ok(None);
                }
            }
            _ => status_error = "暂时无法读取后台状态，请稍后刷新；未重新启动项目。".into(),
            }
        }
    }
    Ok(Some(Status { pending: p, current_version: current.into(),
        backend_version: backend, version_active: active, status_error }))
}

fn recovery_write_lock() -> Result<std::sync::MutexGuard<'static, ()>, String> {
    let deadline = Instant::now() + Duration::from_secs(1);
    loop {
        match RECOVERY_LOCK.try_lock() {
            Ok(guard) => return Ok(guard),
            Err(std::sync::TryLockError::Poisoned(_)) => return Err("恢复状态不可用，请重新打开 RunDock".into()),
            Err(_) if Instant::now() < deadline => std::thread::sleep(Duration::from_millis(10)),
            Err(_) => return Err("正在处理恢复，请稍候".into()),
        }
    }
}

pub fn begin_restore(directory: &Path) -> Result<(), String> {
    let _guard = recovery_write_lock()?;
    let mut p = load(directory)?.ok_or("没有待恢复的项目")?;
    if p.phase != "finished" || p.remaining.is_empty() {
        return Err("没有待恢复的项目，或恢复正在进行".into());
    }
    p.phase = "restoring".into();
    p.failures.clear();
    save(directory, &p)
}

pub fn acknowledge(directory: &Path, keep_current_state: bool) -> Result<(), String> {
    let _guard = recovery_write_lock()?;
    if let Some(p) = load(directory)? {
        if !matches!(p.phase.as_str(), "scheduled" | "finished") || (!p.remaining.is_empty() && !keep_current_state) {
            return Err("仍有项目待恢复，请先处理恢复结果".into());
        }
        archive(directory, &p)?;
        fs::remove_file(path(directory)).map_err(|e| e.to_string())?;
    }
    Ok(())
}
pub fn path(directory: &Path) -> PathBuf {
    directory.join("desktop-update.json")
}
pub fn load(directory: &Path) -> Result<Option<Pending>, String> {
    match fs::read(path(directory)) {
        Ok(bytes) => serde_json::from_slice(&bytes)
            .map(Some)
            .map_err(|_| "更新记录损坏，请保留记录并重新检查更新".into()),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(None),
        Err(e) => Err(e.to_string()),
    }
}
pub fn save(directory: &Path, p: &Pending) -> Result<(), String> {
    fs::create_dir_all(directory).map_err(|e| e.to_string())?;
    let tmp = directory.join("desktop-update.json.tmp");
    let target = path(directory);
    let mut f = fs::File::create(&tmp).map_err(|e| e.to_string())?;
    f.write_all(&serde_json::to_vec(p).map_err(|e| e.to_string())?)
        .map_err(|e| e.to_string())?;
    f.sync_all().map_err(|e| e.to_string())?;
    drop(f);
    #[cfg(windows)]
    {
        use std::os::windows::ffi::OsStrExt;
        #[link(name = "kernel32")]
        extern "system" {
            fn MoveFileExW(from: *const u16, to: *const u16, flags: u32) -> i32;
        }
        let a: Vec<u16> = tmp.as_os_str().encode_wide().chain(Some(0)).collect();
        let b: Vec<u16> = target.as_os_str().encode_wide().chain(Some(0)).collect();
        if unsafe { MoveFileExW(a.as_ptr(), b.as_ptr(), 1 | 8) } == 0 {
            return Err(std::io::Error::last_os_error().to_string());
        }
    }
    #[cfg(not(windows))]
    fs::rename(tmp, target).map_err(|e| e.to_string())?;
    Ok(())
}
pub fn connected(port: u16) -> bool {
    TcpStream::connect_timeout(
        &SocketAddr::from(([127, 0, 0, 1], port)),
        Duration::from_millis(400),
    )
    .is_ok()
}
pub fn can_install_on_start(p: &Pending, port: u16) -> bool {
    p.phase == "scheduled" && !connected(port)
}
/// Capture the actual running set at full exit, not at download/schedule time.
pub fn prepare_scheduled_exit(directory: &Path, port: u16) -> Result<bool, String> {
    let Some(mut p) = load(directory)? else {
        return Ok(false);
    };
    if !matches!(p.phase.as_str(), "scheduled" | "staged") {
        return Ok(false);
    }
    let staged = p.phase == "staged";
    let snapshot = plan(port)?;
    p.projects = snapshot.projects.clone();
    p.remaining = snapshot.projects.clone();
    // Persist recovery before stopping. A crash during full exit must not lose
    // the original running set, even though installation already finished.
    if staged {
        p.phase = "restoring".into();
    }
    save(directory, &p)?;
    if let Err((error, needs_restore)) = stop(port, &snapshot) {
        if needs_restore {
            p.phase = "restoring".into();
            p.error = error.clone();
            save(directory, &p)?;
            let _ = restore(directory, port);
        } else {
            if staged {
                p.phase = "staged".into();
            }
            p.projects.clear();
            p.remaining.clear();
            save(directory, &p)?;
        }
        return Err(error);
    }
    Ok(true)
}
fn client() -> Result<Client, String> {
    Client::builder()
        .no_proxy()
        .connect_timeout(Duration::from_secs(2))
        .timeout(Duration::from_secs(120))
        .build()
        .map_err(|e| e.to_string())
}
pub fn request(
    port: u16,
    route: &str,
    body: Option<&serde_json::Value>,
) -> Result<serde_json::Value, String> {
    request_with_timeout(port, route, body, Duration::from_secs(120))
}
fn request_with_timeout(
    port: u16,
    route: &str,
    body: Option<&serde_json::Value>,
    timeout: Duration,
) -> Result<serde_json::Value, String> {
    let c = Client::builder()
        .no_proxy()
        .connect_timeout(Duration::from_secs(2))
        .timeout(timeout)
        .build()
        .map_err(|e| e.to_string())?;
    let url = format!("http://127.0.0.1:{port}{route}");
    let r = if let Some(b) = body {
        c.post(url).json(b).send()
    } else {
        c.get(url).send()
    }
    .map_err(|e| format!("无法连接后台：{e}"))?;
    let status = r.status();
    let data: serde_json::Value = r
        .json()
        .map_err(|_| "后台不支持完整更新，请先退出所有项目后重新启动 RunDock".to_string())?;
    if !status.is_success() {
        return Err(data["error"]
            .as_str()
            .unwrap_or("后台未完成请求，请稍后重试")
            .to_string());
    }
    Ok(data)
}
pub fn plan(port: u16) -> Result<Plan, String> {
    serde_json::from_value(request(port, "/api/desktop/update-plan", None)?)
        .map_err(|_| "后台版本过旧，请完整退出后重新打开 RunDock".into())
}
pub fn stop(port: u16, plan: &Plan) -> Result<(), (String, bool)> {
    stop_with_confirmation(port, plan, false)
}

/// Only the explicitly confirmed activation path may opt into normal manual-stop semantics.
pub fn stop_confirmed(port: u16, plan: &Plan) -> Result<(), (String, bool)> {
    stop_with_confirmation(port, plan, true)
}

fn stop_with_confirmation(port: u16, plan: &Plan, confirmed: bool) -> Result<(), (String, bool)> {
    let c = client().map_err(|e| (e, false))?;
    let mut body = serde_json::json!({"fingerprint":plan.fingerprint});
    if confirmed {
        body["confirmedStop"] = serde_json::Value::Bool(true);
    }
    let response = c
        .post(format!("http://127.0.0.1:{port}/api/desktop/update-stop"))
        .json(&body)
        .send()
        .map_err(|e| (format!("未能确认停止结果，未切换新版：{e}"), true))?;
    let status = response.status();
    let response: serde_json::Value = response
        .json()
        .map_err(|_| ("后台响应无效，未切换新版".to_string(), true))?;
    if !status.is_success() {
        return Err((
            response["error"]
                .as_str()
                .unwrap_or("停止项目失败")
                .replace("安装已取消", "新版未启用")
                .replace("已取消安装", "未切换新版"),
            response["restoreRequired"] == true,
        ));
    }
    if response["shuttingDown"] != true {
        return Err(("后台未确认退出，未切换新版".into(), true));
    }
    let deadline = Instant::now() + Duration::from_secs(30);
    while connected(port) {
        if Instant::now() > deadline {
            return Err(("后台尚未退出，未切换新版。请稍后重试".into(), true));
        }
        std::thread::sleep(Duration::from_millis(200))
    }
    Ok(())
}
fn restore_status(status: Option<&str>) -> Result<bool, String> {
    match status {
        Some("running") => Ok(true),
        Some("starting") => Ok(false),
        Some("degraded") => {
            Err("项目仍在运行，但服务状态异常；请查看项目记录，必要时点击“重启”后再恢复。".into())
        }
        Some("stopping") => Err("项目尚未停止，暂不能恢复；请等待停止完成后重试。".into()),
        Some("failed" | "stopped") => Err("启动失败；请在项目卡片查看错误并重新启动。".into()),
        _ => Err("无法识别项目状态，未确认恢复；请刷新项目列表后重试。".into()),
    }
}
pub fn restore(directory: &Path, port: u16) -> Result<(), String> {
    let _guard = RECOVERY_LOCK.try_lock().map_err(|_| "正在恢复项目，请稍候")?;
    let Some(mut p) = load(directory)? else {
        return Ok(());
    };
    if !["restoring", "installing"].contains(&p.phase.as_str()) {
        return Ok(());
    }
    if p.phase == "installing" {
        p.error = "上次安装被中断，已尝试恢复项目。请重新检查更新".into();
    }
    p.phase = "restoring".into();
    p.failures.clear();
    if p.error.is_empty() {
        match request(port, "/api/health", None) {
            Ok(health) if health["version"].as_str() == Some(p.version.as_str()) => {}
            _ => {
                p.error =
                    "后台版本尚未确认更新，已尝试恢复项目。请完整退出后重新打开 RunDock".into()
            }
        }
    }
    save(directory, &p)?;
    let remaining = p.remaining.clone();
    for project in remaining {
        let result: Result<(), String> = (|| {
            let until = Instant::now() + Duration::from_secs(60);
            let response = request_with_timeout(
                port,
                "/api/desktop/update-restore",
                Some(&serde_json::to_value(&project).map_err(|e| e.to_string())?),
                Duration::from_secs(15),
            )?;
            if restore_status(response["status"].as_str())? {
                return Ok(());
            }
            loop {
                if Instant::now() > until {
                    return Err(
                        "项目启动超过 60 秒仍未就绪；请查看项目记录，处理启动错误后重试恢复。"
                            .into(),
                    );
                }
                let app = request_with_timeout(
                    port,
                    &format!("/api/apps/{}", project.id),
                    None,
                    until
                        .saturating_duration_since(Instant::now())
                        .min(Duration::from_secs(3)),
                )?;
                if restore_status(app["status"].as_str())? {
                    return Ok(());
                }
                std::thread::sleep(Duration::from_millis(500));
            }
        })();
        match result {
            Ok(()) => p.remaining.retain(|v| v.id != project.id),
            Err(e) => p.failures.push(format!("{}：{}", project.name, e)),
        }
        save(directory, &p)?;
    }
    p.phase = "finished".into();
    save(directory, &p)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn failed_record() -> Pending {
        Pending { version: "2.0.46".into(), installer: None, phase: "finished".into(),
            projects: vec![], remaining: vec![Project { id: "localplay".into(), name: "LocalPlay".into(),
                run_id: "old-run".into(), config_hash: "hash".into(), temporary_port: 0 }],
            error: "old installation failure".into(), failures: vec!["stale degraded warning".into()] }
    }

    #[test]
    fn completed_version_and_project_recovery_are_independent() {
        let mut p = failed_record();
        reconcile_finished(&mut p, true, &[serde_json::json!({"id":"localplay","status":"stopped"})]);
        assert!(p.error.is_empty());
        assert_eq!(p.remaining.len(), 1);
        assert!(p.failures[0].contains("已停止"));
        assert!(!p.failures[0].contains("degraded"));
        reconcile_finished(&mut p, true, &[serde_json::json!({"id":"localplay","status":"running","runId":"new-run"})]);
        assert!(p.remaining.is_empty());
        assert!(p.failures.is_empty());
    }

    #[test]
    fn unhealthy_or_external_projects_are_not_reported_as_restored() {
        for app in [
            serde_json::json!({"id":"localplay","status":"degraded","runId":"new"}),
            serde_json::json!({"id":"localplay","status":"running","runId":""}),
            serde_json::json!({"id":"localplay","status":"starting","runId":"new"}),
        ] {
            let mut p = failed_record();
            reconcile_finished(&mut p, false, &[app]);
            assert_eq!(p.remaining.len(), 1);
            assert_eq!(p.error, "old installation failure");
        }
        assert!(!at_least("unknown", "2.0.46"));
        assert!(!at_least("2.0.45", "2.0.46"));
        assert!(at_least("2.0.47", "2.0.46"));
    }

    #[test]
    fn keeping_current_state_requires_explicit_intent_and_preserves_history() {
        let dir = std::env::temp_dir().join(format!("rundock-ack-{}", std::process::id()));
        let p = failed_record();
        save(&dir, &p).unwrap();
        assert!(acknowledge(&dir, false).is_err());
        assert!(path(&dir).exists());
        acknowledge(&dir, true).unwrap();
        assert!(!path(&dir).exists());
        let history = fs::read_dir(dir.join("update-history")).unwrap().next().unwrap().unwrap().path();
        assert_eq!(fs::read(&history).unwrap(), serde_json::to_vec(&p).unwrap());
        fs::remove_file(history).unwrap();
        fs::remove_dir(dir.join("update-history")).unwrap();
        fs::remove_dir(dir).unwrap();
    }

    #[test]
    fn slow_status_poll_cannot_block_acknowledgement_or_overwrite_a_new_restore() {
        use std::{io::Read, net::TcpListener, sync::mpsc};
        for acknowledge_result in [true, false] {
            let listener = TcpListener::bind("127.0.0.1:0").unwrap();
            let port = listener.local_addr().unwrap().port();
            let dir = std::env::temp_dir().join(format!("rundock-status-race-{}-{port}", std::process::id()));
            let mut p = failed_record();
            if acknowledge_result { p.remaining.clear(); }
            save(&dir, &p).unwrap();
            let (started_tx, started_rx) = mpsc::channel();
            let (release_tx, release_rx) = mpsc::channel();
            let server = std::thread::spawn(move || {
                for (index, body) in [
                    r#"{"version":"2.0.47"}"#,
                    r#"[{"id":"localplay","status":"running","runId":"new"}]"#,
                ].into_iter().enumerate() {
                    let (mut stream, _) = listener.accept().unwrap();
                    stream.set_read_timeout(Some(Duration::from_secs(3))).unwrap();
                    let mut bytes = [0;8192];
                    stream.read(&mut bytes).unwrap();
                    if index == 0 {
                        started_tx.send(()).unwrap();
                        release_rx.recv_timeout(Duration::from_secs(3)).unwrap();
                    }
                    write!(stream,"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",body.len(),body).unwrap();
                }
            });
            let polling_dir = dir.clone();
            let poll = std::thread::spawn(move || status(&polling_dir,port,"2.0.47"));
            started_rx.recv_timeout(Duration::from_secs(3)).unwrap();
            let start = Instant::now();
            let action = if acknowledge_result { acknowledge(&dir,false) } else { begin_restore(&dir) };
            release_tx.send(()).unwrap();
            action.unwrap();
            assert!(start.elapsed() < Duration::from_millis(500), "network poll blocked a user action");
            let observed = poll.join().unwrap().unwrap();
            if acknowledge_result {
                assert!(observed.is_none());
                assert!(!path(&dir).exists(), "stale poll resurrected acknowledged record");
                for file in fs::read_dir(dir.join("update-history")).unwrap() {
                    fs::remove_file(file.unwrap().path()).unwrap();
                }
                fs::remove_dir(dir.join("update-history")).unwrap();
            } else {
                assert_eq!(observed.unwrap().pending.phase,"restoring");
                assert_eq!(load(&dir).unwrap().unwrap().remaining.len(),1);
                fs::remove_file(path(&dir)).unwrap();
            }
            server.join().unwrap();
            fs::remove_dir(dir).unwrap();
        }
    }

    #[test]
    fn old_record_is_archived_only_after_verified_version_and_recovery() {
        use std::{io::Read, net::TcpListener};
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let dir = std::env::temp_dir().join(format!("rundock-reconcile-{}-{port}", std::process::id()));
        let original = failed_record();
        save(&dir, &original).unwrap();
        let server = std::thread::spawn(move || {
            for body in [
                r#"{"version":"2.0.47"}"#,
                r#"[{"id":"localplay","status":"stopped","runId":""}]"#,
                r#"{"version":"2.0.47"}"#,
                r#"[{"id":"localplay","status":"running","runId":"new"}]"#,
            ] {
                let (mut stream, _) = listener.accept().unwrap();
                stream.set_read_timeout(Some(Duration::from_secs(3))).unwrap();
                let mut buffer = [0; 8192];
                let n = stream.read(&mut buffer).unwrap();
                assert!(String::from_utf8_lossy(&buffer[..n]).starts_with("GET "));
                write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}", body.len(), body).unwrap();
            }
        });
        let report = status(&dir, port, "2.0.47").unwrap().unwrap();
        assert!(report.version_active);
        assert!(report.pending.error.is_empty());
        assert_eq!(report.pending.remaining.len(), 1);
        assert!(acknowledge(&dir, false).is_err(), "cannot discard recovery");
        assert!(status(&dir, port, "2.0.47").unwrap().is_none());
        assert!(!path(&dir).exists());
        let history: Vec<_> = fs::read_dir(dir.join("update-history")).unwrap().map(|e| e.unwrap().path()).collect();
        assert!(history.iter().any(|file| fs::read(file).unwrap() == serde_json::to_vec(&original).unwrap()));
        server.join().unwrap();
        for file in history { fs::remove_file(file).unwrap(); }
        fs::remove_dir(dir.join("update-history")).unwrap();
        fs::remove_dir(dir).unwrap();
    }

    fn captured_stop_body(confirmed: bool) -> serde_json::Value {
        use std::{io::Read, net::TcpListener};
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let server = std::thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            stream
                .set_read_timeout(Some(Duration::from_secs(3)))
                .unwrap();
            let mut request = Vec::new();
            let mut buffer = [0; 1024];
            let body_start = loop {
                let count = stream.read(&mut buffer).unwrap();
                assert_ne!(count, 0, "request ended before headers");
                request.extend_from_slice(&buffer[..count]);
                if let Some(index) = request.windows(4).position(|part| part == b"\r\n\r\n") {
                    break index + 4;
                }
            };
            let headers = String::from_utf8_lossy(&request[..body_start]);
            assert!(headers.starts_with("POST /api/desktop/update-stop HTTP/1.1\r\n"));
            let length = headers
                .lines()
                .find_map(|line| {
                    let (name, value) = line.split_once(':')?;
                    name.eq_ignore_ascii_case("content-length")
                        .then(|| value.trim().parse::<usize>().unwrap())
                })
                .expect("JSON request must have content-length");
            while request.len() < body_start + length {
                let count = stream.read(&mut buffer).unwrap();
                assert_ne!(count, 0, "request body truncated");
                request.extend_from_slice(&buffer[..count]);
            }
            let body = serde_json::from_slice(&request[body_start..body_start + length]).unwrap();
            // Reject without shutdown so the test cannot call a real backend or wait for it.
            let response = r#"{"error":"fixture refusal","restoreRequired":false}"#;
            write!(stream, "HTTP/1.1 409 Conflict\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}", response.len(), response).unwrap();
            body
        });
        let plan = Plan {
            instance_id: "fixture".into(),
            fingerprint: "confirmed-plan".into(),
            projects: vec![],
        };
        let result = if confirmed {
            stop_confirmed(port, &plan)
        } else {
            stop(port, &plan)
        };
        assert_eq!(result, Err(("fixture refusal".into(), false)));
        server.join().unwrap()
    }

    #[test]
    fn default_stop_request_never_opts_into_confirmed_stop() {
        assert_eq!(
            captured_stop_body(false),
            serde_json::json!({"fingerprint":"confirmed-plan"})
        );
    }

    #[test]
    fn confirmed_stop_request_sends_explicit_opt_in_with_fingerprint() {
        assert_eq!(
            captured_stop_body(true),
            serde_json::json!({
                "fingerprint":"confirmed-plan", "confirmedStop":true
            })
        );
    }

    #[test]
    fn staged_full_exit_saves_recovery_before_requesting_stop() {
        use std::{io::Read, net::TcpListener};
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let dir =
            std::env::temp_dir().join(format!("rundock-staged-exit-{}-{port}", std::process::id()));
        save(
            &dir,
            &Pending {
                version: "9.0.0".into(),
                installer: None,
                phase: "staged".into(),
                projects: vec![],
                remaining: vec![],
                error: String::new(),
                failures: vec![],
            },
        )
        .unwrap();
        let server_dir = dir.clone();
        let server = std::thread::spawn(move || {
            for step in 0..2 {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(3)))
                    .unwrap();
                let mut buffer = [0; 8192];
                let n = stream.read(&mut buffer).unwrap();
                let request = String::from_utf8_lossy(&buffer[..n]);
                let body = if step == 0 {
                    assert!(request.starts_with("GET /api/desktop/update-plan"));
                    r#"{"instanceId":"old","fingerprint":"snapshot","projects":[{"id":"a","name":"A","runId":"run","configHash":"hash"}]}"#
                } else {
                    assert!(request.starts_with("POST /api/desktop/update-stop"));
                    let saved = load(&server_dir).unwrap().unwrap();
                    assert_eq!(saved.phase, "restoring");
                    assert_eq!(saved.remaining[0].id, "a");
                    r#"{"shuttingDown":true}"#
                };
                write!(stream,"HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",body.len(),body).unwrap();
            }
        });
        assert!(prepare_scheduled_exit(&dir, port).unwrap());
        server.join().unwrap();
        let result = load(&dir).unwrap().unwrap();
        assert_eq!(result.phase, "restoring");
        assert!(result.installer.is_none());
        assert!(
            !can_install_on_start(&result, port),
            "must not reinstall staged files"
        );
        fs::remove_file(path(&dir)).unwrap();
        fs::remove_dir(dir).unwrap();
    }
    #[test]
    fn degraded_recovery_finishes_promptly_and_continues_other_projects() {
        use std::io::Read;
        use std::net::TcpListener;
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        let server = std::thread::spawn(move || {
            for status in ["degraded", "running"] {
                let (mut stream, _) = listener.accept().unwrap();
                stream
                    .set_read_timeout(Some(Duration::from_secs(3)))
                    .unwrap();
                let mut buffer = [0; 8192];
                let n = stream.read(&mut buffer).unwrap();
                assert!(String::from_utf8_lossy(&buffer[..n])
                    .starts_with("POST /api/desktop/update-restore"));
                let body = format!(r#"{{"status":"{status}"}}"#);
                write!(stream, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}", body.len(), body).unwrap();
            }
        });
        let dir = std::env::temp_dir().join(format!(
            "rundock-recovery-status-{}-{port}",
            std::process::id()
        ));
        let projects: Vec<Project> = ["degraded", "healthy"]
            .into_iter()
            .map(|id| Project {
                id: id.into(),
                name: id.into(),
                run_id: "original".into(),
                config_hash: "fixture".into(),
                temporary_port: 0,
            })
            .collect();
        save(
            &dir,
            &Pending {
                version: "9.0.0".into(),
                installer: None,
                phase: "restoring".into(),
                remaining: projects.clone(),
                projects,
                error: "Installation cancelled".into(),
                failures: vec![],
            },
        )
        .unwrap();
        let started = Instant::now();
        restore(&dir, port).unwrap();
        assert!(
            started.elapsed() < Duration::from_secs(3),
            "must not wait 60 seconds on degraded"
        );
        let result = load(&dir).unwrap().unwrap();
        assert_eq!(result.phase, "finished");
        assert_eq!(result.remaining.len(), 1);
        assert_eq!(result.remaining[0].id, "degraded");
        assert!(result.failures[0].contains("重启"));
        assert_eq!(result.error, "Installation cancelled");
        server.join().unwrap();
        fs::remove_file(path(&dir)).unwrap();
        fs::remove_dir(dir).unwrap();
    }
    #[test]
    fn only_starting_projects_are_polled() {
        assert_eq!(restore_status(Some("running")), Ok(true));
        assert_eq!(restore_status(Some("starting")), Ok(false));
        for status in [
            Some("degraded"),
            Some("stopping"),
            Some("failed"),
            Some("stopped"),
            Some("unknown"),
            None,
        ] {
            assert!(restore_status(status).is_err(), "must not poll {status:?}");
        }
        assert!(restore_status(Some("degraded"))
            .unwrap_err()
            .contains("重启"));
    }
    #[test]
    fn durable_intent_replaces_atomically_and_keeps_restore_list() {
        let d =
            std::env::temp_dir().join(format!("rundock-transaction-test-{}", std::process::id()));
        let mut p = Pending {
            version: "9.0.0".into(),
            installer: None,
            phase: "scheduled".into(),
            projects: vec![],
            remaining: vec![],
            error: String::new(),
            failures: vec![],
        };
        save(&d, &p).unwrap();
        p.phase = "restoring".into();
        save(&d, &p).unwrap();
        assert_eq!(load(&d).unwrap().unwrap().phase, "restoring");
        fs::remove_file(path(&d)).unwrap();
        fs::remove_dir(d).unwrap();
    }
}
