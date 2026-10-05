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

	// 缩放/切分辨率时要不要真的把终端改成对应尺寸。以前只能改画面、终端不动，
	// 于是放大后字还是那么小；像迷宫游戏那样把 TIOCSWINSZ 打给终端，
	// 格子数变了、每个格子的物理像素跟着变大变小，才是真缩放。
	resizeTerm bool
	onSize     func(cols, rows int)
	rc, rr     int // 终端原本的尺寸：按 0 回到它（缩放后 tc 已经是缩放后的了）
}

func newViewCtl() *viewCtl {
	return &viewCtl{zoom: 1, tc: 80, tr: 24, fitRatio: true}
}

// setRestore 记下启动时的终端尺寸，按 0 用它恢复。
func (v *viewCtl) setRestore(cols, rows int) {
	v.mu.Lock()
	v.rc, v.rr = cols, rows
	v.mu.Unlock()
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
	// 注意：改完尺寸必须先解锁再回调 onSize。回调里会 syncTerm（要拿同一把锁），
	// 在锁里面回调就是自己等自己 —— 第一次缩放之后所有按键都没反应了。
	v.mu.Lock()
	if v.cols > 0 || v.rows > 0 {
		// 手动分辨率下缩放改的是预设尺寸本身
		v.cols = int(float64(v.cols) * (1 + delta))
		v.rows = int(float64(v.rows) * (1 + delta))
		v.note = fmt.Sprintf("分辨率 %dx%d", v.cols, v.rows)
		v.mu.Unlock()
		v.push()
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
	cols, rows := v.termCols(), v.termRows()
	v.note = fmt.Sprintf("缩放 %.0f%%（画面 %d 列，终端 %dx%d）", v.zoom*100, int(float64(v.tc)*v.zoom), cols, rows)
	v.mu.Unlock()
	v.pushSize(cols, rows)
	return v.note
}

func (v *viewCtl) preset(i int) string {
	if i < 0 || i >= len(viewPresets) {
		return ""
	}
	p := viewPresets[i]
	v.mu.Lock()
	v.cols, v.rows, v.manual = p.cols, p.rows, true
	cols, rows := v.termCols(), v.termRows()
	v.note = fmt.Sprintf("分辨率 %dx%d（第 %d 档，共 %d 档）", p.cols, p.rows, i+1, len(viewPresets))
	v.mu.Unlock()
	v.pushSize(cols, rows)
	return v.note
}

// termCols/termRows 是「终端应该变成多大」：画面格子 + 底部键盘条。
func (v *viewCtl) termCols() int {
	c := v.cols
	if c <= 0 {
		c = int(float64(v.tc) * v.zoom)
	}
	if c < 20 {
		c = 20
	}
	if c > 400 {
		c = 400
	}
	return c
}

func (v *viewCtl) termRows() int {
	r := v.rows
	if r <= 0 {
		r = v.tr - v.bar
		r = int(float64(r) * v.zoom)
	}
	if r+v.bar < 8 {
		r = 8 - v.bar
	}
	if r+v.bar > 140 {
		r = 140 - v.bar
	}
	return r + v.bar
}

// pushSize 在锁外把新尺寸交给外部（真正 resize 终端）。
func (v *viewCtl) pushSize(cols, rows int) {
	v.mu.Lock()
	on, cb := v.resizeTerm, v.onSize
	v.mu.Unlock()
	if on && cb != nil {
		cb(cols, rows)
	}
}

func (v *viewCtl) push() {
	v.mu.Lock()
	cols, rows := v.termCols(), v.termRows()
	v.mu.Unlock()
	v.pushSize(cols, rows)
}

func (v *viewCtl) auto() string {
	v.mu.Lock()
	cols, rows := v.tc, v.tr
	if v.rc > 0 && v.rr > 0 {
		cols, rows = v.rc, v.rr
	}
	v.manual = false
	v.zoom = 1
	v.cols, v.rows = 0, 0
	v.note = fmt.Sprintf("跟随终端 %dx%d（自适应）", cols, rows)
	v.mu.Unlock()
	v.pushSize(cols, rows)
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
