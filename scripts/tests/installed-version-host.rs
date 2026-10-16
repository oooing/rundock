#[path = "../../src-tauri/src/desktop_restart.rs"]
mod desktop_restart;
fn main() {
    let executable = std::env::current_exe().unwrap();
    println!("{}", desktop_restart::file_version(&executable).unwrap());
    let mut input = String::new();
    std::io::stdin().read_line(&mut input).unwrap();
    println!("{}", desktop_restart::file_version(&executable).unwrap());
}
