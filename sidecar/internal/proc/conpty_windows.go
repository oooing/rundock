//go:build windows

package proc

import (
	"context"
	"fmt"
	"sync"

	"github.com/UserExistsError/conpty"
)

// ConPTY 封装一个 Windows 伪控制台会话（用成熟库 github.com/UserExistsError/conpty）。
// 价值：给子进程一个完整的伪控制台，让 timeout / pause / 彩色输出 / Ctrl+C 等
// 依赖控制台的命令正常工作，同时不弹出真实窗口。
//
// 之前手写 CreatePseudoConsole + 管道管理有句柄继承/输出读取的坑，
// 改用现成库避免重复踩 Windows ConPTY 的系统编程陷阱。

// conPTYSession 封装 conpty 进程。
type conPTYSession struct {
	c       *conpty.ConPty
	writeMu sync.Mutex
}

// startConPTY 用伪控制台启动一条命令，返回会话与根 PID。
func startConPTY(ctx context.Context, cmd string, args []string, cwd string, env []string, onOutput func(b []byte)) (*conPTYSession, int, error) {
	// 拼命令行：给含空格的参数加引号（路径里有空格时必须，否则 cmd.exe 会在空格处截断）
	cmdLine := quoteIfSpace(cmd)
	for _, a := range args {
		cmdLine += " " + quoteIfSpace(a)
	}

	opts := []conpty.ConPtyOption{}
	if cwd != "" {
		opts = append(opts, conpty.ConPtyWorkDir(cwd))
	}
	c, err := conpty.Start(cmdLine, opts...)
	if err != nil {
		return nil, 0, fmt.Errorf("conpty start: %w", err)
	}

	pid := c.Pid()

	// 后台读 pty 输出，转发给 onOutput
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			if n > 0 && onOutput != nil {
				out := make([]byte, n)
				copy(out, buf[:n])
				onOutput(out)
			}
			if err != nil {
				return
			}
		}
	}()

	return &conPTYSession{c: c}, pid, nil
}

// sendCtrlC 通过向 pty 写 Ctrl+C(0x03) 触发优雅停止。
func (s *conPTYSession) sendCtrlC() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.c.Write([]byte{0x03})
	return err
}

// close 释放所有资源。
func (s *conPTYSession) close() {
	if s.c != nil {
		s.c.Close()
	}
}

// wait 等待进程退出，返回退出码。
func (s *conPTYSession) wait() (int, error) {
	if s.c == nil {
		return -1, fmt.Errorf("no process")
	}
	code, err := s.c.Wait(context.Background())
	return int(code), err
}

// terminate 强制终止（Close 会终止进程）。
func (s *conPTYSession) terminate() error {
	if s.c != nil {
		return s.c.Close()
	}
	return nil
}

// ----- proc 包集成：StartWithConPTY + helpers -----

// StartWithConPTY 用 ConPTY 启动命令。
// onLine 收到按行切分的输出（pty 不区分 stdout/stderr，统一输出）。
func StartWithConPTY(ctx context.Context, pc *PreparedCommand, onLine func(line string)) (*Handle, error) {
	if pc == nil {
		return nil, fmt.Errorf("nil prepared command")
	}
	innerCtx, cancel := context.WithCancel(ctx)

	lb := newLineBuffer(onLine)

	// 把 env map 合并进环境（conpty 库默认继承父进程环境，这里用注入的覆盖）
	env := mergeEnv(pc.Env)
	sess, pid, err := startConPTY(innerCtx, pc.Cmd, pc.Args, pc.Cwd, env, lb.feed)
	if err != nil {
		cancel()
		return nil, err
	}

	h := &Handle{
		rootPID: pid,
		cancel:  cancel,
		pty:     sess,
	}
	// ConPTY 进程也加入 Job Object（保证整树可回收）
	h.jobCloser = assignJob(pid)
	return h, nil
}

// closeConPTY 关闭 ConPTY 会话资源。
func closeConPTY(s interface{}) {
	if sess, ok := s.(*conPTYSession); ok {
		sess.close()
	}
}

// waitConPTY 等待进程退出。
func waitConPTY(s interface{}) (int, error) {
	if sess, ok := s.(*conPTYSession); ok {
		return sess.wait()
	}
	return -1, fmt.Errorf("not conpty session")
}

// gracefulStopConPTY 向 pty 发 Ctrl+C。
func gracefulStopConPTY(s interface{}) error {
	if sess, ok := s.(*conPTYSession); ok {
		return sess.sendCtrlC()
	}
	return nil
}

// terminateConPTY 强制终止。
func terminateConPTY(s interface{}) error {
	if sess, ok := s.(*conPTYSession); ok {
		return sess.terminate()
	}
	return nil
}

// lineBuffer 把可能分片到达的 pty 字节流按行切分后回调。
type lineBuffer struct {
	buf []byte
	cb  func(line string)
}

func newLineBuffer(cb func(line string)) *lineBuffer {
	return &lineBuffer{cb: cb}
}

func (lb *lineBuffer) feed(b []byte) {
	lb.buf = append(lb.buf, b...)
	for {
		idx := -1
		for i, c := range lb.buf {
			if c == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		line := string(lb.buf[:idx])
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		line = stripANSI(line)
		if line != "" && lb.cb != nil {
			lb.cb(line)
		}
		lb.buf = lb.buf[idx+1:]
	}
}

// quoteIfSpace 给含空格的参数加双引号（Windows 命令行要求）。
func quoteIfSpace(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return "\"" + s + "\""
		}
	}
	return s
}

// stripANSI 去除 ANSI 转义序列。
func stripANSI(s string) string {
	out := make([]byte, 0, len(s))
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
				inEsc = false
			}
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
