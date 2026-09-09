#[tauri::command]
pub async fn select_project_path(kind: String) -> Result<Option<String>, String> {
    if kind != "folder" && kind != "script" {
        return Err("Invalid picker kind".into());
    }
    tauri::async_runtime::spawn_blocking(move || choose(&kind))
        .await
        .map_err(|e| e.to_string())?
}

#[cfg(target_os = "windows")]
fn choose(kind: &str) -> Result<Option<String>, String> {
    use std::os::windows::process::CommandExt;
    use std::process::{Command, Stdio};
    // Fixed code only: no project path, command, or filename is interpolated.
    let dialog = if kind == "folder" {
        "$d=New-Object System.Windows.Forms.FolderBrowserDialog; $d.Description='Select project folder'; $d.ShowNewFolderButton=$false; $property='SelectedPath';"
    } else {
        "$d=New-Object System.Windows.Forms.OpenFileDialog; $d.Title='Select startup script'; $d.Filter='Startup scripts (*.bat;*.cmd;*.ps1)|*.bat;*.cmd;*.ps1'; $d.CheckFileExists=$true; $d.Multiselect=$false; $property='FileName';"
    };
    let script = format!(
        "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new($false); Add-Type -AssemblyName System.Windows.Forms; {dialog} $owner=New-Object System.Windows.Forms.Form; $owner.TopMost=$true; try {{ if($d.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) {{ [Console]::Write($d.$property) }} }} finally {{ $d.Dispose(); $owner.Dispose() }}"
    );
    let system_root = std::env::var_os("SystemRoot").ok_or("SystemRoot unavailable")?;
    let executable = std::path::PathBuf::from(system_root).join("System32/WindowsPowerShell/v1.0/powershell.exe");
    let output = Command::new(executable)
        .args(["-NoLogo", "-NoProfile", "-STA", "-Command", &script])
        .stdin(Stdio::null())
        .creation_flags(0x08000000)
        .output()
        .map_err(|e| e.to_string())?;
    if !output.status.success() {
        return Err("Could not open the path picker".into());
    }
    let path = String::from_utf8(output.stdout).map_err(|e| e.to_string())?;
    if path.is_empty() { Ok(None) } else { Ok(Some(path)) }
}

#[cfg(not(target_os = "windows"))]
fn choose(_kind: &str) -> Result<Option<String>, String> {
    Err("Paste the absolute path on this platform".into())
}
