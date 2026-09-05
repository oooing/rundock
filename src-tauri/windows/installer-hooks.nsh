; Product name changed in 2.0.3. Do not silently create a second NSIS installation.
; Data remains under the unchanged com.launcher.platform / launcher-sidecar paths.
!macro NSIS_HOOK_PREINSTALL
  ReadRegStr $R0 HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Launcher" "UninstallString"
  ReadRegStr $R1 HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\Launcher" "UninstallString"
  ${If} "$R0$R1" != ""
    MessageBox MB_OK|MB_ICONEXCLAMATION "Launcher is now RunDock. Please uninstall the old Launcher first, keeping its application data, then run this installer again." /SD IDOK
    Abort
  ${EndIf}
!macroend
