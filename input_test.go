package main

import (
	"io"
	"testing"
	"time"
)

type sliceSource struct {
	data []byte
	i    int
}

func (s *sliceSource) readByte() (byte, error) {
	if s.i >= len(s.data) {
		return 0, io.EOF
	}
	b := s.data[s.i]
	s.i++
	return b, nil
}

func (s *sliceSource) readable(time.Duration) bool { return s.i < len(s.data) }

type recKey struct {
	sym              uint32
	ctrl, alt, shift bool
}

type recMouse struct {
	kind     string
	btn      int
	pressed  bool
	col, row int
}

type recSink struct {
	keys []recKey
	mice []recMouse
}

func (r *recSink) key(sym uint32, ctrl, alt, shift bool) {
	r.keys = append(r.keys, recKey{sym, ctrl, alt, shift})
}
func (r *recSink) mouseBtn(btn int, press bool, col, row int) {
	r.mice = append(r.mice, recMouse{"btn", btn, press, col, row})
}
func (r *recSink) mouseMove(col, row int) {
	r.mice = append(r.mice, recMouse{"move", 0, false, col, row})
}
func (r *recSink) wheel(up bool, col, row int) {
	r.mice = append(r.mice, recMouse{"wheel", 0, up, col, row})
}

func pumpBytes(t *testing.T, data []byte) *recSink {
	t.Helper()
	s := &recSink{}
	p := &inputPump{src: &sliceSource{data: data}, sink: s}
	p.run()
	return s
}

func TestPumpPrintable(t *testing.T) {
	s := pumpBytes(t, []byte("aA1!"))
	want := []uint32{'a', 'A', '1', '!'}
	if len(s.keys) != len(want) {
		t.Fatalf("按键数=%d 期望 %d: %+v", len(s.keys), len(want), s.keys)
	}
	for i, w := range want {
		if s.keys[i].sym != w || s.keys[i].ctrl || s.keys[i].alt {
			t.Errorf("第%d键=%+v 期望 sym=%#x", i, s.keys[i], w)
		}
	}
}

func TestPumpBasicKeys(t *testing.T) {
	s := pumpBytes(t, []byte{0x09, 0x0d, 0x7f})
	want := []uint32{ksTab, ksReturn, ksBackSpace}
	if len(s.keys) != 3 {
		t.Fatalf("按键数=%d", len(s.keys))
	}
	for i, w := range want {
		if s.keys[i].sym != w {
			t.Errorf("第%d键=%#x 期望 %#x", i, s.keys[i].sym, w)
		}
	}
}

func TestPumpCtrlA(t *testing.T) {
	s := pumpBytes(t, []byte{0x01})
	if len(s.keys) != 1 || s.keys[0].sym != 'a' || !s.keys[0].ctrl {
		t.Fatalf("Ctrl+A=%+v", s.keys)
	}
}

func TestPumpEscapeAlone(t *testing.T) {
	s := pumpBytes(t, []byte{0x1b})
	if len(s.keys) != 1 || s.keys[0].sym != ksEscape || s.keys[0].alt {
		t.Fatalf("单独 ESC=%+v", s.keys)
	}
}

func TestPumpAltKey(t *testing.T) {
	s := pumpBytes(t, []byte{0x1b, 'a'})
	if len(s.keys) != 1 || s.keys[0].sym != 'a' || !s.keys[0].alt {
		t.Fatalf("Alt+a=%+v", s.keys)
	}
}

func TestPumpArrows(t *testing.T) {
	s := pumpBytes(t, []byte("\x1b[A\x1b[B\x1b[C\x1b[D"))
	want := []uint32{ksUp, ksDown, ksRight, ksLeft}
	if len(s.keys) != 4 {
		t.Fatalf("按键数=%d: %+v", len(s.keys), s.keys)
	}
	for i, w := range want {
		if s.keys[i].sym != w {
			t.Errorf("第%d键=%#x 期望 %#x", i, s.keys[i].sym, w)
		}
	}
}

func TestPumpSpecialKeys(t *testing.T) {
	cases := []struct {
		in  string
		sym uint32
	}{
		{"\x1b[15~", ksF1 + 4},
		{"\x1b[3~", ksDelete},
		{"\x1b[5~", ksPageUp},
		{"\x1b[1;5A", ksUp},
		{"\x1bOH", ksHome},
	}
	for _, c := range cases {
		s := pumpBytes(t, []byte(c.in))
		if len(s.keys) != 1 || s.keys[0].sym != c.sym {
			t.Errorf("%q => %+v 期望 %#x", c.in, s.keys, c.sym)
		}
	}
}

func TestPumpShiftTab(t *testing.T) {
	s := pumpBytes(t, []byte("\x1b[Z"))
	if len(s.keys) != 1 || s.keys[0].sym != ksTab || !s.keys[0].shift {
		t.Fatalf("Shift+Tab=%+v", s.keys)
	}
}

func TestPumpMouse(t *testing.T) {
	s := pumpBytes(t, []byte("\x1b[<0;10;5M\x1b[<0;10;5m\x1b[<64;3;4M\x1b[<32;7;8M"))
	if len(s.mice) != 4 {
		t.Fatalf("鼠标事件数=%d: %+v", len(s.mice), s.mice)
	}
	if m := s.mice[0]; m.kind != "btn" || m.btn != 1 || !m.pressed || m.col != 10 || m.row != 5 {
		t.Errorf("左键按下=%+v", m)
	}
	if m := s.mice[1]; m.kind != "btn" || m.btn != 1 || m.pressed {
		t.Errorf("左键释放=%+v", m)
	}
	if m := s.mice[2]; m.kind != "wheel" || !m.pressed || m.col != 3 || m.row != 4 {
		t.Errorf("滚轮上=%+v", m)
	}
	if m := s.mice[3]; m.kind != "move" || m.col != 7 || m.row != 8 {
		t.Errorf("移动=%+v", m)
	}
}

func TestPumpUTF8(t *testing.T) {
	s := pumpBytes(t, []byte("中"))
	if len(s.keys) != 1 || s.keys[0].sym != (0x01000000|0x4E2D) {
		t.Fatalf("UTF-8 中=%+v", s.keys)
	}
}

func TestPumpQuit(t *testing.T) {
	called := false
	s := &recSink{}
	p := &inputPump{
		src:    &sliceSource{data: []byte{0x03}},
		sink:   s,
		onQuit: func() { called = true },
	}
	p.run()
	if !called {
		t.Fatal("Ctrl+C 未触发退出")
	}
}
