//! Relaunch handshake for the installed shell. Backend shutdown and project
//! restoration, when required, are owned by update_transaction.
//! The new executable acknowledges readiness before the old shell exits, then
//! waits on its process handle before entering Tauri's single-instance plugin.
use std::path::{Path, PathBuf};

const FLAG: &str = "--rundock-relaunch";

pub fn installed_executable(current: &Path) -> Result<PathBuf, String> {
    let parent = current.parent().ok_or("无法确定安装目录")?;
    // Live installation may have renamed the running image to *.rundock-running-*.
    let target = parent.join("launcher-platform.exe");
    if !target.is_file() {
        return Err("未找到新版 RunDock，请重新安装后重试".into());
    }
    Ok(target)
}

#[cfg(windows)]
mod windows {
    use super::*;
    use std::{
        ffi::c_void,
        os::windows::process::CommandExt,
        process::{Child, Command, Stdio},
        time::{SystemTime, UNIX_EPOCH},
    };
    type Handle = *mut c_void;
    const SYNCHRONIZE: u32 = 0x00100000;
    const EVENT_MODIFY_STATE: u32 = 0x0002;
    const WAIT_OBJECT_0: u32 = 0;
    #[link(name = "kernel32")]
    extern "system" {
        fn CreateEventW(
            attributes: *const c_void,
            manual: i32,
            initial: i32,
            name: *const u16,
        ) -> Handle;
        fn OpenEventW(access: u32, inherit: i32, name: *const u16) -> Handle;
        fn OpenProcess(access: u32, inherit: i32, pid: u32) -> Handle;
        fn SetEvent(event: Handle) -> i32;
        fn WaitForSingleObject(handle: Handle, milliseconds: u32) -> u32;
        fn CloseHandle(handle: Handle) -> i32;
    }
    struct OwnedHandle(Handle);
    impl OwnedHandle {
        fn new(value: Handle) -> Result<Self, String> {
            if value.is_null() {
                Err(std::io::Error::last_os_error().to_string())
            } else {
                Ok(Self(value))
            }
        }
    }
    impl Drop for OwnedHandle {
        fn drop(&mut self) {
            unsafe {
                CloseHandle(self.0);
            }
        }
    }
    fn wide(value: &str) -> Vec<u16> {
        value.encode_utf16().chain(Some(0)).collect()
    }

    pub fn file_version(path: &Path) -> Result<String, String> {
        // Windows can return the loaded image's resource for its original path
        // after a live installer renames it and replaces that path. Inspect a
        // fresh, private snapshot so this always describes the file on disk.
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|e| e.to_string())?
            .as_nanos();
        let directory =
            std::env::temp_dir().join(format!("rundock-version-{}-{nonce}", std::process::id()));
        std::fs::create_dir(&directory).map_err(|e| format!("无法读取安装版本：{e}"))?;
        struct Snapshot(PathBuf);
        impl Drop for Snapshot {
            fn drop(&mut self) {
                let _ = std::fs::remove_file(self.0.join("installed.exe"));
                let _ = std::fs::remove_dir(&self.0);
            }
        }
        let snapshot = Snapshot(directory);
        let copy = snapshot.0.join("installed.exe");
        std::fs::copy(path, &copy).map_err(|e| format!("无法读取安装版本：{e}"))?;
        resource_version(&copy)
    }

    fn resource_version(path: &Path) -> Result<String, String> {
        use std::os::windows::ffi::OsStrExt;
        #[link(name = "version")]
        extern "system" {
            fn GetFileVersionInfoSizeW(file: *const u16, handle: *mut u32) -> u32;
            fn GetFileVersionInfoW(
                file: *const u16,
                handle: u32,
                size: u32,
                data: *mut c_void,
            ) -> i32;
            fn VerQueryValueW(
                data: *const c_void,
                block: *const u16,
                value: *mut *mut c_void,
                len: *mut u32,
            ) -> i32;
        }
        let name: Vec<u16> = path.as_os_str().encode_wide().chain(Some(0)).collect();
        let mut ignored = 0;
        let size = unsafe { GetFileVersionInfoSizeW(name.as_ptr(), &mut ignored) };
        if size == 0 || size > 1024 * 1024 {
            return Err("无法读取安装版本".into());
        }
        let mut data = vec![0u32; (size as usize + 3) / 4];
        if unsafe { GetFileVersionInfoW(name.as_ptr(), 0, size, data.as_mut_ptr().cast()) } == 0 {
            return Err("无法读取安装版本".into());
        }
        let mut value = std::ptr::null_mut();
        let mut len = 0;
        if unsafe {
            VerQueryValueW(
                data.as_ptr().cast(),
                wide("\\").as_ptr(),
                &mut value,
                &mut len,
            )
        } == 0
            || value.is_null()
            || len < 52
        {
            return Err("安装文件版本信息无效".into());
        }
        // VS_FIXEDFILEINFO: signature, structure version, file MS/LS, product MS/LS.
        let fields = unsafe { std::slice::from_raw_parts(value.cast::<u32>(), 13) };
        if fields[0] != 0xfeef04bd || fields[5] & 0xffff != 0 {
            return Err("安装文件版本格式无效".into());
        }
        Ok(format!(
            "{}.{}.{}",
            fields[4] >> 16,
            fields[4] & 0xffff,
            fields[5] >> 16
        ))
    }

    pub fn prepare(current: &Path, expected_version: &str) -> Result<Child, String> {
        let target = installed_executable(current)?;
        let nonce = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|e| e.to_string())?
            .as_nanos();
        let event_name = format!("Local\\RunDock-Restart-{}-{nonce}", std::process::id());
        let event = OwnedHandle::new(unsafe {
            CreateEventW(std::ptr::null(), 1, 0, wide(&event_name).as_ptr())
        })?;
        let mut child = Command::new(&target)
            .args([
                FLAG,
                &std::process::id().to_string(),
                &event_name,
                expected_version,
            ])
            .current_dir(target.parent().ok_or("安装目录无效")?)
            .creation_flags(0x08000000 | 0x00000200)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .map_err(|e| format!("无法启动新版，当前窗口和项目保持运行：{e}"))?;
        // Only the newly spawned helper can be terminated on failure. Never touch
        // the sidecar or a project, and never exit the old shell without an ack.
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(15);
        loop {
            if unsafe { WaitForSingleObject(event.0, 100) } == WAIT_OBJECT_0 {
                if matches!(child.try_wait(), Ok(None)) {
                    return Ok(child);
                }
                break;
            }
            if !matches!(child.try_wait(), Ok(None)) || std::time::Instant::now() >= deadline {
                break;
            }
        }
        let _ = child.kill();
        let _ = child.wait();
        Err("新版未能准备好重启，当前窗口和项目保持运行。请重新安装后重试。".into())
    }

    pub fn wait(args: &[String], current_version: &str) -> Result<bool, String> {
        if args.get(1).map(String::as_str) != Some(FLAG) {
            return Ok(false);
        }
        if args.len() != 5 || args[4] != current_version {
            return Err("安装版本与更新版本不一致".into());
        }
        let pid: u32 = args[2].parse().map_err(|_| "重启进程标识无效")?;
        if pid == std::process::id()
            || !args[3].starts_with(&format!("Local\\RunDock-Restart-{pid}-"))
        {
            return Err("重启请求无效".into());
        }
        // Capture the process handle before acknowledging: no PID-reuse race.
        let old = OwnedHandle::new(unsafe { OpenProcess(SYNCHRONIZE, 0, pid) })?;
        let event = OwnedHandle::new(unsafe {
            OpenEventW(EVENT_MODIFY_STATE, 0, wide(&args[3]).as_ptr())
        })?;
        if unsafe { SetEvent(event.0) } == 0 {
            return Err("无法确认重启准备状态".into());
        }
        if unsafe { WaitForSingleObject(old.0, 60000) } != WAIT_OBJECT_0 {
            return Err("旧窗口尚未退出，已取消重复启动".into());
        }
        Ok(true)
    }
}

#[cfg(windows)]
pub use windows::file_version;
#[cfg(windows)]
pub use windows::prepare;

pub fn wait_for_previous_instance(current_version: &str) -> Result<bool, String> {
    #[cfg(windows)]
    {
        windows::wait(&std::env::args().collect::<Vec<_>>(), current_version)
    }
    #[cfg(not(windows))]
    {
        let _ = current_version;
        Ok(false)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn missing_installation_does_not_fall_back_to_the_running_backup() {
        assert!(installed_executable(Path::new(
            "missing-install/launcher-platform.rundock-running-old.exe"
        ))
        .is_err());
    }
    #[cfg(windows)]
    #[test]
    fn ordinary_start_and_invalid_restart_are_distinct() {
        assert!(!windows::wait(&["app".into()], "1.0.0").unwrap());
        assert!(windows::wait(&["app".into(), FLAG.into()], "1.0.0").is_err());
        assert!(windows::wait(
            &[
                "app".into(),
                FLAG.into(),
                "1".into(),
                "bad-event".into(),
                "2.0.0".into()
            ],
            "1.0.0"
        )
        .is_err());
    }
}
