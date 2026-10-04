//go:build windows

package recovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var isProcessCritical = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessCritical")

// Exact identity includes creation time and image path. This check happens on
// the same native handle used for termination, closing the PID-reuse race.
func verifyIdentity(h windows.Handle, p Process) error {
	if p.PID <= 4 || p.Created == "" || p.Executable == "" {
		return fmt.Errorf("无法验证占用程序身份，请手动关闭")
	}
	var created, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exit, &kernel, &user); err != nil {
		return fmt.Errorf("无法验证占用程序身份，请手动关闭")
	}
	expected, _ := strconv.ParseUint(p.Created, 10, 64)
	actual := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
	var image [32768]uint16
	length := uint32(len(image))
	if windows.QueryFullProcessImageName(h, 0, &image[0], &length) != nil || actual != expected || exit.HighDateTime != 0 || exit.LowDateTime != 0 || !samePath(windows.UTF16ToString(image[:length]), p.Executable) {
		return fmt.Errorf("占用程序已变化，请重新检查")
	}
	return nil
}

func externalPermission(h windows.Handle, p Process) error {
	if p.PID == os.Getpid() {
		return fmt.Errorf("RunDock 后台受保护，请从软件退出")
	}
	windir, err := windows.GetWindowsDirectory()
	if err != nil || Inside(windir, p.Executable) {
		return fmt.Errorf("Windows 系统程序受保护，请手动处理")
	}
	blocked := map[string]bool{"system": true, "registry": true, "smss.exe": true, "csrss.exe": true, "wininit.exe": true, "winlogon.exe": true, "services.exe": true, "lsass.exe": true, "svchost.exe": true, "fontdrvhost.exe": true, "dwm.exe": true}
	if blocked[strings.ToLower(filepath.Base(p.Executable))] {
		return fmt.Errorf("系统关键程序受保护，请手动处理")
	}
	var critical uint32
	ok, _, _ := isProcessCritical.Call(uintptr(h), uintptr(unsafe.Pointer(&critical)))
	if ok == 0 || critical != 0 {
		return fmt.Errorf("无法确认程序可安全关闭，请手动处理")
	}
	var owner windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &owner) != nil {
		return fmt.Errorf("没有关闭该程序的权限，请从原程序退出")
	}
	defer owner.Close()
	other, err := owner.GetTokenUser()
	if err != nil {
		return fmt.Errorf("无法确认程序所属用户，请手动处理")
	}
	current, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || !other.User.Sid.Equals(current.User.Sid) {
		return fmt.Errorf("该程序属于其他用户，请由原用户关闭")
	}
	var mine, theirs uint32
	if windows.ProcessIdToSessionId(uint32(os.Getpid()), &mine) != nil || windows.ProcessIdToSessionId(uint32(p.PID), &theirs) != nil || mine != theirs {
		return fmt.Errorf("该程序不属于当前登录会话，请手动关闭")
	}
	return nil
}

func CanCloseExternal(p Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(p.PID))
	if err != nil {
		return fmt.Errorf("没有关闭该程序的权限，请从原程序退出")
	}
	defer windows.CloseHandle(h)
	if err := verifyIdentity(h, p); err != nil {
		return err
	}
	return externalPermission(h, p)
}

// End only the confirmed listener process, not its parent or a guessed tree.
func TerminateExternal(p Process) error {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(p.PID))
	if err != nil {
		return fmt.Errorf("无法关闭程序 PID %d，请从原程序退出", p.PID)
	}
	defer windows.CloseHandle(h)
	if err := verifyIdentity(h, p); err != nil {
		return err
	}
	if err := externalPermission(h, p); err != nil {
		return err
	}
	if windows.TerminateProcess(h, 1) != nil {
		return fmt.Errorf("关闭程序 PID %d 失败，请从原程序退出", p.PID)
	}
	status, err := windows.WaitForSingleObject(h, 3000)
	if err != nil || status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("程序 PID %d 尚未退出，请稍后重新检查", p.PID)
	}
	return nil
}
