// Isolated native shell fixture. Uses the same exit policy as the Tauri command.
#[path = "../../src-tauri/src/desktop_exit.rs"]
mod desktop_exit;
use std::io;
use std::process::{Command, Stdio};
use std::os::windows::process::CommandExt;
fn main() {
    let args: Vec<_> = std::env::args().collect();
    let port = &args[2];
    if args[1] == "stop" {
        desktop_exit::prepare_exit(port, false).unwrap();
        return;
    }
    let log = std::fs::File::create(&args[5]).unwrap();
    let mut command = Command::new(&args[3]);
    command.args(["-port", port]).env("LAUNCHER_DATA_DIR", &args[4])
        .creation_flags(0x08000000 | 0x00000200)
        .stdin(Stdio::null()).stdout(log.try_clone().unwrap()).stderr(log);
    let child = command.spawn().unwrap();
    println!("{}", child.id());
    let mut line = String::new();
    io::stdin().read_line(&mut line).unwrap();
    assert_eq!(line.trim(), "keep");
    desktop_exit::prepare_exit(port, true).unwrap();
    // Child is dropped with the shell, not killed. Test checks the project still serves.
}
