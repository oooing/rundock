//go:build windows

package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

const (
	runnerServiceName = "ProjectsStartManagerRunner"
	runnerPipeName    = `\\.\pipe\ProjectsStartManagerRunner-v1`
	runnerPipeMode    = windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE | windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS
)

var impersonateNamedPipeClient = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

type session0Service struct{}

type overlappedPipe struct {
	handle windows.Handle
	once   sync.Once
}

func (p *overlappedPipe) Fd() uintptr { return uintptr(p.handle) }

func (p *overlappedPipe) Read(data []byte) (int, error) {
	var done uint32
	err := p.runIO(func(overlapped *windows.Overlapped) error {
		return windows.ReadFile(p.handle, data, &done, overlapped)
	}, &done)
	if errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_NO_DATA) {
		return 0, io.EOF
	}
	if errors.Is(err, windows.ERROR_OPERATION_ABORTED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		return 0, os.ErrClosed
	}
	return int(done), err
}

func (p *overlappedPipe) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		var done uint32
		err := p.runIO(func(overlapped *windows.Overlapped) error {
			return windows.WriteFile(p.handle, data, &done, overlapped)
		}, &done)
		if errors.Is(err, windows.ERROR_OPERATION_ABORTED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			err = os.ErrClosed
		}
		if err != nil {
			return total, err
		}
		total += int(done)
		data = data[done:]
	}
	return total, nil
}

func (p *overlappedPipe) runIO(start func(*windows.Overlapped) error, done *uint32) error {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)

	overlapped := &windows.Overlapped{HEvent: event}
	err = start(overlapped)
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return err
	}
	if _, err := windows.WaitForSingleObject(event, windows.INFINITE); err != nil {
		return err
	}
	return windows.GetOverlappedResult(p.handle, overlapped, done, false)
}

func (p *overlappedPipe) Close() error {
	var err error
	p.once.Do(func() {
		_ = windows.CancelIoEx(p.handle, nil)
		err = windows.CloseHandle(p.handle)
	})
	return err
}

func (s *session0Service) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	logger, closeLog, err := openRunnerServiceLogger()
	if err != nil {
		return true, 1
	}
	defer closeLog()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveRunnerPipes(ctx, logger) }()
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	logger.Printf("runner service started")

	for {
		select {
		case err := <-done:
			if err != nil {
				logger.Printf("runner service failed: %v", err)
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				wakeRunnerPipe()
				err := <-done
				if err != nil {
					logger.Printf("runner service stop failed: %v", err)
					return true, 1
				}
				logger.Printf("runner service stopped")
				return false, 0
			}
		}
	}
}

func runSession0Service() error {
	return svc.Run(runnerServiceName, &session0Service{})
}

func serveRunnerPipes(ctx context.Context, logger *log.Logger) error {
	for {
		pipe, err := acceptRunnerPipe()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			_ = pipe.Close()
			return nil
		}
		go func() {
			if err := handleRunnerClient(pipe); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				logger.Printf("runner request failed: %v", err)
			}
		}()
	}
}

func acceptRunnerPipe() (*overlappedPipe, error) {
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)")
	if err != nil {
		return nil, fmt.Errorf("create pipe security descriptor: %w", err)
	}
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	name, err := windows.UTF16PtrFromString(runnerPipeName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateNamedPipe(
		name,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED,
		runnerPipeMode,
		windows.PIPE_UNLIMITED_INSTANCES,
		64<<10,
		64<<10,
		0,
		sa,
	)
	if err != nil {
		return nil, fmt.Errorf("create runner pipe: %w", err)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("create runner connect event: %w", err)
	}
	defer windows.CloseHandle(event)
	overlapped := &windows.Overlapped{HEvent: event}
	err = windows.ConnectNamedPipe(handle, overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		if _, waitErr := windows.WaitForSingleObject(event, windows.INFINITE); waitErr != nil {
			_ = windows.CloseHandle(handle)
			return nil, fmt.Errorf("wait for runner pipe client: %w", waitErr)
		}
		var done uint32
		err = windows.GetOverlappedResult(handle, overlapped, &done, false)
	}
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("connect runner pipe: %w", err)
	}
	return &overlappedPipe{handle: handle}, nil
}

func wakeRunnerPipe() {
	pipe, err := openRunnerPipeOnce()
	if err == nil {
		_ = pipe.Close()
	}
}

func openRunnerPipeOnce() (*overlappedPipe, error) {
	name, err := windows.UTF16PtrFromString(runnerPipeName)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IMPERSONATION|windows.FILE_FLAG_OVERLAPPED,
		0,
	)
	if err != nil {
		return nil, err
	}
	return &overlappedPipe{handle: handle}, nil
}

func openRunnerPipe() (*overlappedPipe, error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		pipe, err := openRunnerPipeOnce()
		if err == nil {
			return pipe, nil
		}
		if err != windows.ERROR_PIPE_BUSY && err != windows.ERROR_FILE_NOT_FOUND {
			return nil, fmt.Errorf("open session 0 runner pipe: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("session 0 runner service is unavailable: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func handleRunnerClient(client *overlappedPipe) error {
	defer client.Close()
	start, err := readRunnerFrame(client)
	if err != nil {
		return err
	}
	if start.Type != "start" || start.Spec == nil {
		err := fmt.Errorf("runner service expected start frame")
		_ = writeRunnerFrame(client, runnerFrame{Type: "error", Error: err.Error()})
		return err
	}
	token, err := duplicatePipeClientToken(windows.Handle(client.Fd()))
	if err != nil {
		_ = writeRunnerFrame(client, runnerFrame{Type: "error", Error: err.Error()})
		return err
	}
	defer token.Close()

	worker, job, err := launchSession0Worker(token)
	if err != nil {
		_ = writeRunnerFrame(client, runnerFrame{Type: "error", Error: err.Error()})
		return err
	}
	defer worker.Close()
	defer windows.CloseHandle(job)
	defer windows.TerminateJobObject(job, 1)

	if err := writeRunnerFrame(worker, start); err != nil {
		return fmt.Errorf("send start frame to worker: %w", err)
	}
	first, err := readRunnerFrame(worker)
	if err != nil {
		return fmt.Errorf("read worker start response: %w", err)
	}
	if err := writeRunnerFrame(client, first); err != nil {
		return fmt.Errorf("send start response to client: %w", err)
	}
	if first.Type != "started" {
		if first.Type == "error" {
			return fmt.Errorf("worker start failed: %s", first.Error)
		}
		return fmt.Errorf("unexpected worker start response: %q", first.Type)
	}
	return relayRunnerConnection(client, worker)
}

func duplicatePipeClientToken(pipe windows.Handle) (windows.Token, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	result, _, callErr := impersonateNamedPipeClient.Call(uintptr(pipe))
	if result == 0 {
		return 0, fmt.Errorf("impersonate pipe client: %w", nonzeroSyscallError(callErr))
	}
	reverted := false
	defer func() {
		if !reverted {
			_ = windows.RevertToSelf()
		}
	}()

	var impersonationToken windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, false, &impersonationToken); err != nil {
		return 0, fmt.Errorf("open pipe client token: %w", err)
	}
	defer impersonationToken.Close()

	const desiredAccess = windows.TOKEN_ASSIGN_PRIMARY | windows.TOKEN_DUPLICATE | windows.TOKEN_QUERY | windows.TOKEN_ADJUST_DEFAULT | windows.TOKEN_ADJUST_SESSIONID
	var primaryToken windows.Token
	if err := windows.DuplicateTokenEx(impersonationToken, desiredAccess, nil, windows.SecurityImpersonation, windows.TokenPrimary, &primaryToken); err != nil {
		return 0, fmt.Errorf("duplicate pipe client token: %w", err)
	}
	if err := windows.RevertToSelf(); err != nil {
		primaryToken.Close()
		return 0, fmt.Errorf("revert pipe impersonation: %w", err)
	}
	reverted = true

	sessionID := uint32(0)
	if err := windows.SetTokenInformation(primaryToken, windows.TokenSessionId, (*byte)(unsafe.Pointer(&sessionID)), uint32(unsafe.Sizeof(sessionID))); err != nil {
		primaryToken.Close()
		return 0, fmt.Errorf("move pipe client token to session 0: %w", err)
	}
	return primaryToken, nil
}

func nonzeroSyscallError(err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return syscall.EINVAL
	}
	return err
}

type workerConnection struct {
	reader  *os.File
	writer  *os.File
	process windows.Handle
	once    sync.Once
}

func (c *workerConnection) Read(p []byte) (int, error)  { return c.reader.Read(p) }
func (c *workerConnection) Write(p []byte) (int, error) { return c.writer.Write(p) }
func (c *workerConnection) Close() error {
	var first error
	c.once.Do(func() {
		if err := c.reader.Close(); err != nil {
			first = err
		}
		if err := c.writer.Close(); err != nil && first == nil {
			first = err
		}
		if err := windows.CloseHandle(c.process); err != nil && first == nil {
			first = err
		}
	})
	return first
}

func launchSession0Worker(token windows.Token) (*workerConnection, windows.Handle, error) {
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	var stdinRead, stdinWrite, stdoutRead, stdoutWrite windows.Handle
	if err := windows.CreatePipe(&stdinRead, &stdinWrite, sa, 0); err != nil {
		return nil, 0, fmt.Errorf("create worker stdin pipe: %w", err)
	}
	defer func() {
		if stdinRead != 0 {
			_ = windows.CloseHandle(stdinRead)
		}
		if stdinWrite != 0 {
			_ = windows.CloseHandle(stdinWrite)
		}
	}()
	if err := windows.CreatePipe(&stdoutRead, &stdoutWrite, sa, 0); err != nil {
		return nil, 0, fmt.Errorf("create worker stdout pipe: %w", err)
	}
	defer func() {
		if stdoutRead != 0 {
			_ = windows.CloseHandle(stdoutRead)
		}
		if stdoutWrite != 0 {
			_ = windows.CloseHandle(stdoutWrite)
		}
	}()
	if err := windows.SetHandleInformation(stdinWrite, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, 0, fmt.Errorf("protect worker stdin writer: %w", err)
	}
	if err := windows.SetHandleInformation(stdoutRead, windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		return nil, 0, fmt.Errorf("protect worker stdout reader: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return nil, 0, fmt.Errorf("resolve runner executable: %w", err)
	}
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, 0, err
	}
	commandLine, err := windows.UTF16PtrFromString(`"` + executable + `" --session0-worker`)
	if err != nil {
		return nil, 0, err
	}
	currentDir, err := windows.UTF16PtrFromString(filepath.Dir(executable))
	if err != nil {
		return nil, 0, err
	}
	var environment *uint16
	if err := windows.CreateEnvironmentBlock(&environment, token, false); err != nil {
		return nil, 0, fmt.Errorf("create worker user environment: %w", err)
	}
	defer windows.DestroyEnvironmentBlock(environment)

	startup := windows.StartupInfo{
		Cb:        uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Flags:     windows.STARTF_USESTDHANDLES,
		StdInput:  stdinRead,
		StdOutput: stdoutWrite,
		StdErr:    stdoutWrite,
	}
	var process windows.ProcessInformation
	if err := windows.CreateProcessAsUser(
		token,
		application,
		commandLine,
		nil,
		nil,
		true,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW,
		environment,
		currentDir,
		&startup,
		&process,
	); err != nil {
		return nil, 0, fmt.Errorf("create session 0 worker as pipe client: %w", err)
	}
	_ = windows.CloseHandle(process.Thread)
	stdinRead = 0
	stdoutWrite = 0
	_ = windows.CloseHandle(startup.StdInput)
	_ = windows.CloseHandle(startup.StdOutput)

	job, err := createStrictRunnerJob(process.Process)
	if err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		_ = windows.CloseHandle(process.Process)
		return nil, 0, err
	}

	connection := &workerConnection{
		reader:  os.NewFile(uintptr(stdoutRead), "runner-worker-stdout"),
		writer:  os.NewFile(uintptr(stdinWrite), "runner-worker-stdin"),
		process: process.Process,
	}
	stdoutRead = 0
	stdinWrite = 0
	return connection, job, nil
}

func createStrictRunnerJob(process windows.Handle) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create runner job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configure runner job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("assign worker to runner job: %w", err)
	}
	return job, nil
}

func openRunnerServiceLogger() (*log.Logger, func(), error) {
	base := os.Getenv("ProgramData")
	if base == "" {
		base = `C:\ProgramData`
	}
	dir := filepath.Join(base, "ProjectsStartManager")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create runner log directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(dir, "runner-service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open runner service log: %w", err)
	}
	return log.New(file, "", log.LstdFlags|log.Lmicroseconds), func() { _ = file.Close() }, nil
}
