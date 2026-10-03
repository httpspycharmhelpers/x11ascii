package main

import (
	"sync/atomic"

	"github.com/BurntSushi/xgb/xproto"
	"github.com/BurntSushi/xgb/xtest"
)

type keySlot struct {
	keycode byte
	shift   bool
}

// initInput 启用 XTest 并读取服务器键盘映射（keysym -> keycode/是否需要 Shift）。
func (s *x11Source) initInput() error {
	if err := xtest.Init(s.conn); err != nil {
		return err
	}
	xtest.GrabControl(s.conn, false)

	setup := xproto.Setup(s.conn)
	min, max := setup.MinKeycode, setup.MaxKeycode
	count := int(max) - int(min) + 1
	if count <= 0 {
		return nil
	}
	km, err := xproto.GetKeyboardMapping(s.conn, min, byte(count)).Reply()
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
	return nil
}

func (s *x11Source) setTermSize(cols, rows int) {
	atomic.StoreInt32(&s.cols, int32(cols))
	atomic.StoreInt32(&s.rows, int32(rows))
}

func (s *x11Source) fake(t, detail byte, rx, ry int16) {
	xtest.FakeInput(s.conn, t, detail, xproto.TimeCurrentTime, s.win, rx, ry, 0)
}

func (s *x11Source) key(sym uint32, ctrl, alt, shift bool) {
	slot, ok := s.symMap[sym]
	if !ok {
		return
	}
	if slot.shift {
		shift = true
	}
	sk, ck, ak := s.mod[0], s.mod[2], s.mod[3]
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

// cellToPix 把终端单元格映射到 X 根窗口像素坐标。
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
	px := s.x + (col*2-1)*w/(cols*2)
	py := s.y + (row*2-1)*h/(rows*2)
	if px < 0 {
		px = 0
	}
	if py < 0 {
		py = 0
	}
	return int16(px), int16(py)
}

func (s *x11Source) mouseBtn(btn int, press bool, col, row int) {
	t := byte(xproto.ButtonRelease)
	if press {
		t = byte(xproto.ButtonPress)
	}
	px, py := s.cellToPix(col, row)
	s.fake(t, byte(btn), px, py)
}

func (s *x11Source) mouseMove(col, row int) {
	px, py := s.cellToPix(col, row)
	s.fake(xproto.MotionNotify, 0, px, py)
}

func (s *x11Source) wheel(up bool, col, row int) {
	b := byte(4)
	if !up {
		b = 5
	}
	px, py := s.cellToPix(col, row)
	s.fake(xproto.ButtonPress, b, px, py)
	s.fake(xproto.ButtonRelease, b, px, py)
}
