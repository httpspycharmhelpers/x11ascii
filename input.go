package main

import (
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// X keysyms（仅用到的这些）
const (
	ksBackSpace = 0xFF08
	ksTab       = 0xFF09
	ksReturn    = 0xFF0D
	ksEscape    = 0xFF1B
	ksHome      = 0xFF50
	ksLeft      = 0xFF51
	ksUp        = 0xFF52
	ksRight     = 0xFF53
	ksDown      = 0xFF54
	ksPageUp    = 0xFF55
	ksPageDown  = 0xFF56
	ksEnd       = 0xFF57
	ksInsert    = 0xFF63
	ksDelete    = 0xFFFF
	ksF1        = 0xFFBE
)

// inputSink 收到解析后的输入事件（坐标 col/row 为终端 1-based 单元格）。
type inputSink interface {
	key(sym uint32, ctrl, alt, shift bool)
	mouseBtn(btn int, press bool, col, row int)
	mouseMove(col, row int)
	wheel(up bool, col, row int)
}

type byteSource interface {
	readByte() (byte, error)
	readable(time.Duration) bool
}

type fdSource struct{ fd int }

func (f fdSource) readByte() (byte, error) {
	var b [1]byte
	n, err := unix.Read(f.fd, b[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return b[0], nil
}

func (f fdSource) readable(d time.Duration) bool {
	pfd := []unix.PollFd{{Fd: int32(f.fd), Events: unix.POLLIN}}
	n, err := unix.Poll(pfd, int(d.Milliseconds()))
	return err == nil && n > 0
}

// inputPump 把终端字节流解析成按键/鼠标事件并交给 sink。
// Ctrl+C（0x03）用于退出，不转发（可避免把终端锁死）。
type inputPump struct {
	src    byteSource
	sink   inputSink
	onQuit func()
}

func (p *inputPump) run() {
	for {
		b, err := p.src.readByte()
		if err != nil {
			return
		}
		if b == 0x03 {
			if p.onQuit != nil {
				p.onQuit()
			}
			return
		}
		if b == 0x1b {
			if !p.src.readable(25 * time.Millisecond) {
				p.sink.key(ksEscape, false, false, false)
				continue
			}
			b2, err := p.src.readByte()
			if err != nil {
				return
			}
			switch b2 {
			case '[', 'O':
				p.csi()
			default:
				p.emitByte(b2, true)
			}
			continue
		}
		p.emitByte(b, false)
	}
}

func (p *inputPump) emitByte(b byte, alt bool) {
	if b >= 0x80 {
		p.emitRune(b, alt)
		return
	}
	switch {
	case b == 0x09:
		p.sink.key(ksTab, false, alt, false)
	case b == 0x0d || b == 0x0a:
		p.sink.key(ksReturn, false, alt, false)
	case b == 0x08 || b == 0x7f:
		p.sink.key(ksBackSpace, false, alt, false)
	case b == 0x00:
		p.sink.key(' ', true, alt, false)
	case b >= 0x01 && b <= 0x1a:
		p.sink.key(uint32(b)+0x60, true, alt, false)
	case b == 0x1c:
		p.sink.key('\\', true, alt, false)
	case b == 0x1d:
		p.sink.key(']', true, alt, false)
	case b == 0x1e:
		p.sink.key('^', true, alt, false)
	case b == 0x1f:
		p.sink.key('_', true, alt, false)
	case b >= 0x20 && b <= 0x7e:
		p.sink.key(uint32(b), false, alt, false)
	}
}

func (p *inputPump) emitRune(lead byte, alt bool) {
	var buf [4]byte
	buf[0] = lead
	n := 1
	want := 1
	switch {
	case lead&0xE0 == 0xC0:
		want = 2
	case lead&0xF0 == 0xE0:
		want = 3
	case lead&0xF8 == 0xF0:
		want = 4
	default:
		return
	}
	for n < want {
		nb, err := p.src.readByte()
		if err != nil {
			return
		}
		buf[n] = nb
		n++
	}
	r, _ := utf8.DecodeRune(buf[:n])
	if r == utf8.RuneError {
		return
	}
	sym := uint32(r)
	if r > 0xff {
		sym = 0x01000000 | uint32(r)
	}
	p.sink.key(sym, false, alt, false)
}

func (p *inputPump) csi() {
	var sb strings.Builder
	for {
		b, err := p.src.readByte()
		if err != nil {
			return
		}
		if b >= 0x40 && b <= 0x7e {
			p.interpretCSI(sb.String(), b)
			return
		}
		sb.WriteByte(b)
	}
}

func (p *inputPump) interpretCSI(s string, final byte) {
	if strings.HasPrefix(s, "<") {
		parts := strings.Split(s[1:], ";")
		if len(parts) >= 3 {
			cb, _ := strconv.Atoi(parts[0])
			x, _ := strconv.Atoi(parts[1])
			y, _ := strconv.Atoi(parts[2])
			switch {
			case cb&64 != 0:
				p.sink.wheel(cb == 64, x, y)
			case final == 'm':
				p.sink.mouseBtn((cb&3)+1, false, x, y)
			case cb&32 != 0:
				p.sink.mouseMove(x, y)
			default:
				p.sink.mouseBtn((cb&3)+1, true, x, y)
			}
		}
		return
	}
	if s == "" && final == 'M' {
		b1, e1 := p.src.readByte()
		b2, e2 := p.src.readByte()
		b3, e3 := p.src.readByte()
		if e1 != nil || e2 != nil || e3 != nil {
			return
		}
		cb := int(b1) - 32
		p.sink.mouseBtn((cb&3)+1, true, int(b2)-32, int(b3)-32)
		return
	}
	switch final {
	case 'A':
		p.sink.key(ksUp, false, false, false)
	case 'B':
		p.sink.key(ksDown, false, false, false)
	case 'C':
		p.sink.key(ksRight, false, false, false)
	case 'D':
		p.sink.key(ksLeft, false, false, false)
	case 'H':
		p.sink.key(ksHome, false, false, false)
	case 'F':
		p.sink.key(ksEnd, false, false, false)
	case 'Z':
		p.sink.key(ksTab, false, false, true)
	case 'P':
		p.sink.key(ksF1, false, false, false)
	case 'Q':
		p.sink.key(ksF1+1, false, false, false)
	case 'R':
		p.sink.key(ksF1+2, false, false, false)
	case 'S':
		p.sink.key(ksF1+3, false, false, false)
	case '~':
		first := s
		if i := strings.IndexByte(first, ';'); i >= 0 {
			first = first[:i]
		}
		n, _ := strconv.Atoi(first)
		p.tildeKey(n)
	}
}

func (p *inputPump) tildeKey(n int) {
	switch n {
	case 1, 7:
		p.sink.key(ksHome, false, false, false)
	case 2:
		p.sink.key(ksInsert, false, false, false)
	case 3:
		p.sink.key(ksDelete, false, false, false)
	case 4, 8:
		p.sink.key(ksEnd, false, false, false)
	case 5:
		p.sink.key(ksPageUp, false, false, false)
	case 6:
		p.sink.key(ksPageDown, false, false, false)
	case 15:
		p.sink.key(ksF1+4, false, false, false)
	case 17:
		p.sink.key(ksF1+5, false, false, false)
	case 18:
		p.sink.key(ksF1+6, false, false, false)
	case 19:
		p.sink.key(ksF1+7, false, false, false)
	case 20:
		p.sink.key(ksF1+8, false, false, false)
	case 21:
		p.sink.key(ksF1+9, false, false, false)
	case 23:
		p.sink.key(ksF1+10, false, false, false)
	case 24:
		p.sink.key(ksF1+11, false, false, false)
	}
}
