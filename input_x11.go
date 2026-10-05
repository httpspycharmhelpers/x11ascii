package main

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgb/xtest"
)

// 设 X11ASCII_DEBUG_INPUT=1 时把注入的按键/鼠标打到 stderr，排查"点了没反应"用。
func debugInput(format string, a ...any) {
	if os.Getenv("X11ASCII_DEBUG_INPUT") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[input] "+format+"\n", a...)
}

type keySlot struct {
	keycode byte
	shift   bool
}

// initInput 启用 XTest 并读取服务器键盘映射（keysym -> keycode/是否需要 Shift）。
func (s *x11Source) initInput() error {
	// 输入注入单独开一条连接：抓画面的连接不能用来注入指针（见 x11Source.inConn 注释）。
	// 拿不到独立连接时退回主连接，至少键盘还能用。
	s.inConn = s.conn
	if c, _, err := dialX11(s.disp); err == nil {
		if err := xtest.Init(c); err == nil {
			s.inConn = c
		} else {
			c.Close()
		}
	}
	if err := xtest.Init(s.inConn); err != nil {
		return err
	}
	xtest.GrabControl(s.inConn, false)

	setup := xproto.Setup(s.inConn)
	min, max := setup.MinKeycode, setup.MaxKeycode
	count := int(max) - int(min) + 1
	if count <= 0 {
		return nil
	}
	km, err := xproto.GetKeyboardMapping(s.inConn, min, byte(count)).Reply()
	if err != nil {
		return err
	}
	per := int(km.KeysymsPerKeycode)
	if per < 1 {
		per = 1
	}
	s.symMap = make(map[uint32]keySlot, 256)
	for i := 0; i < count; i++ {
		for j := 0; j < per && j < 2; j++ {
			sym := uint32(km.Keysyms[i*per+j])
			if sym == 0 {
				continue
			}
			if _, ok := s.symMap[sym]; ok {
				continue
			}
			s.symMap[sym] = keySlot{keycode: byte(int(min) + i), shift: j == 1}
		}
	}
	if mm, err := xproto.GetModifierMapping(s.conn).Reply(); err == nil {
		perM := int(mm.KeycodesPerModifier)
		for m := 0; m < 8 && perM > 0 && m*perM < len(mm.Keycodes); m++ {
			s.mod[m] = byte(mm.Keycodes[m*perM])
		}
	}
	atomic.StoreInt32(&s.aw, int32(s.w))
	atomic.StoreInt32(&s.ah, int32(s.h))
	s.FocusBiggest()
	return nil
}

func (s *x11Source) setTermSize(cols, rows int) {
	atomic.StoreInt32(&s.cols, int32(cols))
	atomic.StoreInt32(&s.rows, int32(rows))
}

func (s *x11Source) fake(t, detail byte, rx, ry int16) {
	c := s.inConn
	if c == nil {
		c = s.conn
	}
	xtest.FakeInput(c, t, detail, xproto.TimeCurrentTime, s.win, rx, ry, 0)
}

func (s *x11Source) key(sym uint32, ctrl, alt, shift bool) {
	slot, ok := s.symMap[sym]
	if !ok {
		// 静默丢掉最难受：手机上按了没反应又不知道是哪一步坏了
		debugInput("按键丢失 sym=%#x (%s)：X 键盘映射里没有这个键", sym, symName(sym))
		return
	}
	if slot.shift {
		shift = true
	}
	sk, ck, ak := s.mod[0], s.mod[2], s.mod[3]
	debugInput("按键 sym=0x%x -> keycode=%d shift=%v ctrl=%v alt=%v", sym, slot.keycode, slot.shift, ctrl, alt)
	down := func(k byte) {
		if k != 0 {
			s.fake(xproto.KeyPress, k, 0, 0)
		}
	}
	up := func(k byte) {
		if k != 0 {
			s.fake(xproto.KeyRelease, k, 0, 0)
		}
	}
	if shift {
		down(sk)
	}
	if ctrl {
		down(ck)
	}
	if alt {
		down(ak)
	}
	down(slot.keycode)
	hold := s.holdMS
	// 单独注入的修饰键要按久一点：游戏是「按住 Ctrl 持续射击」，60ms 的点一下没用
	if slot.keycode == s.mod[2] || slot.keycode == s.mod[3] || slot.keycode == s.mod[0] {
		if s.modHoldMS > hold {
			hold = s.modHoldMS
		}
	}
	if hold > 0 {
		s.conn.Sync()
		time.Sleep(time.Duration(hold) * time.Millisecond)
	}
	up(slot.keycode)
	if alt {
		up(ak)
	}
	if ctrl {
		up(ck)
	}
	if shift {
		up(sk)
	}
}

// cellToPix 把终端单元格映射到 X 像素坐标。
// 必须按 convert 用的同一套 letterbox 视口换算，否则 -fit 留黑边时点击会整体偏移/缩放不准
// （浏览器点链接、按钮全靠这个）。
func (s *x11Source) cellToPix(col, row int) (int16, int16) {
	cols := int(atomic.LoadInt32(&s.cols))
	rows := int(atomic.LoadInt32(&s.rows))
	w := int(atomic.LoadInt32(&s.aw))
	h := int(atomic.LoadInt32(&s.ah))
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if w <= 0 {
		w = s.w
	}
	if h <= 0 {
		h = s.h
	}
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}
	if col > cols {
		col = cols
	}
	if row > rows {
		row = rows
	}

	// 视口（网格坐标）→ 源像素坐标。网格横向 1 格=1 像素，纵向 1 格=半个字符高度，
	// 所以纵向用 rows*2。比例和 convert 保持一致，点击才会落在所见位置。
	offX, offY, dw, dh := viewport(w, h, cols, rows, atomic.LoadInt32(&s.fit) != 0)
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	gx := offX + int((float64(col)-0.5)/float64(cols)*float64(dw))
	gy := offY + int((float64(row)-0.5)/float64(rows)*float64(dh))
	px := s.x + (gx-offX)*w/dw
	py := s.y + (gy-offY)*h/dh
	// 点在黑边上时夹到画面内，避免坐标落到窗口外
	if lo, hi := s.x, s.x+w-1; px < lo {
		px = lo
	} else if px > hi {
		px = hi
	}
	if lo, hi := s.y, s.y+h-1; py < lo {
		py = lo
	} else if py > hi {
		py = hi
	}
	return int16(px), int16(py)
}

// XTEST 的按钮事件用的是服务端"当前指针位置"，请求里的 rootX/rootY 只是随事件记录的
// 坐标值。实测 Termux X11 就是这样：不先把指针挪过去，ButtonPress 会落在上一次指针
// 所在的地方，点击看起来完全没反应。所以每次点/滚轮前都要先移动指针并等服务端确认。
func (s *x11Source) movePointerTo(px, py int16) {
	s.fake(xproto.MotionNotify, 0, px, py)
	c := s.inConn
	if c == nil {
		c = s.conn
	}
	c.Sync()
}

func (s *x11Source) mouseBtn(btn int, press bool, col, row int) {
	t := byte(xproto.ButtonRelease)
	if press {
		t = byte(xproto.ButtonPress)
	}
	px, py := s.cellToPix(col, row)
	s.movePointerTo(px, py)
	c := s.inConn
	if c == nil {
		c = s.conn
	}
	if q, err := xproto.QueryPointer(c, s.win).Reply(); err == nil {
		debugInput("按钮 %d 按下=%v 格子(%d,%d) -> 目标(%d,%d) 实际指针=(%d,%d) child=0x%x root=0x%x 独立连接=%v",
			btn, press, col, row, px, py, q.RootX, q.RootY, q.Child, s.win,
			s.inConn != nil && s.inConn != s.conn)
	} else {
		debugInput("按钮 %d 注入后 QueryPointer 失败: %v", btn, err)
	}
	s.fake(t, byte(btn), px, py)
}

func (s *x11Source) mouseMove(col, row int) {
	px, py := s.cellToPix(col, row)
	debugInput("移动 格子(%d,%d) -> 像素(%d,%d)", col, row, px, py)
	s.fake(xproto.MotionNotify, 0, px, py)
}

// wheel 方向：X 的 4/5/6/7 分别是上滚/下滚/左滚/右滚。
// 触屏上双指滑动就是靠这个，浏览器滚动全靠它。
func (s *x11Source) wheel(dir int, col, row int) {
	b := byte(4 + dir) // 0=上 1=下 2=左 3=右
	px, py := s.cellToPix(col, row)
	debugInput("滚轮 方向=%d 格子(%d,%d) -> 像素(%d,%d) 按钮=%d", dir, col, row, px, py, b)
	s.movePointerTo(px, py)
	s.fake(xproto.ButtonPress, b, px, py)
	s.fake(xproto.ButtonRelease, b, px, py)
}
