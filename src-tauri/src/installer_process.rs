//! Identify the installer across shell restarts without confusing a reused PID.
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::Write,
    path::Path,
    process::{Child, ExitStatus},
    time::{Duration, Instant},
};

#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Identity {
    pub pid: u32,
    pub created: u64,
}

/// Never publish a partial identity, or replace a valid record on write failure.
pub fn save(path: &Path, identity: &Identity) -> Result<(), String> {
    let temporary = path.with_extension("json.tmp");
    let result = (|| {
        let mut file = fs::File::create(&temporary).map_err(|e| e.to_string())?;
        file.write_all(&serde_json::to_vec(identity).map_err(|e| e.to_string())?)
            .map_err(|e| e.to_string())?;
        file.sync_all().map_err(|e| e.to_string())?;
        drop(file);
        #[cfg(windows)]
        {
            use std::os::windows::ffi::OsStrExt;
            #[link(name = "kernel32")]
            extern "system" {
                fn MoveFileExW(from: *const u16, to: *const u16, flags: u32) -> i32;
            }
            let from: Vec<u16> = temporary.as_os_str().encode_wide().chain(Some(0)).collect();
            let to: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
            if unsafe { MoveFileExW(from.as_ptr(), to.as_ptr(), 1 | 8) } == 0 {
                return Err(std::io::Error::last_os_error().to_string());
            }
        }
        #[cfg(not(windows))]
        fs::rename(&temporary, path).map_err(|e| e.to_string())?;
        Ok(())
    })();
    if result.is_err() {
        let _ = fs::remove_file(&temporary);
    }
    result
}

pub fn load(path: &Path) -> Result<Identity, String> {
    let bytes = fs::read(path).map_err(|e| format!("无法读取安装进程身份：{e}"))?;
    let identity: Identity =
        serde_json::from_slice(&bytes).map_err(|_| "安装进程身份记录损坏".to_string())?;
    if identity.pid == 0 || identity.created == 0 {
        return Err("安装进程身份记录无效".into());
    }
    Ok(identity)
}

/// Timeout and query errors leave the installer running; callers retain staging.
pub fn wait_for_exit(child: &mut Child, timeout: Duration) -> Result<Option<ExitStatus>, String> {
    let deadline = Instant::now() + timeout;
    loop {
        if let Some(status) = child.try_wait().map_err(|e| e.to_string())? {
            return Ok(Some(status));
        }
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Ok(None);
        }
        std::thread::sleep(remaining.min(Duration::from_millis(200)));
    }
}

#[cfg(windows)]
mod windows {
    use super::*;
    use std::{ffi::c_void, os::windows::io::AsRawHandle};
    type Handle = *mut c_void;
    #[link(name = "kernel32")]
    extern "system" {
        fn OpenProcess(access: u32, inherit: i32, pid: u32) -> Handle;
        fn GetProcessTimes(
            h: Handle,
            created: *mut u64,
            exit: *mut u64,
            kernel: *mut u64,
            user: *mut u64,
        ) -> i32;
        fn WaitForSingleObject(h: Handle, timeout: u32) -> u32;
        fn CloseHandle(h: Handle) -> i32;
    }
    fn created(handle: Handle) -> Result<u64, String> {
        let (mut c, mut e, mut k, mut u) = (0, 0, 0, 0);
        if unsafe { GetProcessTimes(handle, &mut c, &mut e, &mut k, &mut u) } == 0 {
            return Err(std::io::Error::last_os_error().to_string());
        }
        Ok(c)
    }
    pub fn identify(child: &Child) -> Result<Identity, String> {
        Ok(Identity {
            pid: child.id(),
            created: created(child.as_raw_handle())?,
        })
    }
    pub fn alive(identity: &Identity) -> Result<bool, String> {
        let handle = unsafe { OpenProcess(0x1000 | 0x100000, 0, identity.pid) };
        if handle.is_null() {
            let error = std::io::Error::last_os_error();
            return if error.raw_os_error() == Some(87) {
                Ok(false)
            } else {
                Err(error.to_string())
            };
        }
        let result = (|| {
            if created(handle)? != identity.created {
                return Ok(false);
            }
            match unsafe { WaitForSingleObject(handle, 0) } {
                0 => Ok(false),
                258 => Ok(true),
                _ => Err("无法确认安装进程状态，请稍后重试".into()),
            }
        })();
        unsafe {
            CloseHandle(handle);
        }
        result
    }
}
#[cfg(windows)]
pub use windows::{alive, identify};
#[cfg(not(windows))]
pub fn identify(_: &Child) -> Result<Identity, String> {
    Err("仅支持 Windows".into())
}
#[cfg(not(windows))]
pub fn alive(_: &Identity) -> Result<bool, String> {
    Err("仅支持 Windows".into())
}

#[cfg(all(test, windows))]
pub(crate) mod tests {
    use super::*;

    pub(crate) fn fixture_dir(label: &str) -> std::path::PathBuf {
        static NEXT: std::sync::atomic::AtomicUsize = std::sync::atomic::AtomicUsize::new(0);
        let directory = std::env::temp_dir().join(format!(
            "rundock-installer-{label}-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed)
        ));
        fs::create_dir_all(&directory).unwrap();
        directory
    }

    pub(crate) struct Fixture(pub Child);
    impl Drop for Fixture {
        fn drop(&mut self) {
            // Only the test subprocess created below, never a real installer.
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
    pub(crate) fn fixture() -> Fixture {
        Fixture(
            std::process::Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "installer_process::tests::installer_fixture",
                    "--ignored",
                ])
                .env("RUNDOCK_INSTALLER_WAIT_FIXTURE", "1")
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .spawn()
                .unwrap(),
        )
    }
    #[test]
    #[ignore = "Subprocess fixture; only run through fixture()"]
    fn installer_fixture() {
        assert_eq!(
            std::env::var("RUNDOCK_INSTALLER_WAIT_FIXTURE").unwrap(),
            "1"
        );
        std::thread::sleep(Duration::from_millis(1500));
    }

    #[test]
    fn identity_replace_is_atomic_and_preserves_old_record_on_failure() {
        use std::os::windows::fs::OpenOptionsExt;
        let path = fixture_dir("atomic").join("identity.json");
        let first = Identity {
            pid: 10,
            created: 20,
        };
        let second = Identity {
            pid: 30,
            created: 40,
        };
        save(&path, &first).unwrap();
        // Readers that deny delete make the atomic replacement fail on Windows.
        let reader = fs::OpenOptions::new()
            .read(true)
            .share_mode(1 | 2)
            .open(&path)
            .unwrap();
        assert!(save(&path, &second).is_err());
        assert_eq!(load(&path).unwrap(), first);
        assert!(!path.with_extension("json.tmp").exists());
        drop(reader);
        save(&path, &second).unwrap();
        assert_eq!(load(&path).unwrap(), second);
    }

    #[test]
    fn identity_load_rejects_missing_corrupt_and_zero_identity() {
        let path = fixture_dir("invalid").join("identity.json");
        assert!(load(&path).is_err());
        fs::write(&path, b"{\"pid\":").unwrap();
        assert!(load(&path).is_err());
        fs::write(&path, br#"{"pid":0,"created":0}"#).unwrap();
        assert!(load(&path).is_err());
    }

    #[test]
    fn wait_timeout_does_not_kill_and_later_observes_exit() {
        let mut child = fixture();
        let identity = identify(&child.0).unwrap();
        let start = Instant::now();
        assert!(wait_for_exit(&mut child.0, Duration::from_millis(40))
            .unwrap()
            .is_none());
        assert!(start.elapsed() < Duration::from_secs(1));
        assert!(alive(&identity).unwrap());
        assert!(wait_for_exit(&mut child.0, Duration::from_secs(6))
            .unwrap()
            .unwrap()
            .success());
        assert!(!alive(&identity).unwrap());
    }

    #[test]
    fn installer_identity_rejects_reused_pid_and_detects_exit() {
        let mut child = std::process::Command::new("cmd.exe")
            .args(["/c", "ping -n 6 127.0.0.1 >nul"])
            .spawn()
            .unwrap();
        let id = identify(&child).unwrap();
        assert!(alive(&id).unwrap());
        assert!(!alive(&Identity {
            pid: id.pid,
            created: id.created + 1
        })
        .unwrap());
        child.kill().unwrap();
        child.wait().unwrap();
        assert!(!alive(&id).unwrap());
    }
}
