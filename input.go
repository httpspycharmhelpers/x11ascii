package main

import (
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// X keysyms（仅用到的这些）
const (
	ksBackSpace = 0xFF08
	ksTab       = 0xFF09
	ksReturn    = 0xFF0D
	ksEscape    = 0xFF1B
	ksHome      = 0xFF50
	ksLeft      = 0xFF51
	ksUp        = 0xFF52
	ksRight     = 0xFF53
	ksDown      = 0xFF54
	ksPageUp    = 0xFF55
	ksPageDown  = 0xFF56
	ksEnd       = 0xFF57
	ksInsert    = 0xFF63
	ksDelete    = 0xFFFF
	ksF1        = 0xFFBE

	ksControlL = 0xFFE3
	ksShiftL   = 0xFFE1
	ksAltL     = 0xFFE9
)

// parseCtrlKeys 解析 -ctrl "a,q" 这类列表：终端不会上报「单独按 Ctrl」，
// 所以让用户指定一个普通键去冒充长按 Ctrl（游戏开枪/斜跑必需）。
func parseCtrlKeys(list string) map[uint32]bool {
	out := map[uint32]bool{}
	for _, part := range strings.Split(list, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(part) == 1 {
			out[uint32(part[0])] = true
			continue
		}
		if sym, ok := keysymByName(part); ok {
			out[sym] = true
		}
	}
	return out
}

// keysymByName 把 "F12"/"space"/"a" 之类名字转成 X keysym。
func keysymByName(name string) (uint32, bool) {
	switch strings.ToLower(name) {
	case "space":
		return ' ', true
	case "tab":
		return ksTab, true
	case "escape", "esc":
		return ksEscape, true
	case "return", "enter":
		return ksReturn, true
	case "up":
		return ksUp, true
	case "down":
		return ksDown, true
	case "left":
		return ksLeft, true
	case "right":
		return ksRight, true
	case "ctrl", "control":
		return ksControlL, true
	case "shift":
		return ksShiftL, true
	case "alt":
		return ksAltL, true
	}
	if len(name) == 1 {
		return uint32(name[0]), true
	}
	// F1..F12
	if len(name) >= 2 && (name[0] == 'f' || name[0] == 'F') {
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 12 {
			return ksF1 + uint32(n-1), true
		}
	}
	return 0, false
}

// discardSink 什么都不做：没有 X 源时把按键丢掉，但界面层热键照样能用。
type discardSink struct{}

func (discardSink) key(sym uint32, ctrl, alt, shift bool)      {}
func (discardSink) mouseBtn(btn int, press bool, col, row int) {}
func (discardSink) mouseMove(col, row int)                     {}
func (discardSink) wheel(dir int, col, row int)                {}

// inputSink 收到解析后的输入事件（坐标 col/row 为终端 1-based 单元格）。
type inputSink interface {
	key(sym uint32, ctrl, alt, shift bool)
	mouseBtn(btn int, press bool, col, row int)
	mouseMove(col, row int)
	wheel(dir int, col, row int)
}

// parseMod 解析 xterm 的修饰键参数（CSI 1;5A 里的 5）。
// 位定义：1=Shift 2=Alt 4=Ctrl(Control) 8=Meta，且参数值是「位+1」。
func parseMod(param string) (ctrl, alt, shift bool) {
	n, err := strconv.Atoi(param)
	if err != nil || n < 2 {
		return
	}
	m := n - 1
	return m&4 != 0, m&2 != 0, m&1 != 0
}

type byteSource interface {
	readByte() (byte, error)
	readable(time.Duration) bool
}

type fdSource struct{ fd int }

func (f fdSource) readByte() (byte, error) {
	var b [1]byte
	n, err := unix.Read(f.fd, b[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return b[0], nil
}

func (f fdSource) readable(d time.Duration) bool {
	pfd := []unix.PollFd{{Fd: int32(f.fd), Events: unix.POLLIN}}
	n, err := unix.Poll(pfd, int(d.Milliseconds()))
	return err == nil && n > 0
}

// inputPump 把终端字节流解析成按键/鼠标事件并交给 sink。
// 退出通道（谁都能退出去，不要只能靠 kill）：
//   - Ctrl+C（字节 0x03）
//   - 连按两下 Esc（400ms 内）——手机上 ESC 键最容易够到
//   - 快速连点 N 下（-quit-taps，默认 3）——纯触屏唯一可靠的退路
//   - -quitkey 指定的单键
type inputPump struct {
	src    byteSource
	sink   inputSink
	onQuit func()

	quitSym   uint32 // -quitkey，0=不用
	quitTaps  int    // 连点几下退出，0=关闭
	escWaitMS int    // ESC 后等转义序列的窗口（毫秒），0=用默认

	escAt    time.Time // 上一键 Esc 的时间
	taps     int       // 当前连点计数
	tapAt    time.Time // 连点序列的第一次时间
	lastTap  time.Time // 上一次点击
	ctrlKeys map[uint32]bool
	remap    map[uint32]keyAction // 自定义键位：手机上的键 → 发给程序的实际按键
	onMouse  func()               // 有鼠标/触摸事件时回调（主循环据此避免抢焦点）
	// 点击/触摸位置（1-based 单元格）。返回 true 表示这次点击已被消费掉
	// （例如落在屏幕底部的虚拟键盘条上），不再当成鼠标点击发给被包裹的程序。
	onTap func(col, row int) bool
	// hook 在把按键发给程序之前先过一道：返回 true 表示被界面层消费掉了
	// （输入层打字、缩放/分辨率热键都走这里）。
	hook func(sym uint32, ctrl, alt, shift bool) bool
}

func (p *inputPump) mouseMoved() {
	if p.onMouse != nil {
		p.onMouse()
	}
}

// wantQuit 判断这个按键是否触发退出（双 Esc / -quitkey）。
func (p *inputPump) wantQuit(sym uint32) bool {
	if sym == 0 {
		return false
	}
	now := time.Now()
	if sym == ksEscape {
		if now.Sub(p.escAt) < 400*time.Millisecond && !p.escAt.IsZero() {
			return true
		}
		p.escAt = now
	}
	return p.quitSym != 0 && sym == p.quitSym
}

// tapQuit 快速连点退出：纯触屏用户没有键盘，这是唯一可靠的退路。
func (p *inputPump) tapQuit() bool {
	if p.quitTaps <= 0 {
		return false
	}
	now := time.Now()
	if now.Sub(p.tapAt) > 1200*time.Millisecond {
		p.taps, p.tapAt = 0, now
	}
	if p.taps > 0 && now.Sub(p.lastTap) < 450*time.Millisecond {
		p.taps++
	} else {
		p.taps, p.tapAt = 1, now
	}
	p.lastTap = now
	return p.taps >= p.quitTaps
}

// lookupRemap 大小写都试一次，手机键盘带 Shift 时也能命中。
func (p *inputPump) lookupRemap(sym uint32) (keyAction, bool) {
	if len(p.remap) == 0 {
		return keyAction{}, false
	}
	if a, ok := p.remap[sym]; ok {
		return a, true
	}
	if sym >= 'a' && sym <= 'z' {
		if a, ok := p.remap[sym-32]; ok {
			return a, true
		}
	}
	if sym >= 'A' && sym <= 'Z' {
		if a, ok := p.remap[sym+32]; ok {
			return a, true
		}
	}
	return keyAction{}, false
}

func (p *inputPump) fireQuit() {
	if p.onQuit != nil {
		p.onQuit()
	}
}

func (p *inputPump) run() {
	for {
		b, err := p.src.readByte()
		if err != nil {
			return
		}
		if b == 0x03 {
			p.fireQuit()
			return
		}
		if b == 0x1b {
			// ESC 后面的转义序列（方向键、Termux 扩展键、F 键）必须在同一个
			// 等待窗口里到齐。窗口太小的话，Android 输入法/扩展键一旦把
			// "ESC [ A" 分成两次送达，方向键就会变成 Escape + "[" + "a"，
			// 游戏里表现为「方向键失灵、还会乱打字」。
			if !p.src.readable(p.escWait()) {
				if p.wantQuit(ksEscape) {
					p.fireQuit()
					return
				}
				p.sink.key(ksEscape, false, false, false)
				continue
			}
			b2, err := p.src.readByte()
			if err != nil {
				return
			}
			switch b2 {
			case '[', 'O':
				p.csi()
			default:
				p.emitByte(b2, true)
			}
			continue
		}
		p.emitByte(b, false)
	}
}

func (p *inputPump) emitKey(sym uint32, ctrl, alt, shift bool) {
	// 界面层优先：输入层打开时按键都归它，其余情况只吃热键
	if p.hook != nil && p.hook(sym, ctrl, alt, shift) {
		return
	}
	// 自定义键位优先：把手机上的键换成程序真正认的组合键
	if act, ok := p.lookupRemap(sym); ok {
		p.sink.key(act.sym, act.ctrl || ctrl, act.alt || alt, act.shift || shift)
		return
	}
	if p.ctrlKeys[sym] {
		// 终端不会上报「单独按 Ctrl」，所以用普通键冒充长按 Ctrl（游戏开枪/斜跑用）
		p.sink.key(ksControlL, false, false, false)
		return
	}
	if p.wantQuit(sym) {
		p.fireQuit()
		return
	}
	p.sink.key(sym, ctrl, alt, shift)
}

func (p *inputPump) emitByte(b byte, alt bool) {
	if b >= 0x80 {
		p.emitRune(b, alt)
		return
	}
	switch {
	case b == 0x09:
		p.emitKey(ksTab, false, alt, false)
	case b == 0x0d || b == 0x0a:
		p.emitKey(ksReturn, false, alt, false)
	case b == 0x08 || b == 0x7f:
		p.emitKey(ksBackSpace, false, alt, false)
	case b == 0x00:
		p.emitKey(' ', true, alt, false)
	case b >= 0x01 && b <= 0x1a:
		p.emitKey(uint32(b)+0x60, true, alt, false)
	case b == 0x1c:
		p.emitKey('\\', true, alt, false)
	case b == 0x1d:
		p.emitKey(']', true, alt, false)
	case b == 0x1e:
		p.emitKey('^', true, alt, false)
	case b == 0x1f:
		p.emitKey('_', true, alt, false)
	case b >= 0x20 && b <= 0x7e:
		p.emitKey(uint32(b), false, alt, false)
	}
}

func (p *inputPump) emitRune(lead byte, alt bool) {
	var buf [4]byte
	buf[0] = lead
	n := 1
	want := 1
	switch {
	case lead&0xE0 == 0xC0:
		want = 2
	case lead&0xF0 == 0xE0:
		want = 3
	case lead&0xF8 == 0xF0:
		want = 4
	default:
		return
	}
	for n < want {
		nb, err := p.src.readByte()
		if err != nil {
			return
		}
		buf[n] = nb
		n++
	}
	r, _ := utf8.DecodeRune(buf[:n])
	if r == utf8.RuneError {
		return
	}
	sym := uint32(r)
	if r > 0xff {
		sym = 0x01000000 | uint32(r)
	}
	p.emitKey(sym, false, alt, false)
}

func (p *inputPump) csi() {
	var sb strings.Builder
	for {
		b, err := p.src.readByte()
		if err != nil {
			return
		}
		if b >= 0x40 && b <= 0x7e {
			p.interpretCSI(sb.String(), b)
			return
		}
		sb.WriteByte(b)
	}
}

// escWait 是 ESC 后等待转义序列后续字节的时间。
const escWaitDefault = 150 * time.Millisecond

func (p *inputPump) escWait() time.Duration {
	if p.escWaitMS > 0 {
		return time.Duration(p.escWaitMS) * time.Millisecond
	}
	return escWaitDefault
}

func (p *inputPump) interpretCSI(s string, final byte) {
	if strings.HasPrefix(s, "<") {
		parts := strings.Split(s[1:], ";")
		if len(parts) >= 3 {
			cb, _ := strconv.Atoi(parts[0])
			x, _ := strconv.Atoi(parts[1])
			y, _ := strconv.Atoi(parts[2])
			p.mouseMoved()
			switch {
			case cb&64 != 0:
				// SGR: 64=上滚 65=下滚 66=左滚 67=右滚
				dir := 0
				if cb != 64 {
					dir = int(cb - 64)
				}
				if dir > 3 {
					dir = 0
				}
				p.sink.wheel(dir, x, y)
			case final == 'm':
				p.sink.mouseBtn((cb&3)+1, false, x, y)
			case cb&32 != 0:
				p.sink.mouseMove(x, y)
			default:
				if p.tapQuit() {
					p.fireQuit()
					return
				}
				if p.onTap != nil && p.onTap(x, y) {
					return
				}
				p.sink.mouseBtn((cb&3)+1, true, x, y)
			}
		}
		return
	}
	if s == "" && final == 'M' {
		b1, e1 := p.src.readByte()
		b2, e2 := p.src.readByte()
		b3, e3 := p.src.readByte()
		if e1 != nil || e2 != nil || e3 != nil {
			return
		}
		cb := int(b1) - 32
		p.mouseMoved()
		if p.tapQuit() {
			p.fireQuit()
			return
		}
		x, y := int(b2)-32, int(b3)-32
		if p.onTap != nil && p.onTap(x, y) {
			return
		}
		p.sink.mouseBtn((cb&3)+1, true, x, y)
		return
	}
	// xterm 会把修饰键放在末位参数里：CSI 1;5A = Ctrl+Up，CSI 3;5~ = Ctrl+Delete。
	// 以前这个参数被直接丢掉，于是 Ctrl+方向键、Ctrl+Tab 全变成普通键（游戏里没法开枪/斜跑）。
	ctrl, alt, shift := false, false, false
	if i := strings.LastIndexByte(s, ';'); i >= 0 {
		ctrl, alt, shift = parseMod(s[i+1:])
	}
	switch final {
	case 'A':
		p.emitKey(ksUp, ctrl, alt, shift)
	case 'B':
		p.emitKey(ksDown, ctrl, alt, shift)
	case 'C':
		p.emitKey(ksRight, ctrl, alt, shift)
	case 'D':
		p.emitKey(ksLeft, ctrl, alt, shift)
	case 'H':
		p.emitKey(ksHome, ctrl, alt, shift)
	case 'F':
		p.emitKey(ksEnd, ctrl, alt, shift)
	case 'Z':
		p.emitKey(ksTab, ctrl, alt, true) // Shift+Tab 的 shift 来自按键本身
	case 'P':
		p.emitKey(ksF1, ctrl, alt, shift)
	case 'Q':
		p.emitKey(ksF1+1, ctrl, alt, shift)
	case 'R':
		p.emitKey(ksF1+2, ctrl, alt, shift)
	case 'S':
		p.emitKey(ksF1+3, ctrl, alt, shift)
	case '~':
		first := s
		if i := strings.IndexByte(first, ';'); i >= 0 {
			first = first[:i]
		}
		n, _ := strconv.Atoi(first)
		p.tildeKey(n, ctrl, alt, shift)
	}
}

func (p *inputPump) tildeKey(n int, ctrl, alt, shift bool) {
	switch n {
	case 1, 7:
		p.emitKey(ksHome, ctrl, alt, shift)
	case 2:
		p.emitKey(ksInsert, ctrl, alt, shift)
	case 3:
		p.emitKey(ksDelete, ctrl, alt, shift)
	case 4, 8:
		p.emitKey(ksEnd, ctrl, alt, shift)
	case 5:
		p.emitKey(ksPageUp, ctrl, alt, shift)
	case 6:
		p.emitKey(ksPageDown, ctrl, alt, shift)
	case 15:
		p.emitKey(ksF1+4, ctrl, alt, shift)
	case 17:
		p.emitKey(ksF1+5, ctrl, alt, shift)
	case 18:
		p.emitKey(ksF1+6, ctrl, alt, shift)
	case 19:
		p.emitKey(ksF1+7, ctrl, alt, shift)
	case 20:
		p.emitKey(ksF1+8, ctrl, alt, shift)
	case 21:
		p.emitKey(ksF1+9, ctrl, alt, shift)
	case 23:
		p.emitKey(ksF1+10, ctrl, alt, shift)
	case 24:
		p.emitKey(ksF1+11, ctrl, alt, shift)
	}
}
