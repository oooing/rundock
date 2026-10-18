// Acceptance-only entry point for the actual installer worker. Not bundled.
// Only use with explicit local installation authorization and a saved baseline.
#![allow(dead_code)]
#[path = "../src/app_update.rs"]
mod app_update;
#[path = "../src/desktop_restart.rs"]
mod desktop_restart;
#[path = "../src/installer_process.rs"]
mod installer_process;
#[path = "../src/update_transaction.rs"]
mod update_transaction;
const SIDECAR_PORT: &str = "0";
fn sidecar_data_dir() -> Option<std::path::PathBuf> {
    None
}
fn spawn_sidecar(_: &std::path::Path) -> std::io::Result<std::process::Child> {
    Err(std::io::Error::other("probe cannot launch a backend"))
}
fn wait_for_sidecar(_: &std::path::Path, _: u64) -> Option<String> {
    None
}
fn main() {
    let args: Vec<_> = std::env::args().collect();
    assert_eq!(
        args.len(),
        6,
        "usage: probe <data-dir> <version> <installer-path> <sha256> <size>"
    );
    let directory = std::path::Path::new(&args[1]);
    if let Some(p) = update_transaction::load(directory).unwrap() {
        assert!(
            p.phase == "finished" && p.remaining.is_empty(),
            "existing update must be resolved first"
        );
    }
    let mut pending: update_transaction::Pending = serde_json::from_value(serde_json::json!({
        "version": args[2], "installer": {"path":args[3], "hash":args[4], "size":args[5].parse::<u64>().unwrap()},
        "phase":"staging", "projects":[], "remaining":[], "error":"", "failures":[]
    })).unwrap();
    match app_update::stage_files(directory, &mut pending) {
        Ok(()) => println!("{}", serde_json::to_string(&pending).unwrap()),
        Err(e) => {
            eprintln!("{e}");
            std::process::exit(1);
        }
    }
}
