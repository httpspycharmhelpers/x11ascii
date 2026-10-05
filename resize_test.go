package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriterClearRedraws(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf, modeTrue)
	c := &Canvas{Cols: 2, Rows: 1, Pix: make([]byte, 12)}
	fill(c, [3]byte{255, 0, 0}, [3]byte{0, 0, 255})
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	n := buf.Len()
	w.Clear()
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String()[n:], "\x1b[2J") {
		t.Fatal("Clear 后应清屏重画")
	}
}

// 外部改了终端尺寸（横屏/软键盘）之后必须重新跟随，否则两侧留黑边。
func TestViewCtlFollowTerm(t *testing.T) {
	v := newViewCtl()
	var got [2]int
	v.onSize = func(c, r int) { got = [2]int{c, r} }
	v.resizeTerm = true
	v.setRestore(80, 24)
	v.syncTerm(120, 40, 2)
	v.preset(4) // 对应按键 5：画面 160x48 → 终端 160x50
	if !v.manualSize() {
		t.Fatal("preset 之后应是手动尺寸")
	}
	if got[0] != 160 || got[1] != 50 {
		t.Fatalf("preset 没改终端尺寸: %dx%d", got[0], got[1])
	}
	// 终端被外部改成 120x30（横屏）后应丢掉手动尺寸、按新尺寸走
	v.followTerm()
	if v.manualSize() {
		t.Fatal("followTerm 之后不应还是手动尺寸")
	}
	if v.zoom != 1 {
		t.Fatalf("followTerm 之后缩放应回到 1，实际 %v", v.zoom)
	}
	v.syncTerm(120, 40, 2)
	c, r, _, _ := v.img()
	if c != 120 || r != 38 {
		t.Fatalf("应按新终端 120x30 铺满（扣键盘条 2 行）: 实际 %dx%d", c, r)
	}
}
