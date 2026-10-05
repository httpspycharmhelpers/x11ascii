package main

import (
	"fmt"
	"os"
	"sync"
)

type viewPreset struct {
	cols int
	rows int
}

// 输入层里按数字键直接切到常用分辨率。
var viewPresets = []viewPreset{
	{60, 18}, {80, 24}, {100, 30}, {120, 36}, {160, 48}, {200, 60},
}

// viewCtl 管的是「画面占多大 / 用多少格子画」，也就是缩放和分辨率。
// 以前这些只能在启动时用 -w/-h/-region 定死了，运行中想改只能退出重来。
type viewCtl struct {
	mu       sync.Mutex
	zoom     float64 // <1 画面变大（格子少、字大）；>1 画面变小（格子多、更细腻）
	manual   bool    // true=用手动指定/缩放，不再跟随终端尺寸
	cols     int     // 手动指定的画面列数（0=按终端算）
	rows     int
	tc, tr   int  // 终端实际大小
	bar      int  // 底部键盘条占几行
	fitRatio bool // 是否保持源画面比例（false=拉伸铺满）
	note     string
}

func newViewCtl() *viewCtl {
	return &viewCtl{zoom: 1, tc: 80, tr: 24, fitRatio: true}
}

func (v *viewCtl) syncTerm(cols, rows, bar int) {
	v.mu.Lock()
	v.tc, v.tr, v.bar = cols, rows, bar
	v.mu.Unlock()
}

// img 返回画面区（不含键盘条）的格子数，以及它在终端里的居中偏移。
func (v *viewCtl) img() (cols, rows, offX, offY int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	tc, tr := v.tc, v.tr
	availRows := tr - v.bar
	if availRows < 4 {
		availRows = 4
	}
	if v.manual {
		cols, rows = v.cols, v.rows
		if cols == 0 {
			cols = int(float64(tc) * v.zoom)
		}
		if rows == 0 {
			rows = int(float64(availRows) * v.zoom)
		}
	} else {
		cols = tc
		rows = availRows
	}
	if cols < 8 {
		cols = 8
	}
	if rows < 4 {
		rows = 4
	}
	// 比终端还大就居中裁掉超出部分：放大画面时不用先把终端字号调小
	offX = (tc - cols) / 2
	offY = (tr - v.bar - rows) / 2
	if offX < 0 {
		offX = 0
	}
	if offY < 0 {
		offY = 0
	}
	return
}

func (v *viewCtl) zoomed(delta float64) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.cols > 0 || v.rows > 0 {
		// 手动分辨率下缩放改的是预设尺寸本身
		v.cols = int(float64(v.cols) * (1 + delta))
		v.rows = int(float64(v.rows) * (1 + delta))
		v.note = fmt.Sprintf("分辨率 %dx%d", v.cols, v.rows)
		return v.note
	}
	nz := v.zoom * (1 + delta)
	if nz < 0.35 {
		nz = 0.35
	}
	if nz > 3 {
		nz = 3
	}
	v.zoom = nz
	v.manual = true
	v.note = fmt.Sprintf("缩放 %.0f%%（画面占 %d 列）", v.zoom*100, int(float64(v.tc)*v.zoom))
	return v.note
}

func (v *viewCtl) preset(i int) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if i < 0 || i >= len(viewPresets) {
		return ""
	}
	p := viewPresets[i]
	v.cols, v.rows, v.manual = p.cols, p.rows, true
	v.note = fmt.Sprintf("分辨率 %dx%d（第 %d 档，共 %d 档）", p.cols, p.rows, i+1, len(viewPresets))
	return v.note
}

func (v *viewCtl) auto() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.manual = false
	v.zoom = 1
	v.cols, v.rows = 0, 0
	v.note = "跟随终端大小（自适应）"
	return v.note
}

func (v *viewCtl) toggleFit() string {
	v.mu.Lock()
	v.fitRatio = !v.fitRatio
	ratio := v.fitRatio
	v.mu.Unlock()
	if ratio {
		return "保持画面比例（不拉伸）"
	}
	return "拉伸铺满整个画面区"
}

func (v *viewCtl) manualSize() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.manual
}

func (v *viewCtl) fitOn() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.fitRatio
}

func (v *viewCtl) takeNote() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	n := v.note
	v.note = ""
	return n
}

func (v *viewCtl) status() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.manual {
		return fmt.Sprintf("画面 %dx%d 格子（缩放 %.0f%%）", v.cols, v.rows, v.zoom*100)
	}
	return fmt.Sprintf("画面 %dx%d 格子（跟随终端）", v.tc, v.tr-v.bar)
}

func (v *viewCtl) say(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
}
