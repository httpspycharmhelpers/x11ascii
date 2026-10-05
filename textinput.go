package main

import (
	"strings"
	"sync"
	"unicode"
)

// 文本输入层：以前这个工具只能「把按键原样捅给 X」，没法在终端里编辑文字，
// 所以没法打字、没法粘贴、也没法用左右方向键挪光标。
// 现在按 ` 打开这一层：自己吃掉按键并维护光标，回车才发给 X 程序。
type textInput struct {
	mu       sync.Mutex
	buf      []rune
	caret    int // 光标位置（0=行首）
	hist     []string
	hpos     int // 历史游标，len(hist)=当前输入
	open     bool
	prompt   string
	status   string
	wantClip bool // 置位表示「这层要读 X 剪贴板」，由主循环去做 X 事务后清除
}

func newTextInput() *textInput {
	return &textInput{prompt: "输入 ▸ ", hpos: 0}
}

func (t *textInput) isOpen() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.open
}

func (t *textInput) toggle() bool {
	t.mu.Lock()
	t.open = !t.open
	if t.open {
		t.status = "回车=发给 X 程序　Ctrl+回车=走剪贴板粘贴　Ctrl+X=读 X 剪贴板　Esc=回游戏"
	}
	o := t.open
	t.mu.Unlock()
	return o
}

func (t *textInput) close() {
	t.mu.Lock()
	t.open = false
	t.mu.Unlock()
}

func (t *textInput) commit() (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := string(t.buf)
	if strings.TrimSpace(s) != "" {
		t.hist = append(t.hist, s)
		if len(t.hist) > 50 {
			t.hist = t.hist[len(t.hist)-50:]
		}
	}
	t.buf = t.buf[:0]
	t.caret = 0
	t.hpos = len(t.hist)
	t.status = "已发送：" + s
	return s, true
}

func (t *textInput) setClip(s string) {
	t.mu.Lock()
	t.buf = []rune(s)
	t.caret = len(t.buf)
	t.status = "已从 X 剪贴板取回 " + itoa(len([]rune(s))) + " 字，方向键可移动光标修改"
	t.mu.Unlock()
}

func (t *textInput) wantClipGet() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.wantClip
}

func (t *textInput) clearWantClip() {
	t.mu.Lock()
	t.wantClip = false
	t.mu.Unlock()
}

func (t *textInput) setStatus(s string) {
	t.mu.Lock()
	t.status = s
	t.mu.Unlock()
}

func (t *textInput) text() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func (t *textInput) insert(s string) {
	if s == "" {
		return
	}
	rs := []rune(s)
	t.mu.Lock()
	head := append([]rune{}, t.buf[:t.caret]...)
	tail := append([]rune{}, t.buf[t.caret:]...)
	t.buf = append(head, append(rs, tail...)...)
	t.caret += len(rs)
	t.mu.Unlock()
}

// handle 吃掉一个按键，返回 true=这一层处理了（不要发给 X）。
// sym 是 X11 keysym；普通字符已经由调用方转成 rune 走 insertRune。
func (t *textInput) handle(sym uint32, ctrl, alt, shift bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	up := func() {
		t.caret--
		if t.caret < 0 {
			t.caret = 0
		}
	}
	down := func() {
		t.caret++
		if t.caret > len(t.buf) {
			t.caret = len(t.buf)
		}
	}
	back := func() {
		if t.caret > 0 {
			t.buf = append(t.buf[:t.caret-1], t.buf[t.caret:]...)
			t.caret--
		}
	}
	del := func() {
		if t.caret < len(t.buf) {
			t.buf = append(t.buf[:t.caret], t.buf[t.caret+1:]...)
		}
	}
	switch {
	case sym == ksEscape:
		t.open = false
		return true
	case sym == ksReturn:
		return false // 交给调用方：回车=发送
	case sym == ksLeft:
		up()
	case sym == ksRight:
		down()
	case sym == ksHome:
		t.caret = 0
	case sym == ksEnd:
		t.caret = len(t.buf)
	case sym == ksUp:
		if t.hpos > 0 {
			t.hpos--
			t.loadHist()
		}
	case sym == ksDown:
		if t.hpos < len(t.hist) {
			t.hpos++
			t.loadHist()
		}
	case sym == ksBackSpace:
		back()
	case sym == ksDelete:
		del()
	case ctrl && (sym == 'u' || sym == 'U'):
		t.buf = t.buf[:0]
		t.caret = 0
	case ctrl && (sym == 'w' || sym == 'W'):
		i := t.caret
		for i > 0 && unicode.IsSpace(rune(t.buf[i-1])) {
			i--
		}
		for i > 0 && !unicode.IsSpace(rune(t.buf[i-1])) {
			i--
		}
		t.buf = append(t.buf[:i], t.buf[t.caret:]...)
		t.caret = i
	case ctrl && (sym == 'k' || sym == 'K'):
		t.buf = t.buf[:t.caret]
	case ctrl && (sym == 'a' || sym == 'A'):
		t.caret = 0
	case ctrl && (sym == 'e' || sym == 'E'):
		t.caret = len(t.buf)
	case ctrl && sym == 'x':
		t.status = "读 X 剪贴板…"
		t.wantClip = true
	}
	return true
}

// loadHist 必须在已持锁时调用（handle 里用）
func (t *textInput) loadHist() {
	if t.hpos < len(t.hist) {
		t.buf = []rune(t.hist[t.hpos])
	} else {
		t.buf = t.buf[:0]
	}
	t.caret = len(t.buf)
}

func (t *textInput) insertRune(r rune) {
	t.mu.Lock()
	rs := append([]rune{}, t.buf[:t.caret]...)
	rs = append(rs, r)
	t.buf = append(rs, t.buf[t.caret:]...)
	t.caret++
	t.mu.Unlock()
}

// render 画成一行终端文本（光标用反色块表示）。
func (t *textInput) render(cols int) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	head := t.prompt
	body := string(t.buf)
	// 太长就把开头让给光标，左边显示 …
	avail := cols - len([]rune(head)) - 1
	rs := []rune(body)
	start := 0
	if len(rs) > avail && avail > 4 {
		start = t.caret - avail/2
		if start < 0 {
			start = 0
		}
		if start > len(rs)-avail {
			start = len(rs) - avail
		}
		rs = rs[start : start+avail]
	}
	caret := t.caret - start
	if caret < 0 {
		caret = 0
	}
	if caret > len(rs) {
		caret = len(rs)
	}
	var b strings.Builder
	b.WriteString("\x1b[7m" + head + "\x1b[0m")
	b.WriteString(string(rs[:caret]))
	if len(rs) > caret {
		b.WriteString("\x1b[7m" + string(rs[caret:caret+1]) + "\x1b[0m")
		b.WriteString(string(rs[caret+1:]))
	} else {
		b.WriteString("\x1b[7m \x1b[0m")
	}
	if t.status != "" {
		room := cols - len([]rune(b.String()))/1 - 1
		if room > 8 {
			s := []rune(t.status)
			if len(s) > room {
				s = s[:room]
			}
			b.WriteString("  \x1b[2m" + string(s) + "\x1b[0m")
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		return "-" + string(d)
	}
	return string(d)
}
