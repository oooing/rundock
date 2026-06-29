// Tauri Rust 壳：窗口 + 托盘 + spawn Go sidecar + 把端口注入前端。
//
// 启动流程：
//   1. spawn sidecar 二进制（externalBin，固定 LAUNCHER_PORT=17654）
//   2. 轮询 %APPDATA%\launcher-sidecar\sidecar.port 直到就绪（或超时）
//   3. 通过初始化脚本把 window.__LAUNCHER_BASE__ 注入 webview
//   4. 应用退出时 sidecar 随之结束（sidecar 收到 SIGINT 或 Job 句柄关闭即回收所有托管进程）
//
// 窗口行为：
//   - 点关闭(X) 不真退出，而是 emit "close-requested" 给前端弹窗选择（最小化到托盘 / 退出）。
//   - 托盘图标：双击显示窗口；右键菜单「显示窗口」「退出」。
//   - 退出(quit_app)前调 sidecar stop-all 停止所有项目，再 exit。

use std::io::{Read, Write as IoWrite};
use std::net::TcpStream;
use std::path::PathBuf;
use std::time::{Duration, Instant};
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Emitter, Manager, WindowEvent};
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

/// 向本地 sidecar 发送 POST /api/apps/stop-all（loopback，裸 TCP 手写 HTTP）。
/// 退出前优雅停止所有项目。失败不阻塞退出（Job 兜底回收）。
fn sidecar_stop_all(data_dir: &PathBuf) {
    let port = match read_port_file(data_dir) {
        Some(p) => p,
        None => return,
    };
    let addr = format!("127.0.0.1:{}", port);
    let req = "POST /api/apps/stop-all HTTP/1.0\r\nHost: localhost\r\nContent-Length: 0\r\nConnection: close\r\n\r\n";
    // 停止项目可能耗时（grace period），给充裕超时
    let Ok(mut stream) = TcpStream::connect_timeout(
        &addr.parse().unwrap_or_else(|_| "127.0.0.1:0".parse().unwrap()),
        Duration::from_secs(3),
    ) else {
        return;
    };
    let _ = stream.set_read_timeout(Some(Duration::from_secs(30)));
    let _ = stream.set_write_timeout(Some(Duration::from_secs(5)));
    let _ = stream.write_all(req.as_bytes());
    // 读取会阻塞直到 sidecar 处理完 stop-all 并关闭连接（Connection: close）。
    // 只需等它结束，不解析响应内容。
    let mut sink = [0u8; 256];
    while let Ok(n) = stream.read(&mut sink) {
        if n == 0 {
            break;
        }
    }
    let _ = stream.shutdown(std::net::Shutdown::Both);
}

/// 退出应用：先停止所有项目服务，再退出。供前端「退出」调用。
#[tauri::command]
fn quit_app(app: tauri::AppHandle) {
    let data_dir = sidecar_data_dir().unwrap_or_else(|| PathBuf::from("."));
    // 1) 优雅停止所有项目
    sidecar_stop_all(&data_dir);
    // 2) 退出（Job Object 关闭会级联回收 sidecar 及其子进程作为兜底）
    app.exit(0);
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

            // 4. 系统托盘：双击显示窗口；右键菜单「显示窗口」「退出」。
            let show_item = MenuItem::with_id(app, "show", "显示窗口", true, None::<&str>)?;
            let quit_item = MenuItem::with_id(app, "quit", "退出", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show_item, &quit_item])?;
            let _tray = TrayIconBuilder::new()
                .icon(app.default_window_icon().cloned().expect("no window icon"))
                .tooltip("启动平台")
                .menu(&menu)
                .show_menu_on_left_click(false)
                .on_tray_icon_event(|tray, event| {
                    // 双击托盘图标：显示并聚焦主窗口
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        let app = tray.app_handle();
                        if let Some(win) = app.get_webview_window("main") {
                            let _ = win.show();
                            let _ = win.set_focus();
                        }
                    }
                })
                .on_menu_event(|app, event| match event.id().as_ref() {
                    "show" => {
                        if let Some(win) = app.get_webview_window("main") {
                            let _ = win.show();
                            let _ = win.set_focus();
                        }
                    }
                    "quit" => {
                        // 托盘退出：通知前端弹二次确认（前端确认后调 quit_app）
                        let _ = app.emit("tray-quit-requested", ());
                    }
                    _ => {}
                })
                .build(app)?;

            Ok(())
        })
        // 拦截窗口关闭：不直接退出，交由前端弹窗决定（最小化到托盘 / 退出）。
        .on_window_event(|window, event| {
            if let WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.emit("close-requested", ());
            }
        })
        .invoke_handler(tauri::generate_handler![sidecar_base, quit_app])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

fn log_sidecar(tag: &str, bytes: &[u8]) {
    let s = String::from_utf8_lossy(bytes);
    for line in s.lines() {
        println!("[sidecar/{}] {}", tag, line);
    }
}
