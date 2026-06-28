package proc

import (
	"bufio"
	"os/exec"
)

// attachPipes 把 stdout/stderr 接管，按行切分后回调。
// 子进程结束关闭管道后，goroutine 自然退出，无需显式取消。
func attachPipes(cmd *exec.Cmd, onStdout, onStderr func(line string)) {
	if onStdout != nil {
		if pipe, err := cmd.StdoutPipe(); err == nil {
			go scanLines(pipe, onStdout)
		}
	} else {
		cmd.Stdout = nil
	}
	if onStderr != nil {
		if pipe, err := cmd.StderrPipe(); err == nil {
			go scanLines(pipe, onStderr)
		}
	} else {
		cmd.Stderr = nil
	}
}

func scanLines(r interface{ Read([]byte) (int, error) }, cb func(string)) {
	scanner := bufio.NewScanner(r)
	// 提高单行上限，避免长行（如打包进度）被截断
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		cb(scanner.Text())
	}
}
