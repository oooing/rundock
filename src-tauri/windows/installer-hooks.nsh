; Product name changed in 2.0.3. Do not silently create a second NSIS installation.
; Data remains under the unchanged com.launcher.platform / launcher-sidecar paths.
!define RUNDOCK_STOP_SCRIPT "${__FILEDIR__}\stop-installed-app.ps1"
!ifndef RUNDOCK_INSTALL_HELPER
  !define RUNDOCK_INSTALL_HELPER "${__FILEDIR__}\..\binaries\rundock-install-helper-x86_64-pc-windows-msvc.exe"
!endif
Var RunDockInstallOwner

; Match the app's first-launch policy: Windows display language, Chinese primary
; language -> Simplified Chinese, otherwise English. NSIS handles that lookup;
; tauri.conf.json must list English first and include SimpChinese.
; Numeric LANGIDs allow these strings to be declared before MUI_LANGUAGE.
LangString RunDockClosing 1033 "Closing RunDock and its background service..."
LangString RunDockClosing 2052 "正在关闭 RunDock 及其后台服务…"
LangString RunDockCloseFailed 1033 "RunDock could not release its installation files. Close RunDock and retry setup. If access is denied, run setup as administrator. Your project data has not been removed."
LangString RunDockCloseFailed 2052 "无法释放 RunDock 安装文件。请关闭 RunDock 后重试；如果提示拒绝访问，请以管理员身份运行安装程序。项目数据未被删除。"
LangString RunDockInstallFailed 1033 "Installation could not finish. RunDock and your projects keep running. Check file permissions or run setup as administrator."
LangString RunDockInstallFailed 2052 "安装未完成，RunDock 和项目继续运行。请检查文件权限，或以管理员身份重试。"
LangString RunDockLegacyMigration 1033 "Launcher is now RunDock. Please uninstall the old Launcher first, keeping its application data, then run this installer again."
LangString RunDockLegacyMigration 2052 "Launcher 已更名为 RunDock。请先卸载旧版 Launcher，保留应用数据，然后重新运行此安装程序。"

!macro RunDockStopInstalledApp
  Push $R0
  Push $R1
  InitPluginsDir
  File /oname=$PLUGINSDIR\rundock-stop-installed-app.ps1 "${RUNDOCK_STOP_SCRIPT}"
  DetailPrint "$(RunDockClosing)"
  nsExec::ExecToStack /TIMEOUT=120000 '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "$PLUGINSDIR\rundock-stop-installed-app.ps1" -InstallDirectory "$INSTDIR\."'
  Pop $R0
  Pop $R1
  DetailPrint "$R1"
  ${If} $R0 != 0
    SetDetailsView show
    MessageBox MB_OK|MB_ICONSTOP "$(RunDockCloseFailed)$\r$\n$\r$\n$R1" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
  Pop $R1
  Pop $R0
!macroend

!macro RunDockLiveInstall OPERATION
  Push $R0
  Push $R1
  InitPluginsDir
  File /oname=$PLUGINSDIR\rundock-install-helper.exe "${RUNDOCK_INSTALL_HELPER}"
  System::Call 'kernel32::GetCurrentProcessId() i .r0'
  StrCpy $RunDockInstallOwner $0
  nsExec::ExecToStack /TIMEOUT=30000 '"$PLUGINSDIR\rundock-install-helper.exe" ${OPERATION} "$INSTDIR\." "$RunDockInstallOwner"'
  Pop $R0
  Pop $R1
  ${If} $R0 != 0
    DetailPrint "$R1"
    MessageBox MB_OK|MB_ICONSTOP "$(RunDockInstallFailed)$\r$\n$\r$\n$R1" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
  Pop $R1
  Pop $R0
!macroend

Function .onInstFailed
  ; Roll back file staging only. Never stop the running application or projects.
  nsExec::ExecToLog '"$PLUGINSDIR\rundock-install-helper.exe" rollback "$INSTDIR\." "$RunDockInstallOwner"'
FunctionEnd

!macro NSIS_HOOK_PREINSTALL
  ReadRegStr $R0 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Launcher" "UninstallString"
  ReadRegStr $R1 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Launcher" "UninstallString"
  ${If} "$R0$R1" != ""
    MessageBox MB_OK|MB_ICONEXCLAMATION "$(RunDockLegacyMigration)" /SD IDOK
    Abort
  ${EndIf}
  !insertmacro RunDockLiveInstall prepare
!macroend

!macro NSIS_HOOK_POSTINSTALL
  !insertmacro RunDockLiveInstall commit
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  !insertmacro RunDockStopInstalledApp
!macroend
