// Acceptance-only CLI harness for the production transaction module.
// Not shipped in the application, and never reads the real user database.
mod app_update {
    #[derive(Clone, serde::Serialize, serde::Deserialize)]
    pub struct Ready {}
}
#[path = "../src/update_transaction.rs"]
mod update_transaction;
use update_transaction::*;
fn main() {
    let args: Vec<String> = std::env::args().collect();
    let port = args[2].parse().unwrap();
    let dir = std::path::Path::new(&args[3]);
    match args[1].as_str() {
        "schedule" => {
            let p = Pending {
                version: "2.0.42".into(),
                installer: None,
                phase: "scheduled".into(),
                projects: vec![],
                remaining: vec![],
                error: String::new(),
                failures: vec![],
            };
            save(dir, &p).unwrap();
        }
        "can-start" => {
            println!(
                "{}",
                can_install_on_start(&load(dir).unwrap().unwrap(), port)
            );
        }
        "deferred-exit" => {
            assert!(prepare_scheduled_exit(dir, port).unwrap());
        }
        "stop" => {
            let snapshot = plan(port).unwrap();
            println!("{}", serde_json::to_string(&snapshot).unwrap());
            let mut p = Pending {
                version: "2.0.42".into(),
                installer: None,
                phase: "restoring".into(),
                projects: snapshot.projects.clone(),
                remaining: snapshot.projects,
                error: String::new(),
                failures: vec![],
            };
            save(dir, &p).unwrap();
            let snapshot:Plan=serde_json::from_value(serde_json::json!({"instanceId":snapshot.instance_id,"fingerprint":snapshot.fingerprint,"projects":p.projects})).unwrap();
            if let Err((error, needs_restore)) = stop(port, &snapshot) {
                p.error = error;
                if !needs_restore {
                    p.phase = "finished".into();
                    p.remaining.clear();
                }
                save(dir, &p).unwrap();
                if needs_restore {
                    restore(dir, port).unwrap();
                }
                std::process::exit(2)
            }
        }
        "restore" => {
            restore(dir, port).unwrap();
            println!("{}", serde_json::to_string(&load(dir).unwrap()).unwrap());
        }
        _ => panic!("unknown command"),
    }
}
