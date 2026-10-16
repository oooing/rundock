#[path = "../../src-tauri/src/desktop_restart.rs"]
mod desktop_restart;
use std::{
    io,
    os::windows::process::CommandExt,
    process::{Command, Stdio},
};
fn main() {
    match desktop_restart::wait_for_previous_instance("9.9.9") {
        Ok(true) => {
            std::fs::write(
                std::env::var("RESTART_FIXTURE_MARKER").unwrap(),
                format!(
                    "{}\n{}",
                    std::process::id(),
                    std::env::current_exe().unwrap().display()
                ),
            )
            .unwrap();
            std::thread::sleep(std::time::Duration::from_secs(60));
            return;
        }
        Err(error) => {
            eprintln!("{error}");
            std::process::exit(1);
        }
        _ => {}
    }
    let args: Vec<_> = std::env::args().collect();
    if args[1] == "reject-version" {
        assert!(
            desktop_restart::prepare(&std::env::current_exe().unwrap(), "wrong-version").is_err()
        );
        println!("old-shell-still-running");
        return;
    }
    let log = std::fs::File::create(&args[5]).unwrap();
    let child = Command::new(&args[3])
        .args(["-port", &args[2]])
        .env("LAUNCHER_DATA_DIR", &args[4])
        .creation_flags(0x08000000 | 0x00000200)
        .stdin(Stdio::null())
        .stdout(log.try_clone().unwrap())
        .stderr(log)
        .spawn()
        .unwrap();
    println!("{}", child.id());
    let mut line = String::new();
    io::stdin().read_line(&mut line).unwrap();
    assert_eq!(line.trim(), "restart");
    let _new_shell = desktop_restart::prepare(&std::env::current_exe().unwrap(), "9.9.9").unwrap();
    // Ready does not mean started: single-instance initialization must wait.
    assert!(!std::path::Path::new(&std::env::var("RESTART_FIXTURE_MARKER").unwrap()).exists());
    // Exit drops Child handles; it never kills backend/project processes.
}
