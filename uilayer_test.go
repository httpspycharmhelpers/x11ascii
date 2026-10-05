package main

import "testing"

func TestViewCtlFollowsTerminal(t *testing.T) {
	v := newViewCtl()
	v.syncTerm(100, 30, 2)
	cols, rows, offX, offY := v.img()
	if cols != 100 || rows != 28 {
		t.Fatalf("跟随终端时应为 100x28，实际 %dx%d", cols, rows)
	}
	if offX != 0 || offY != 0 {
		t.Fatalf("等大时不该有偏移，实际 %d,%d", offX, offY)
	}
}

func TestViewCtlZoomCenters(t *testing.T) {
	v := newViewCtl()
	v.syncTerm(100, 30, 2)
	v.zoomed(-0.5) // 放大 → 0.5 倍格子 → 画面居中
	cols, rows, offX, offY := v.img()
	if cols != 50 || rows != 14 {
		t.Fatalf("放大一档后应为 50x14，实际 %dx%d", cols, rows)
	}
	if offX != 25 || offY != 7 { // (28-14)/2 = 7
		t.Fatalf("应居中：offX=%d offY=%d", offX, offY)
	}
}

func TestViewCtlZoomOutCrops(t *testing.T) {
	v := newViewCtl()
	v.syncTerm(80, 24, 0)
	for i := 0; i < 8; i++ {
		v.zoomed(0.25) // 缩小 → 格子变多，可能超出终端
	}
	cols, rows, offX, offY := v.img()
	if cols <= 80 || rows <= 24 {
		t.Fatalf("缩小后格子应变多，实际 %dx%d", cols, rows)
	}
	if offX != 0 || offY != 0 {
		t.Fatalf("超出终端时偏移应为 0（居中裁切），实际 %d,%d", offX, offY)
	}
}

func TestViewCtlPresetsAndAuto(t *testing.T) {
	v := newViewCtl()
	v.syncTerm(100, 30, 0)
	if s := v.preset(2); s == "" {
		t.Fatal("预设应返回提示文案")
	}
	cols, rows, _, _ := v.img()
	if cols != 100 || rows != 30 {
		t.Fatalf("第 3 档预设应为 100x30，实际 %dx%d", cols, rows)
	}
	v.auto()
	cols, rows, _, _ = v.img()
	if cols != 100 || rows != 30 {
		t.Fatalf("恢复自适应后应跟随终端 100x30，实际 %dx%d", cols, rows)
	}
	if v.manualSize() {
		t.Fatal("auto 之后不应还是手动尺寸")
	}
}

func TestViewCtlFitToggle(t *testing.T) {
	v := newViewCtl()
	if !v.fitOn() {
		t.Fatal("默认应保持比例")
	}
	v.toggleFit()
	if v.fitOn() {
		t.Fatal("切一次应变成拉伸")
	}
}

func TestTextInputCaretEditing(t *testing.T) {
	ti := newTextInput()
	ti.open = true
	for _, r := range "hello" {
		ti.insertRune(r)
	}
	if ti.text() != "hello" {
		t.Fatalf("插入后应为 hello，实际 %q", ti.text())
	}
	// 光标移到最前，删一个字 → ello
	ti.handle(ksHome, false, false, false)
	ti.handle(ksDelete, false, false, false)
	if ti.text() != "ello" {
		t.Fatalf("Home+Delete 应得 ello，实际 %q", ti.text())
	}
	// 左右方向键移动光标后插入
	ti.handle(ksHome, false, false, false)
	ti.handle(ksRight, false, false, false)
	ti.insertRune('X')
	if ti.text() != "eXllo" {
		t.Fatalf("方向键移动光标后插入应得 eXllo，实际 %q", ti.text())
	}
	// Backspace 删掉光标前一个（此时光标在 X 后面，删的就是 X）
	ti.handle(ksBackSpace, false, false, false)
	if ti.text() != "ello" {
		t.Fatalf("Backspace 应得 ello，实际 %q", ti.text())
	}
	// End 之后追加
	ti.handle(ksEnd, false, false, false)
	ti.insertRune('!')
	if ti.text() != "ello!" {
		t.Fatalf("End 后追加应得 ello!，实际 %q", ti.text())
	}
}

func TestTextInputHistory(t *testing.T) {
	ti := newTextInput()
	ti.open = true
	ti.hpos = 0
	ti.hist = []string{"first", "second"}
	ti.hpos = len(ti.hist)
	ti.insert("draft")
	s, _ := ti.commit()
	if s != "draft" {
		t.Fatalf("回车应返回 draft，实际 %q", s)
	}
	if ti.text() != "" {
		t.Fatalf("回车后应清空，实际 %q", ti.text())
	}
	ti.handle(ksUp, false, false, false)
	if ti.text() != "draft" {
		t.Fatalf("上箭头应调出历史 draft，实际 %q", ti.text())
	}
	ti.handle(ksUp, false, false, false)
	if ti.text() != "second" {
		t.Fatalf("再上应是 second，实际 %q", ti.text())
	}
}

func TestTextInputWordKill(t *testing.T) {
	ti := newTextInput()
	ti.open = true
	ti.insert("hello world foo")
	ti.handle(ksEnd, false, false, false)
	ti.handle('w', true, false, false)
	if ti.text() != "hello world " {
		t.Fatalf("Ctrl+W 应删掉最后一个词，实际 %q", ti.text())
	}
	ti.handle('u', true, false, false)
	if ti.text() != "" {
		t.Fatalf("Ctrl+U 应清空，实际 %q", ti.text())
	}
}

func TestUIKeyGatesTyping(t *testing.T) {
	u := newUI()
	var sent []string
	u.send = func(s string) bool { sent = append(sent, s); return true }
	u.cp = func(s string) bool { sent = append(sent, "clip:"+s); return true }

	// 输入层没开：` 不该发给 X，而是打开输入层
	if !u.key('`', false, false, false) {
		t.Fatal("` 应被界面层吃掉")
	}
	if !u.ti.isOpen() {
		t.Fatal("` 应打开输入层")
	}
	for _, r := range "abc" {
		if !u.key(uint32(r), false, false, false) {
			t.Fatalf("输入层打开时 %c 应被吃掉", r)
		}
	}
	if u.ti.text() != "abc" {
		t.Fatalf("输入层应攒下 abc，实际 %q", u.ti.text())
	}
	// 回车 = 逐字发送
	if !u.key(ksReturn, false, false, false) {
		t.Fatal("回车应被吃掉")
	}
	if len(sent) != 1 || sent[0] != "abc" {
		t.Fatalf("应发送 abc，实际 %v", sent)
	}
	// Ctrl+回车 = 走剪贴板
	u.ti.insert("xy")
	u.key(ksReturn, true, false, false)
	if len(sent) != 2 || sent[1] != "clip:xy" {
		t.Fatalf("Ctrl+回车应走剪贴板，实际 %v", sent)
	}
	// Esc 退出输入层
	u.key(ksEscape, false, false, false)
	if u.ti.isOpen() {
		t.Fatal("Esc 应关闭输入层")
	}
}

func TestUIKeyHotkeysVsGameKeys(t *testing.T) {
	u := newUI()
	// 输入层没开时，只有热键被吃掉，普通按键要留给游戏
	if u.key('a', false, false, false) {
		t.Fatal("普通键 a 不该被吃掉（要发给游戏）")
	}
	if !u.key('+', false, false, false) {
		t.Fatal("+ 应被吃掉（放大）")
	}
	if !u.key('-', false, false, false) {
		t.Fatal("- 应被吃掉（缩小）")
	}
	if !u.key('1', false, false, false) {
		t.Fatal("1 应被吃掉（预设分辨率）")
	}
	if !u.key('0', false, false, false) {
		t.Fatal("0 应被吃掉（自适应）")
	}
	if !u.key('f', false, false, false) {
		t.Fatal("f 应被吃掉（切换拉伸）")
	}
	// 输入层打开后，普通键归输入层
	u.key('`', false, false, false)
	if !u.key('a', false, false, false) {
		t.Fatal("输入层打开时 a 应被吃掉")
	}
	if u.ti.text() != "a" {
		t.Fatalf("输入层应收到 a，实际 %q", u.ti.text())
	}
}

func TestSymRune(t *testing.T) {
	cases := []struct {
		sym  uint32
		want rune
		ok   bool
	}{
		{'a', 'a', true},
		{'Z', 'Z', true},
		{0x00e9, 'é', true},
		{0x01000000 | uint32('中'), '中', true},
		{ksLeft, 0, false},
		{ksF1, 0, false},
	}
	for _, c := range cases {
		got, ok := symRune(c.sym)
		if ok != c.ok || (ok && got != c.want) {
			t.Fatalf("symRune(%#x) = %q,%v 期望 %q,%v", c.sym, got, ok, c.want, c.ok)
		}
	}
}

func TestBlitCenterPadsAndCrops(t *testing.T) {
	// 小画面放进大终端：四周留黑，画面居中
	src := &Canvas{Cols: 2, Rows: 2, Pix: make([]byte, 2*2*6)}
	for i := 0; i < 6; i++ {
		src.Pix[i] = 0xff
	}
	dst := &Canvas{}
	blitCenter(src, dst, 6, 6, 2, 2)
	if dst.Cols != 6 || dst.Rows != 6 {
		t.Fatalf("目标应是 6x6，实际 %dx%d", dst.Cols, dst.Rows)
	}
	at := func(c, r int) byte { return dst.Pix[(r*dst.Cols+c)*6] }
	if at(2, 2) != 0xff {
		t.Fatalf("画面左上角应落在 (2,2)，实际该格=%d", at(2, 2))
	}
	if at(3, 3) != 0 {
		t.Fatal("未着色的格子应保持黑")
	}
	if at(0, 0) != 0 || at(5, 5) != 0 {
		t.Fatal("四周应是黑边")
	}
	// 大画面放进小终端：裁掉超出部分，不能越界写
	big := &Canvas{Cols: 8, Rows: 8, Pix: make([]byte, 8*8*6)}
	for i := range big.Pix {
		big.Pix[i] = 0x7f
	}
	dst2 := &Canvas{}
	blitCenter(big, dst2, 4, 4, 0, 0)
	if len(dst2.Pix) != 4*4*6 {
		t.Fatalf("目标缓冲应是 4*4*6，实际 %d", len(dst2.Pix))
	}
}
