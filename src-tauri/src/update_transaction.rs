//! Durable update intent and project restoration. No arbitrary paths from IPC.
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::Write,
    net::{SocketAddr, TcpStream},
    path::{Path, PathBuf},
    time::{Duration, Instant},
};

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
    if p.phase != "scheduled" {
        return Ok(false);
    }
    let snapshot = plan(port)?;
    p.projects = snapshot.projects.clone();
    p.remaining = snapshot.projects.clone();
    save(directory, &p)?;
    if let Err((error, needs_restore)) = stop(port, &snapshot) {
        if needs_restore {
            p.phase = "restoring".into();
            p.error = error.clone();
            save(directory, &p)?;
            let _ = restore(directory, port);
        } else {
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
    let c = client()?;
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
    let c = client().map_err(|e| (e, false))?;
    let response = c
        .post(format!("http://127.0.0.1:{port}/api/desktop/update-stop"))
        .json(&serde_json::json!({"fingerprint":plan.fingerprint}))
        .send()
        .map_err(|e| (format!("未能确认停止结果，已取消安装：{e}"), true))?;
    let status = response.status();
    let response: serde_json::Value = response
        .json()
        .map_err(|_| ("后台响应无效，已取消安装".to_string(), true))?;
    if !status.is_success() {
        return Err((
            response["error"]
                .as_str()
                .unwrap_or("停止项目失败")
                .to_string(),
            response["restoreRequired"] == true,
        ));
    }
    if response["shuttingDown"] != true {
        return Err(("后台未确认退出，已取消安装".into(), true));
    }
    let deadline = Instant::now() + Duration::from_secs(30);
    while connected(port) {
        if Instant::now() > deadline {
            return Err(("后台尚未退出，已取消安装。请稍后重试".into(), true));
        }
        std::thread::sleep(Duration::from_millis(200))
    }
    Ok(())
}
pub fn restore(directory: &Path, port: u16) -> Result<(), String> {
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
        let result = (|| {
            request(
                port,
                "/api/desktop/update-restore",
                Some(&serde_json::to_value(&project).map_err(|e| e.to_string())?),
            )?;
            let until = Instant::now() + Duration::from_secs(60);
            loop {
                let app = request(port, &format!("/api/apps/{}", project.id), None)?;
                match app["status"].as_str() {
                    Some("running") => return Ok(()),
                    Some("failed" | "stopped") => {
                        return Err("启动失败，请查看项目记录后重试".to_string())
                    }
                    _ => {}
                }
                if Instant::now() > until {
                    return Err("已请求启动，但尚未确认就绪，请查看项目记录".into());
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
