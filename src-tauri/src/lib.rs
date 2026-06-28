// Tauri Rust 壳：窗口 + 托盘 + spawn Go sidecar + 把端口注入前端。
//
// 启动流程：
//   1. spawn sidecar 二进制（externalBin，固定 LAUNCHER_PORT=17654）
//   2. 轮询 %APPDATA%\launcher-sidecar\sidecar.port 直到就绪（或超时）
//   3. 通过初始化脚本把 window.__LAUNCHER_BASE__ 注入 webview
//   4. 应用退出时 sidecar 随之结束（sidecar 收到 SIGINT 或 Job 句柄关闭即回收所有托管进程）

use std::path::PathBuf;
use std::time::{Duration, Instant};
use tauri::Manager;
use tauri_plugin_shell::process::CommandEvent;
use tauri_plugin_shell::ShellExt;

const SIDECAR_PORT: &str = "17654";

/// 解析 sidecar 数据目录（%APPDATA%\launcher-sidecar）。
/// 与 Go config.Default() 保持一致。
fn sidecar_data_dir() -> Option<PathBuf> {
    #[cfg(target_os = "windows")]
    {
        if let Some(appdata) = std::env::var_os("APPDATA") {
            return Some(PathBuf::from(appdata).join("launcher-sidecar"));
        }
    }
    std::env::var_os("HOME")
        .map(|h| PathBuf::from(h).join(".launcher-sidecar"))
}

/// 读取 sidecar 写出的端口发现文件。空表示尚未就绪。
fn read_port_file(data_dir: &PathBuf) -> Option<String> {
    let path = data_dir.join("sidecar.port");
    std::fs::read_to_string(path).ok().map(|s| s.trim().to_string())
}

/// 轮询等待 sidecar 端口文件出现，最多 wait_secs 秒。
fn wait_for_sidecar(data_dir: &PathBuf, wait_secs: u64) -> Option<String> {
    let deadline = Instant::now() + Duration::from_secs(wait_secs);
    while Instant::now() < deadline {
        if let Some(port) = read_port_file(data_dir) {
            return Some(port);
        }
        std::thread::sleep(Duration::from_millis(300));
    }
    read_port_file(data_dir)
}

#[tauri::command]
fn sidecar_base() -> Option<String> {
    // 前端通过此命令取 sidecar 基址（壳启动时已确定端口）。
    sidecar_data_dir()
        .and_then(|d| read_port_file(&d))
        .map(|port| format!("http://127.0.0.1:{}", port))
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            // 1. spawn sidecar
            let sidecar = app
                .shell()
                .sidecar("launcher-sidecar")
                .expect("sidecar binary not bundled");
            let (mut rx, _child) = sidecar
                .args(["-port", SIDECAR_PORT])
                .spawn()
                .expect("failed to spawn sidecar");

            // sidecar 输出转发到日志（便于排障）
            tauri::async_runtime::spawn(async move {
                while let Some(event) = rx.recv().await {
                    match event {
                        CommandEvent::Stdout(bytes) => {
                            log_sidecar("OUT", &bytes);
                        }
                        CommandEvent::Stderr(bytes) => {
                            log_sidecar("ERR", &bytes);
                        }
                        CommandEvent::Terminated(p) => {
                            eprintln!("[sidecar] terminated: code={:?}", p.code);
                        }
                        _ => {}
                    }
                }
            });

            // 2. 等端口就绪（最多 15s）
            let data_dir = sidecar_data_dir().unwrap_or_else(|| PathBuf::from("."));
            let port = wait_for_sidecar(&data_dir, 15).unwrap_or_else(|| SIDECAR_PORT.to_string());
            let base = format!("http://127.0.0.1:{}", port);
            println!("[shell] sidecar base = {}", base);

            // 3. 注入基址到前端：通过 eval 设置 window.__LAUNCHER_BASE__。
            // 前端的 connection store 也会探测 /api/health 作为兜底确认。
            let init_script = format!(
                "window.__LAUNCHER_BASE__ = {};",
                serde_json::to_string(&base).unwrap_or_default()
            );
            if let Some(win) = app.get_webview_window("main") {
                let _ = win.eval(&init_script);
            }

            Ok(())
        })
        .invoke_handler(tauri::generate_handler![sidecar_base])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

fn log_sidecar(tag: &str, bytes: &[u8]) {
    let s = String::from_utf8_lossy(bytes);
    for line in s.lines() {
        println!("[sidecar/{}] {}", tag, line);
    }
}
