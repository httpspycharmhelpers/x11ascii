package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BurntSushi/xgb/xproto"
)

// session 负责把整条流程串起来：
//
//	输入命令 → 开启 X11（可选经典桌面）→ 起程序 → 定位程序窗口 → 渲染到 Termux
//	→ 退出时按“程序 → 桌面 → X11”顺序全部关掉
type session struct {
	display string
	x11     *exec.Cmd // 由本进程启动的 termux-x11（复用已有 X 时为 nil）
	desktop *exec.Cmd // 桌面会话（可选）
	owned   bool      // X11 是否归本进程所有（false=复用了已有的，不负责关）
}

type sessionOpts struct {
	display string // 目标 display，空=自动挑一个空闲编号
	x11Args string // 传给 termux-x11 的额外参数
	desktop string // 桌面会话命令，空=不启桌面（裸 X，只跑目标程序）
}

// startX11Session 启动（或复用）一个 X11 display，可选地再拉起经典桌面。
func startX11Session(o sessionOpts) (*session, error) {
	display := strings.TrimSpace(o.display)
	if display == "" {
		display = pickFreeDisplay()
	} else if !strings.HasPrefix(display, ":") {
		display = ":" + display
	}
	s := &session{display: display}
	if x11Alive(display) {
		return s, nil // 已有 X 在跑，直接复用，退出时不动它
	}

	// 清掉上次异常退出留下的死 socket，否则 bind 会失败
	num := strings.TrimPrefix(display, ":")
	if i := strings.IndexByte(num, '.'); i >= 0 {
		num = num[:i]
	}
	for _, p := range socketCandidates(num) {
		if _, err := os.Stat(p); err == nil {
			_ = os.Remove(p)
		}
	}

	args := append([]string{display}, strings.Fields(o.x11Args)...)
	cmd := exec.Command("termux-x11", args...)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 termux-x11 失败: %w", err)
	}
	s.x11, s.owned = cmd, true
	if err := waitX11(display, 30*time.Second); err != nil {
		s.stop()
		return nil, err
	}
	return s, nil
}

// startDesktop 在已就绪的 display 上拉起桌面会话，并等到它画出窗口为止。
func (s *session) startDesktop(cmdline string) error {
	if strings.TrimSpace(cmdline) == "" {
		return nil
	}
	c := exec.Command("sh", "-c", cmdline)
	c.Env = append(os.Environ(), "DISPLAY="+s.display)
	c.Stdout, c.Stderr = os.Stderr, os.Stderr
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return fmt.Errorf("启动桌面失败: %w", err)
	}
	s.desktop = c
	waitDesktop(s.display, 15*time.Second)
	return nil
}

// stop 按“桌面 → X11”顺序收干净（程序由 stopChild 先行，因为 defer 是后进先出）。
func (s *session) stop() {
	stopChild(s.desktop)
	s.desktop = nil
	if s.owned {
		stopChild(s.x11)
		s.x11 = nil
		num := strings.TrimPrefix(s.display, ":")
		if i := strings.IndexByte(num, '.'); i >= 0 {
			num = num[:i]
		}
		for _, p := range socketCandidates(num) {
			if _, err := os.Stat(p); err == nil {
				_ = os.Remove(p)
			}
		}
	}
}

// x11Alive 探测某个 display 是否已有 X server 在监听。
func x11Alive(display string) bool {
	conn, _, err := dialX11(display)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// pickFreeDisplay 找一个没有 X 在跑的 display 编号。
func pickFreeDisplay() string {
	for n := 1; n <= 9; n++ {
		d := ":" + strconv.Itoa(n)
		if !x11Alive(d) {
			return d
		}
	}
	return ":1"
}

// waitX11 轮询等待 X server 可连接。
func waitX11(display string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if x11Alive(display) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("等待 X11 %s 就绪超时", display)
}

// waitDesktop 等桌面会话画出第一个真正的窗口（避免程序比桌面先起来）。
func waitDesktop(display string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if desktopWindowsUp(display) {
			return
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func desktopWindowsUp(display string) bool {
	conn, _, err := dialX11(display)
	if err != nil {
		return false
	}
	defer conn.Close()
	root := xproto.Setup(conn).Roots[0]
	tree, err := xproto.QueryTree(conn, root.Root).Reply()
	if err != nil {
		return false
	}
	for _, w := range tree.Children {
		geo, err := xproto.GetGeometry(conn, xproto.Drawable(w)).Reply()
		if err != nil || geo.Width < 2 || geo.Height < 2 {
			continue
		}
		attr, err := xproto.GetWindowAttributes(conn, w).Reply()
		if err == nil && attr.MapState == xproto.MapStateViewable {
			return true
		}
	}
	return false
}
