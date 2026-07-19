//go:build windows

package proc

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	session0ServiceArg = "--session0-service"
	session0WorkerArg  = "--session0-worker"
	session0InstallArg = "--install-session0-service"
)

func RunInternalMode(args []string) (bool, int) {
	return runInternalMode(context.Background(), args, os.Stdin, os.Stdout)
}

func runInternalMode(ctx context.Context, args []string, input io.Reader, output io.Writer) (bool, int) {
	if len(args) != 1 {
		return false, 0
	}
	switch args[0] {
	case session0WorkerArg:
		return true, runSession0Worker(ctx, input, output)
	case session0ServiceArg:
		if err := runSession0Service(); err != nil {
			_, _ = fmt.Fprintf(output, "session 0 service: %v\n", err)
			return true, 1
		}
		return true, 0
	case session0InstallArg:
		if err := installRunnerService(); err != nil {
			_, _ = fmt.Fprintf(output, "install session 0 service: %v\n", err)
			return true, 1
		}
		return true, 0
	default:
		return false, 0
	}
}

func EnsureSession0Service() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve sidecar executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve absolute sidecar executable: %w", err)
	}

	ready, err := runnerServiceReady(executable)
	if err == nil && ready {
		return nil
	}
	if err := elevateServiceInstall(executable); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		ready, checkErr := runnerServiceReady(executable)
		if checkErr == nil && ready {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("session 0 runner service did not become ready after elevation")
}

func runnerServiceReady(executable string) (bool, error) {
	runnerExecutable, err := runnerServiceExecutable()
	if err != nil {
		return false, err
	}
	binaryPath, state, err := queryRunnerService()
	if err != nil {
		return false, err
	}
	if !strings.Contains(strings.ToLower(binaryPath), strings.ToLower(runnerExecutable)) {
		return false, nil
	}
	equal, err := filesHaveSameContent(executable, runnerExecutable)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return state == svc.Running && equal, nil
}

func runnerServiceExecutable() (string, error) {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	return filepath.Abs(filepath.Join(base, "ProjectsStartManager", "runner", "launcher-runner.exe"))
}

func filesHaveSameContent(left, right string) (bool, error) {
	leftData, err := os.ReadFile(left)
	if err != nil {
		return false, err
	}
	rightData, err := os.ReadFile(right)
	if err != nil {
		return false, err
	}
	return sha256.Sum256(leftData) == sha256.Sum256(rightData), nil
}

func queryRunnerService() (string, svc.State, error) {
	managerHandle, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return "", 0, fmt.Errorf("open service manager for query: %w", err)
	}
	defer windows.CloseServiceHandle(managerHandle)
	name, err := windows.UTF16PtrFromString(runnerServiceName)
	if err != nil {
		return "", 0, err
	}
	serviceHandle, err := windows.OpenService(managerHandle, name, windows.SERVICE_QUERY_CONFIG|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return "", 0, err
	}
	service := &mgr.Service{Name: runnerServiceName, Handle: serviceHandle}
	defer service.Close()
	config, err := service.Config()
	if err != nil {
		return "", 0, err
	}
	status, err := service.Query()
	if err != nil {
		return "", 0, err
	}
	return config.BinaryPathName, status.State, nil
}

func elevateServiceInstall(executable string) error {
	runas, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	args, _ := windows.UTF16PtrFromString(session0InstallArg)
	cwd, err := windows.UTF16PtrFromString(filepath.Dir(executable))
	if err != nil {
		return err
	}
	if err := windows.ShellExecute(0, runas, file, args, cwd, windows.SW_HIDE); err != nil {
		return fmt.Errorf("request elevation for runner service: %w", err)
	}
	return nil
}

func installRunnerService() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	runnerExecutable, err := runnerServiceExecutable()
	if err != nil {
		return err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(runnerServiceName)
	if err == nil {
		config, configErr := service.Config()
		equal, equalErr := filesHaveSameContent(executable, runnerExecutable)
		if configErr == nil && equalErr == nil && equal && strings.Contains(strings.ToLower(config.BinaryPathName), strings.ToLower(runnerExecutable)) {
			status, queryErr := service.Query()
			if queryErr == nil && status.State != svc.Running {
				if startErr := service.Start(); startErr != nil && startErr != windows.ERROR_SERVICE_ALREADY_RUNNING {
					service.Close()
					return fmt.Errorf("start runner service: %w", startErr)
				}
			}
			service.Close()
			return nil
		}
		_ = stopService(service)
		if err := service.Delete(); err != nil {
			service.Close()
			return fmt.Errorf("replace runner service: %w", err)
		}
		service.Close()
	} else if err != windows.ERROR_SERVICE_DOES_NOT_EXIST {
		return fmt.Errorf("open runner service: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(executable), filepath.Clean(runnerExecutable)) {
		if err := os.MkdirAll(filepath.Dir(runnerExecutable), 0o755); err != nil {
			return fmt.Errorf("create runner service directory: %w", err)
		}
		data, err := os.ReadFile(executable)
		if err != nil {
			return fmt.Errorf("read runner executable: %w", err)
		}
		if err := os.WriteFile(runnerExecutable, data, 0o755); err != nil {
			return fmt.Errorf("write runner service executable: %w", err)
		}
	}

	config := mgr.Config{
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  "Projects Start Manager Runner",
		Description:  "Runs managed console applications without interactive Windows console windows.",
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		service, err = manager.CreateService(runnerServiceName, runnerExecutable, config, session0ServiceArg)
		if err == nil {
			break
		}
		if err != windows.ERROR_SERVICE_MARKED_FOR_DELETE || time.Now().After(deadline) {
			return fmt.Errorf("create runner service: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	defer service.Close()
	if err := service.Start(); err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return fmt.Errorf("start runner service: %w", err)
	}
	ready, err := waitForServiceState(service, svc.Running, 10*time.Second)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("runner service did not enter running state")
	}
	return nil
}

func stopService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil && err != windows.ERROR_SERVICE_NOT_ACTIVE {
		return err
	}
	_, err = waitForServiceState(service, svc.Stopped, 10*time.Second)
	return err
}

func waitForServiceState(service *mgr.Service, target svc.State, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for {
		status, err := service.Query()
		if err != nil {
			return false, err
		}
		if status.State == target {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
}
