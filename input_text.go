package main

import (
	"time"

	"github.com/BurntSushi/xgb/xproto"
)

// 这一块是「输入层」和 X 程序之间的桥：
//   - typeText：把一整段文字逐字打进 X 里的程序（等同于真人在键盘上敲）
//   - setClipboard / getClipboard：终端这边和 X 程序的剪贴板互通

// typeText 逐字发送。中文/emoji 这类 X 抓不到 keysym 的字符走 XSendEvent
// 按 Unicode 码点发（X11 的 UTF-8 键入约定），其余走正常的 XTest 按键。
func (s *x11Source) typeText(t string) bool {
	if s.conn == nil || t == "" {
		return false
	}
	s.focusLocked()
	r := []rune(t)
	for i, c := range r {
		if c == '\n' {
			s.key(ksReturn, false, false, false)
			continue
		}
		if c == '\t' {
			s.key(ksTab, false, false, false)
			continue
		}
		if c < 0x20 {
			continue
		}
		if c < 0x80 || (c >= 0xa0 && c <= 0xff) {
			s.key(uint32(c), false, false, false)
		} else {
			s.sendRune(c)
		}
		if i%8 == 7 {
			time.Sleep(12 * time.Millisecond)
		}
	}
	s.flush()
	return true
}

// sendRune 用 XSendEvent 发一个 Unicode 码点（老程序自己按 UTF-8 约定解码）。
func (s *x11Source) sendRune(c rune) {
	if s.win == 0 {
		return
	}
	buf := make([]byte, 24)
	buf[0] = byte(c >> 24)
	buf[1] = byte(c >> 16)
	buf[2] = byte(c >> 8)
	buf[3] = byte(c)
	buf[4] = 0
	buf[5] = 0
	buf[6] = 0
	buf[7] = 0
	buf[8] = 0
	buf[9] = 0
	buf[10] = 0
	buf[11] = 0
	buf[12] = 0
	buf[13] = 0
	buf[14] = 0
	buf[15] = 0
	buf[16] = 0
	buf[17] = 0
	buf[18] = 0
	buf[19] = 0
	buf[20] = byte(xproto.KeyPress)
	buf[21] = 0
	buf[22] = 0
	buf[23] = 0
	xproto.SendEvent(s.conn, false, s.win,
		uint32(xproto.EventMaskKeyPress|xproto.EventMaskKeyRelease), string(buf))
}

func (s *x11Source) flush() {
	if s.conn == nil {
		return
	}
	xproto.GetInputFocus(s.conn).Reply()
}

// ---- 剪贴板 ----

const clipboardAtomName = "CLIPBOARD"

func (s *x11Source) clipboardAtoms() (clip, utf8Atom xproto.Atom, err error) {
	r, err := xproto.InternAtom(s.conn, false, uint16(len(clipboardAtomName)), clipboardAtomName).Reply()
	if err != nil {
		return 0, 0, err
	}
	clip = r.Atom
	get := func(n string) (xproto.Atom, error) {
		rr, e := xproto.InternAtom(s.conn, false, uint16(len(n)), n).Reply()
		if e != nil {
			return 0, e
		}
		return rr.Atom, nil
	}
	if utf8Atom, err = get("UTF8_STRING"); err != nil {
		return
	}
	return clip, utf8Atom, nil
}

// setClipboard 把文字写进 X 的 CLIPBOARD，让 X 里的程序 Ctrl+V 能粘到。
func (s *x11Source) setClipboard(text string) bool {
	if s.conn == nil {
		return false
	}
	clip, _, err := s.clipboardAtoms()
	if err != nil {
		return false
	}
	xproto.ChangeWindowAttributes(s.conn, s.win, xproto.CwEventMask,
		[]uint32{uint32(xproto.EventMaskPropertyChange)})
	data := []byte(text)
	xproto.ChangeProperty(s.conn, xproto.PropModeReplace, s.win, clip,
		xproto.AtomString, 8, uint32(len(data)), data)
	xproto.SetSelectionOwner(s.conn, s.win, clip, xproto.TimeCurrentTime)
	s.flush()
	own, err := xproto.GetSelectionOwner(s.conn, clip).Reply()
	return err == nil && own.Owner == s.win
}

// getClipboard 从 X 的 CLIPBOARD 取回文字（X 里的程序复制的 → 终端输入框）。
func (s *x11Source) getClipboard() string {
	if s.conn == nil {
		return ""
	}
	clip, utf8Atom, err := s.clipboardAtoms()
	if err != nil {
		return ""
	}
	owner, err := xproto.GetSelectionOwner(s.conn, clip).Reply()
	if err != nil || owner.Owner == 0 {
		return ""
	}
	var out []byte
	req := xproto.ConvertSelection(s.conn, s.win, clip, utf8Atom, utf8Atom, xproto.TimeCurrentTime)
	if _, err := req.Reply(); err != nil {
		return ""
	}
	// 对方可能分多次（INCR）或延迟应答，短暂等待一下
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		rep, err := xproto.GetProperty(s.conn, true, s.win, utf8Atom, xproto.AtomAny, 0, 1<<20).Reply()
		if err != nil {
			break
		}
		if rep.Format != 0 {
			out = append(out, rep.Value...)
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	s.flush()
	return string(out)
}
