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

use std::fs::OpenOptions;
use std::io::{Read, Write as IoWrite};
use std::net::TcpStream;
use std::path::PathBuf;
use std::process::{Child, Command, Stdio};
use std::sync::Mutex;
use std::time::{Duration, Instant};
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Emitter, Manager, WindowEvent};

mod window_layout;

#[cfg(not(debug_assertions))]
const SIDECAR_PORT: &str = "17654";
#[cfg(debug_assertions)]
const SIDECAR_PORT: &str = "17655";

struct SidecarState(Mutex<Option<Child>>);

struct UiMenu {
    show: MenuItem<tauri::Wry>,
    quit: MenuItem<tauri::Wry>,
}

/// Only changes presentation; project configuration and running processes are untouched.
#[tauri::command]
fn set_ui_language(app: tauri::AppHandle, locale: String) -> Result<(), String> {
    let english = locale == "en";
    let title = if english { "RunDock" } else { "RunDock 启动坞" };
    let menu = app.state::<UiMenu>();
    menu.show.set_text(if english { "Show window" } else { "显示窗口" }).map_err(|e| e.to_string())?;
    menu.quit.set_text(if english { "Quit" } else { "退出" }).map_err(|e| e.to_string())?;
    if let Some(window) = app.get_webview_window("main") {
        window.set_title(title).map_err(|e| e.to_string())?;
    }
    if let Some(tray) = app.tray_by_id("main-tray") {
        tray.set_tooltip(Some(title)).map_err(|e| e.to_string())?;
    }
    Ok(())
}

/// 解析 sidecar 数据目录（%APPDATA%\launcher-sidecar）。
/// 与 Go config.Default() 保持一致。
fn sidecar_data_dir() -> Option<PathBuf> {
    let name = if cfg!(debug_assertions) { "launcher-sidecar-dev" } else { "launcher-sidecar" };
    #[cfg(target_os = "windows")]
    {
        if let Some(appdata) = std::env::var_os("APPDATA") {
            return Some(PathBuf::from(appdata).join(name));
        }
    }
    std::env::var_os("HOME").map(|h| PathBuf::from(h).join(format!(".{}", name)))
}

/// 读取 sidecar 写出的端口发现文件。空表示尚未就绪。
fn read_port_file(data_dir: &PathBuf) -> Option<String> {
    let path = data_dir.join("sidecar.port");
    std::fs::read_to_string(path)
        .ok()
        .map(|s| s.trim().to_string())
}

/// 轮询等待 sidecar 端口文件出现，最多 wait_secs 秒。
fn wait_for_sidecar(data_dir: &PathBuf, wait_secs: u64) -> Option<String> {
    let deadline = Instant::now() + Duration::from_secs(wait_secs);
    while Instant::now() < deadline {
        if let Some(port) = read_port_file(data_dir) {
            if sidecar_health_ok(&port) {
                return Some(port);
            }
        }
        std::thread::sleep(Duration::from_millis(300));
    }
    read_port_file(data_dir).filter(|port| sidecar_health_ok(port))
}

fn sidecar_health_ok(port: &str) -> bool {
    let addr = format!("127.0.0.1:{}", port);
    let Ok(mut stream) = TcpStream::connect_timeout(
        &addr
            .parse()
            .unwrap_or_else(|_| "127.0.0.1:0".parse().unwrap()),
        Duration::from_millis(500),
    ) else {
        return false;
    };
    let _ = stream.set_read_timeout(Some(Duration::from_secs(1)));
    let _ = stream.set_write_timeout(Some(Duration::from_secs(1)));
    let req = "GET /api/health HTTP/1.0\r\nHost: localhost\r\nConnection: close\r\n\r\n";
    if stream.write_all(req.as_bytes()).is_err() {
        return false;
    }
    let mut response = Vec::with_capacity(512);
    if stream.take(4096).read_to_end(&mut response).is_err() {
        return false;
    }
    let response = String::from_utf8_lossy(&response);
    response.contains(" 200 ") && response.contains("release-v2")
}

fn sidecar_exe_path() -> Option<PathBuf> {
    let exe_dir = std::env::current_exe().ok()?.parent()?.to_path_buf();
    let candidates = [
        exe_dir.join("launcher-sidecar.exe"),
        exe_dir.join("launcher-sidecar-x86_64-pc-windows-msvc.exe"),
        exe_dir
            .join("binaries")
            .join("launcher-sidecar-x86_64-pc-windows-msvc.exe"),
    ];
    candidates.into_iter().find(|p| p.exists())
}

fn append_shell_log(data_dir: &PathBuf, msg: &str) {
    let _ = std::fs::create_dir_all(data_dir);
    if let Ok(mut f) = OpenOptions::new()
        .create(true)
        .append(true)
        .open(data_dir.join("shell-sidecar.log"))
    {
        let _ = writeln!(f, "{}", msg);
    }
}

fn spawn_sidecar(data_dir: &PathBuf) -> std::io::Result<Child> {
    let exe = sidecar_exe_path().ok_or_else(|| {
        std::io::Error::new(
            std::io::ErrorKind::NotFound,
            "launcher-sidecar.exe not found",
        )
    })?;
    append_shell_log(
        data_dir,
        &format!("[shell] spawn sidecar: {}", exe.display()),
    );
    let exe_dir = exe.parent().map(|p| p.to_path_buf());
    let log_path = data_dir.join("shell-sidecar.log");
    let stdout = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)?;
    let stderr = OpenOptions::new()
        .create(true)
        .append(true)
        .open(&log_path)?;
    let mut cmd = Command::new(exe);
    cmd.args(["-port", SIDECAR_PORT])
        .env("LAUNCHER_DATA_DIR", data_dir)
        .stdin(Stdio::null())
        .stdout(Stdio::from(stdout))
        .stderr(Stdio::from(stderr));
    if let Some(dir) = exe_dir {
        cmd.current_dir(dir);
    }
    #[cfg(target_os = "windows")]
    {
        use std::os::windows::process::CommandExt;
        const CREATE_NO_WINDOW: u32 = 0x08000000;
        const CREATE_NEW_PROCESS_GROUP: u32 = 0x00000200;
        cmd.creation_flags(CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP);
    }
    cmd.spawn()
}

#[tauri::command]
fn sidecar_base() -> Option<String> {
    // 前端通过此命令取 sidecar 基址（壳启动时已确定端口）。
    sidecar_data_dir()
        .and_then(|d| read_port_file(&d))
        .filter(|port| sidecar_health_ok(port))
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
        &addr
            .parse()
            .unwrap_or_else(|_| "127.0.0.1:0".parse().unwrap()),
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
    // 停服务可能需要几秒，放到后台线程，避免前端窗口看起来卡死。
    std::thread::spawn(move || {
        let data_dir = sidecar_data_dir().unwrap_or_else(|| PathBuf::from("."));
        // 1) 优雅停止所有项目
        sidecar_stop_all(&data_dir);
        if let Ok(mut child) = app.state::<SidecarState>().0.lock() {
            if let Some(mut child) = child.take() {
                let _ = child.kill();
                let _ = child.wait();
            }
        }
        // 2) 退出（Job Object 关闭会级联回收 sidecar 及其子进程作为兜底）
        app.exit(0);
    });
}

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    let mut builder = tauri::Builder::default();

    // 必须最先注册：第二次启动时立即终止新实例，并恢复、显示和聚焦已有窗口。
    #[cfg(desktop)]
    {
        builder = builder.plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            if let Some(win) = app.get_webview_window("main") {
                let _ = win.unminimize();
                let _ = win.show();
                let _ = win.set_focus();
            }
        }));
    }

    builder
		.setup(|app| {
            // Prefer a roomy three-column window; on small/high-DPI displays use
            // the available work area rather than opening beyond the screen.
            if let (Some(window), Some(config)) = (
                app.get_webview_window("main"),
                app.config().app.windows.first(),
            ) {
                if let Ok(Some(monitor)) = window.current_monitor() {
                    let area = monitor.work_area();
                    if window_layout::needs_maximized(
                        config.width, config.height, monitor.scale_factor(),
                        area.size.width, area.size.height,
                    ) {
                        let _ = window.maximize();
                    } else {
                        let _ = window.center();
                    }
                }
            }
            let data_dir = sidecar_data_dir().unwrap_or_else(|| PathBuf::from("."));
            let child = if sidecar_health_ok(SIDECAR_PORT) {
                println!("[shell] reuse existing sidecar on {}", SIDECAR_PORT);
                None
            } else {
                let _ = std::fs::remove_file(data_dir.join("sidecar.port"));
                let mut child = spawn_sidecar(&data_dir).expect("failed to spawn launcher-sidecar.exe");
                std::thread::sleep(Duration::from_millis(300));
                if let Ok(Some(status)) = child.try_wait() {
                    append_shell_log(
                        &data_dir,
                        &format!("[shell] sidecar exited immediately: {:?}", status.code()),
                    );
                }
                Some(child)
            };
            app.manage(SidecarState(Mutex::new(child)));

            // 2. 等端口就绪（最多 15s）
            let port = wait_for_sidecar(&data_dir, 15)
                .or_else(|| sidecar_health_ok(SIDECAR_PORT).then(|| SIDECAR_PORT.to_string()))
                .unwrap_or_else(|| SIDECAR_PORT.to_string());
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
            app.manage(UiMenu { show: show_item, quit: quit_item });
            let _tray = TrayIconBuilder::with_id("main-tray")
                .icon(app.default_window_icon().cloned().expect("no window icon"))
                .tooltip("RunDock 启动坞")
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
                        // 托盘退出：先显示主窗口，否则确认弹窗会渲染在隐藏窗口里。
                        if let Some(win) = app.get_webview_window("main") {
                            let _ = win.unminimize();
                            let _ = win.show();
                            let _ = win.set_focus();
                            // 1) 直接发给 main 窗口。
                            let _ = win.emit("tray-quit-requested", ());
                            // 2) 再注入一个普通 DOM 事件作为兜底，不依赖 Tauri JS 事件监听是否注册成功。
                            let _ = win.eval(
                                "window.dispatchEvent(new CustomEvent('launcher-tray-quit-requested'));",
                            );
                        } else {
                            // 找不到窗口时没有地方弹确认，退化为直接退出。
                            app.exit(0);
                        }
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
        .invoke_handler(tauri::generate_handler![sidecar_base, quit_app, set_ui_language])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
