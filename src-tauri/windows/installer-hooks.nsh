; Product name changed in 2.0.3. Do not silently create a second NSIS installation.
; Data remains under the unchanged com.launcher.platform / launcher-sidecar paths.
!define RUNDOCK_STOP_SCRIPT "${__FILEDIR__}\stop-installed-app.ps1"

!macro RunDockStopInstalledApp
  Push $R0
  Push $R1
  InitPluginsDir
  File /oname=$PLUGINSDIR\rundock-stop-installed-app.ps1 "${RUNDOCK_STOP_SCRIPT}"
  DetailPrint "Closing RunDock and its background service..."
  nsExec::ExecToStack /TIMEOUT=120000 '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -WindowStyle Hidden -ExecutionPolicy Bypass -File "$PLUGINSDIR\rundock-stop-installed-app.ps1" -InstallDirectory "$INSTDIR\."'
  Pop $R0
  Pop $R1
  DetailPrint "$R1"
  ${If} $R0 != 0
    SetDetailsView show
    MessageBox MB_OK|MB_ICONSTOP "RunDock could not release its installation files. Close RunDock and retry setup. If access is denied, run setup as administrator. Your project data has not been removed.$\r$\n$\r$\n$R1" /SD IDOK
    SetErrorLevel 1
    Abort
  ${EndIf}
  Pop $R1
  Pop $R0
!macroend

!macro NSIS_HOOK_PREINSTALL
  ReadRegStr $R0 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Launcher" "UninstallString"
  ReadRegStr $R1 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Launcher" "UninstallString"
  ${If} "$R0$R1" != ""
    MessageBox MB_OK|MB_ICONEXCLAMATION "Launcher is now RunDock. Please uninstall the old Launcher first, keeping its application data, then run this installer again." /SD IDOK
    Abort
  ${EndIf}
  !insertmacro RunDockStopInstalledApp
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  !insertmacro RunDockStopInstalledApp
!macroend
