package main

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// startChild 启动被包裹的命令（如 dosbox）到独立的进程组，
// 并把 DISPLAY 传给它，方便退出时整组清理。
func startChild(cmdline, display string) (*exec.Cmd, error) {
	c := exec.Command("sh", "-c", cmdline)
	env := os.Environ()
	if display != "" {
		env = append(env, "DISPLAY="+display)
	}
	c.Env = env
	c.Stdout = os.Stderr
	c.Stderr = os.Stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return c, nil
}

// stopChild 先 SIGTERM 整个进程组，宽限后 SIGKILL。
func stopChild(c *exec.Cmd) {
	if c == nil || c.Process == nil {
		return
	}
	pgid := c.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = c.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
}
