package main

import (
	"errors"
	"testing"
	"time"
	"unicode/utf8"
)

// 手机上字母/数字/标点靠输入法，Esc/Tab/方向键等靠 Termux 扩展键行。
// 这一组测试把「全键盘」逐个键钉死：任何一个键解析错了，游戏里就是某个动作失灵。

func TestFullAlphabet(t *testing.T) {
	// 全部小写字母
	for c := byte('a'); c <= 'z'; c++ {
		s := pumpBytes(t, []byte{c})
		if len(s.keys) != 1 {
			t.Fatalf("%c 按键数=%d 期望 1", c, len(s.keys))
		}
		if s.keys[0].sym != uint32(c) || s.keys[0].ctrl || s.keys[0].alt || s.keys[0].shift {
			t.Fatalf("%c 解析成 %+v", c, s.keys[0])
		}
	}
	// 全部大写字母（必须带 Shift，否则游戏里是反的）
	for c := byte('A'); c <= 'Z'; c++ {
		s := pumpBytes(t, []byte{c})
		if len(s.keys) != 1 {
			t.Fatalf("%c 按键数=%d 期望 1", c, len(s.keys))
		}
		k := s.keys[0]
		if k.sym != uint32(c) {
			t.Fatalf("%c keysym=%#x 期望 %#x", c, k.sym, uint32(c))
		}
	}
	// 数字
	for c := byte('0'); c <= '9'; c++ {
		s := pumpBytes(t, []byte{c})
		if len(s.keys) != 1 || s.keys[0].sym != uint32(c) {
			t.Fatalf("数字 %c 解析错误: %+v", c, s.keys)
		}
	}
}

// 输入法上能打出来的所有 ASCII 标点
func TestFullPunctuation(t *testing.T) {
	const punct = " !\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
	for i := 0; i < len(punct); i++ {
		c := punct[i]
		if c == ' ' {
			continue // 空格是单独的测试
		}
		s := pumpBytes(t, []byte{c})
		if len(s.keys) != 1 || s.keys[0].sym != uint32(c) {
			t.Fatalf("标点 %q(%#x) 解析成 %+v", c, c, s.keys)
		}
	}
	s := pumpBytes(t, []byte(" "))
	if len(s.keys) != 1 || s.keys[0].sym != ' ' || s.keys[0].ctrl {
		t.Fatalf("空格解析成 %+v", s.keys)
	}
}

// Termux 扩展键行实际发出来的转义序列
func TestTermuxExtraKeySequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want uint32
	}{
		{"上箭头", "\x1b[A", ksUp},
		{"下箭头", "\x1b[B", ksDown},
		{"右箭头", "\x1b[C", ksRight},
		{"左箭头", "\x1b[D", ksLeft},
		{"应用模式上", "\x1bOA", ksUp},
		{"应用模式左", "\x1bOD", ksLeft},
		{"Home", "\x1b[1~", ksHome},
		{"End", "\x1b[4~", ksEnd},
		{"Insert", "\x1b[2~", ksInsert},
		{"Delete", "\x1b[3~", ksDelete},
		{"PgUp", "\x1b[5~", ksPageUp},
		{"PgDn", "\x1b[6~", ksPageDown},
		{"F1", "\x1bOP", ksF1},
		{"F2", "\x1bOQ", ksF1 + 1},
		{"F3", "\x1bOR", ksF1 + 2},
		{"F4", "\x1bOS", ksF1 + 3},
		{"F5", "\x1b[15~", ksF1 + 4},
		{"F6", "\x1b[17~", ksF1 + 5},
		{"F7", "\x1b[18~", ksF1 + 6},
		{"F8", "\x1b[19~", ksF1 + 7},
		{"F9", "\x1b[20~", ksF1 + 8},
		{"F10", "\x1b[21~", ksF1 + 9},
		{"F11", "\x1b[23~", ksF1 + 10},
		{"F12", "\x1b[24~", ksF1 + 11},
		{"BackTab(Shift+Tab)", "\x1b[Z", ksTab},
	}
	for _, c := range cases {
		s := pumpBytes(t, []byte(c.in))
		found := false
		for _, k := range s.keys {
			if k.sym == c.want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s (%q) 没有解析出 keysym %#x，得到 %+v", c.name, c.in, c.want, s.keys)
		}
	}
}

// 扩展键行的组合键（Termux 默认就是发 CSI 1;5A 这种）
func TestTermuxExtraKeyCombos(t *testing.T) {
	cases := []struct {
		in               string
		sym              uint32
		ctrl, alt, shift bool
	}{
		{"\x1b[1;5A", ksUp, true, false, false},     // Ctrl+Up
		{"\x1b[1;5D", ksLeft, true, false, false},   // Ctrl+左（游戏斜跑）
		{"\x1b[1;3D", ksLeft, false, true, false},   // Alt+左
		{"\x1b[1;2D", ksLeft, false, false, true},   // Shift+左
		{"\x1b[3;5~", ksDelete, true, false, false}, // Ctrl+Delete
		{"\x1b[5;5~", ksPageUp, true, false, false}, // Ctrl+PgUp
		{"\x1b[1;5C", ksRight, true, false, false},  // Ctrl+右
	}
	for _, c := range cases {
		s := pumpBytes(t, []byte(c.in))
		if len(s.keys) == 0 {
			t.Errorf("%q 没有产生按键", c.in)
			continue
		}
		k := s.keys[len(s.keys)-1]
		if k.sym != c.sym || k.ctrl != c.ctrl || k.alt != c.alt || k.shift != c.shift {
			t.Errorf("%q 解析成 %+v，期望 sym=%#x ctrl=%v alt=%v shift=%v",
				c.in, k, c.sym, c.ctrl, c.alt, c.shift)
		}
	}
}

// 终端控制字节：Ctrl+字母 走 0x01..0x1a，必须还原成 Ctrl+字母
func TestCtrlByteRoundTrip(t *testing.T) {
	// 这些字节终端有专门约定，不能当普通 Ctrl+字母
	special := map[byte]uint32{
		0x08: ksBackSpace, // Ctrl+H = 退格
		0x09: ksTab,       // Ctrl+I = Tab
		0x0a: ksReturn,    // Ctrl+J = 回车
		0x0d: ksReturn,    // Ctrl+M = 回车
	}
	for c := byte(1); c <= 26; c++ {
		s := pumpBytes(t, []byte{c})
		if want, ok := special[c]; ok {
			if len(s.keys) != 1 || s.keys[0].sym != want || s.keys[0].ctrl {
				t.Errorf("字节 %#x 应解析成 %s，得到 %+v", c, symName(want), s.keys)
			}
			continue
		}
		if c == 0x03 {
			if len(s.keys) != 0 {
				t.Errorf("Ctrl+C 不该发给被包裹的程序（工具自己用来退出）")
			}
			continue
		}
		if len(s.keys) != 1 {
			t.Fatalf("Ctrl+%c 按键数=%d", 'A'+c-1, len(s.keys))
		}
		k := s.keys[0]
		if !k.ctrl {
			t.Errorf("Ctrl+%c 没有 ctrl 标记: %+v", 'A'+c-1, k)
		}
		if want := 'a' + (c - 1); k.sym != uint32(want) {
			t.Errorf("Ctrl+%c keysym=%q 期望 %q", 'A'+c-1, rune(k.sym), rune(want))
		}
	}
}

// -map / 键位表把手机上的键换成程序真正认的键
func TestRemapCoversPhoneKeys(t *testing.T) {
	m := map[uint32]keyAction{}
	for _, line := range []string{
		"space = ctrl",    // 开火：空格当 Ctrl（Wolf3D 就是 Ctrl 开火）
		"z = alt",         // 手机上按不出 Alt
		"c = esc",         // 菜单键
		"q = ctrl-alt-f2", // 手机上没有 F 键
		"- = esc",         // 标点也走键位表
	} {
		km, ok := parseKeymapLine(line)
		if !ok {
			t.Fatalf("看不懂键位 %q", line)
		}
		for k, v := range km {
			m[k] = v
		}
	}
	cases := []struct {
		in        string
		sym       uint32
		ctrl, alt bool
	}{
		{" ", ksControlL, false, false},
		// "alt"/"ctrl" 这种写法是直接注入修饰键本身（和 -ctrl 同理），不是组合键
		{"z", ksAltL, false, false},
		{"c", ksEscape, false, false},
		{"q", ksF1 + 1, true, true},
		{"-", ksEscape, false, false},
		{"\x1b[1;5A", ksUp, true, false}, // 方向键没在键位表里，原样带 Ctrl
	}
	for _, c := range cases {
		s := &recSink{}
		p := &inputPump{src: &sliceSource{data: []byte(c.in)}, sink: s, remap: m}
		p.run()
		if len(s.keys) == 0 {
			t.Errorf("%q 没有产生按键", c.in)
			continue
		}
		k := s.keys[len(s.keys)-1]
		if k.sym != c.sym || k.ctrl != c.ctrl || k.alt != c.alt {
			t.Errorf("%q 映射成 %+v，期望 sym=%#x ctrl=%v alt=%v", c.in, k, c.sym, c.ctrl, c.alt)
		}
	}
}

// 虚拟键盘条：锁定 Ctrl 后点方向键 = Ctrl+方向
func TestKeyBarLatchAndFire(t *testing.T) {
	kb := newKeyBar(2, ksControlL)
	kb.layout(50)
	if len(kb.items) == 0 {
		t.Fatal("键盘条没有排布任何键")
	}
	// 找 CTRL / 上 / FIRE
	var ctrl, up, fire *kbItem
	for i := range kb.items {
		switch kb.items[i].label {
		case "CTRL":
			ctrl = &kb.items[i]
		case "↑":
			up = &kb.items[i]
		case "FIRE":
			fire = &kb.items[i]
		}
	}
	if ctrl == nil || up == nil || fire == nil {
		t.Fatalf("缺少必要按键: ctrl=%v up=%v fire=%v", ctrl, up, fire)
	}
	imgRows := 20
	// 点在键盘条上（画面之外）
	it := kb.hit(up.col, imgRows+1+up.row, imgRows)
	if it == nil || it.label != "↑" {
		t.Fatalf("命中测试失败: %+v", it)
	}
	// 画面内的点击不能命中键盘条
	if kb.hit(up.col, 5, imgRows) != nil {
		t.Fatal("画面内的点击不该命中键盘条")
	}
	// 锁定 Ctrl
	if !kb.toggle(ctrl) {
		t.Fatal("第一次点 CTRL 应该锁定")
	}
	c, a, s := kb.mods()
	if !c || a || s {
		t.Fatalf("锁定状态错误 ctrl=%v alt=%v shift=%v", c, a, s)
	}
	if kb.toggle(ctrl) {
		t.Fatal("第二次点 CTRL 应该解锁")
	}
	if c, _, _ := kb.mods(); c {
		t.Fatal("解锁后 ctrl 仍为锁定")
	}
	if kb.fireSym != ksControlL {
		t.Fatalf("默认开火键=%#x 期望 ctrl", kb.fireSym)
	}
	// 渲染：每行宽度=col 数，且锁定时有高亮
	lines := kb.render(50)
	if len(lines) != 2 {
		t.Fatalf("渲染行数=%d", len(lines))
	}
	kb.toggle(ctrl)
	if lines := kb.render(50); len(lines) != 2 || !containsStr(lines[0], "\x1b[43m") {
		t.Fatal("锁定 CTRL 时应该有黄底高亮")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Android 输入法/Termux 扩展键有时把 "ESC [ A" 分成两次送达。
// 等待窗口太小的话方向键会变成 Escape + "[" + "a"，游戏里就是方向键失灵。
// slowSource 把一个字节序列切成多段，每段之间 readByte 会返回 errNotReady。
type slowSource struct {
	chunks [][]byte
	i, off int
}

var (
	errEOF      = errors.New("EOF")
	errNotReady = errors.New("not ready")
)

func (s *slowSource) readByte() (byte, error) {
	for s.i < len(s.chunks) {
		c := s.chunks[s.i]
		if s.off < len(c) {
			b := c[s.off]
			s.off++
			return b, nil
		}
		// 这一段读完了，模拟“终端还没把下一段送上来”
		if s.off == 0 && s.i+1 < len(s.chunks) {
			return 0, errNotReady
		}
		s.i++
		s.off = 0
	}
	return 0, errEOF
}

// readable：后面还有下一段就认为“等得到”（真实等待时长由 -esc-wait 控制）
func (s *slowSource) readable(time.Duration) bool { return s.i+1 < len(s.chunks) }

func TestEscapeSequenceSplitDelivery(t *testing.T) {
	// ESC、[、A 分三次送达，模拟输入法卡顿
	src := &slowSource{chunks: [][]byte{{0x1b}, {'['}, {'A'}}}
	s := &recSink{}
	p := &inputPump{src: src, sink: s, escWaitMS: 150}
	p.run()
	if len(s.keys) != 1 || s.keys[0].sym != ksUp {
		t.Fatalf("分段送达的 ESC[A 应解析成 ↑，得到 %+v", s.keys)
	}
	// 真正的单独 ESC（等不到后续字节）仍然要发出去
	src2 := &slowSource{chunks: [][]byte{{0x1b}}}
	s2 := &recSink{}
	p2 := &inputPump{src: src2, sink: s2, escWaitMS: 150}
	p2.run()
	if len(s2.keys) != 1 || s2.keys[0].sym != ksEscape {
		t.Fatalf("单独 ESC 应发出去，得到 %+v", s2.keys)
	}
}

// 反斜杠、反引号、空格这些容易在 shell/JSON 里被吃掉，必须确认能注入
func TestTrickyPrintableKeys(t *testing.T) {
	for _, c := range []byte{'\\', '`', ' ', '~', '{', '}', '|', '?'} {
		s := pumpBytes(t, []byte{c})
		if len(s.keys) != 1 || s.keys[0].sym != uint32(c) {
			t.Errorf("键 %q(%#x) 解析成 %+v", c, c, s.keys)
		}
	}
}

func TestKeyBarRenderWideAndNarrow(t *testing.T) {
	for _, cols := range []int{20, 40, 80, 120, 200} {
		for _, rows := range []int{1, 2, 3} {
			kb := newKeyBar(rows, ksControlL)
			kb.layout(cols)
			lines := kb.render(cols)
			if len(lines) != rows {
				t.Fatalf("cols=%d rows=%d 渲染行数=%d", cols, rows, len(lines))
			}
			for i, ln := range lines {
				if w := visibleWidth(ln); w != cols {
					t.Errorf("cols=%d rows=%d 第%d行可见宽度=%d", cols, rows, i+1, w)
				}
			}
			// 每个键都必须能准确命中（排版和点击区域不能错位）
			for i := range kb.items {
				it := &kb.items[i]
				if got := kb.hit(it.col, 1+it.row, 0); got != it {
					t.Errorf("cols=%d 的 %s 命中错位", cols, it.label)
				}
				if got := kb.hit(it.col+it.span-1, 1+it.row, 0); got != it {
					t.Errorf("cols=%d 的 %s 右边界命中错位", cols, it.label)
				}
			}
		}
	}
}

// visibleWidth 统计终端上真正占多少列（跳过 CSI 转义、正确算宽字符）。
func visibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i++
			if i < len(s) && s[i] == '[' {
				i++
			}
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			if i < len(s) {
				i++ // final byte
			}
			continue
		}
		if s[i] >= 0x80 {
			_, sz := utf8.DecodeRuneInString(s[i:])
			i += sz
			w++
			continue
		}
		i++
		w++
	}
	return w
}
