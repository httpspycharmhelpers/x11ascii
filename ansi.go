package main

import (
	"bufio"
	"io"
	"strconv"
)

type colorMode int

const (
	modeTrue colorMode = iota
	mode256
)

type cell struct {
	fg, bg uint32
}

type Writer struct {
	buf     *bufio.Writer
	mode    colorMode
	cols    int
	rows    int
	prev    []cell
	fg, bg  uint32
	haveFG  bool
	haveBG  bool
	curX    int
	curY    int
	scratch []byte
}

func newWriter(w io.Writer, mode colorMode) *Writer {
	return &Writer{
		buf:  bufio.NewWriterSize(w, 1<<18),
		mode: mode,
		curX: -1,
		curY: -1,
	}
}

func packRGB(c [3]byte) uint32 {
	return uint32(c[0])<<16 | uint32(c[1])<<8 | uint32(c[2])
}

func appendRGB(b []byte, v uint32) []byte {
	b = strconv.AppendUint(b, uint64(byte(v>>16)), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(byte(v>>8)), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(byte(v)), 10)
	return b
}

func (w *Writer) colorOf(c [3]byte) uint32 {
	if w.mode == mode256 {
		return uint32(nearest256(c[0], c[1], c[2]))
	}
	return packRGB(c)
}

func (w *Writer) appendSGR(b []byte, needFG, needBG bool, fg, bg uint32) []byte {
	b = append(b, "\x1b["...)
	first := true
	if needFG {
		if w.mode == mode256 {
			b = append(b, "38;5;"...)
			b = strconv.AppendUint(b, uint64(fg), 10)
		} else {
			b = append(b, "38;2;"...)
			b = appendRGB(b, fg)
		}
		first = false
	}
	if needBG {
		if !first {
			b = append(b, ';')
		}
		if w.mode == mode256 {
			b = append(b, "48;5;"...)
			b = strconv.AppendUint(b, uint64(bg), 10)
		} else {
			b = append(b, "48;2;"...)
			b = appendRGB(b, bg)
		}
	}
	return append(b, 'm')
}

func (w *Writer) Clear() {
	w.cols, w.rows = -1, -1
	w.haveFG, w.haveBG = false, false
	w.curX, w.curY = -1, -1
}

// WriteStatus 在画面下方写虚拟键盘条（每行一条，含颜色转义）。
func (w *Writer) WriteStatus(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	b := w.scratch[:0]
	for i, ln := range lines {
		b = append(b, "\x1b["...)
		b = strconv.AppendInt(b, int64(w.rows+i+1), 10)
		b = append(b, ";1H\x1b[K"...)
		b = append(b, ln...)
	}
	// 光标已经挪到画面之外，通知下一次 Write 重新绝对定位
	w.curX, w.curY = -1, -1
	w.buf.Write(b)
	w.scratch = b[:0]
	return w.buf.Flush()
}

func (w *Writer) Write(c *Canvas) error {
	if w.cols != c.Cols || w.rows != c.Rows {
		w.cols, w.rows = c.Cols, c.Rows
		w.prev = make([]cell, c.Cols*c.Rows)
		w.haveFG, w.haveBG = false, false
		w.curX, w.curY = -1, -1
		w.buf.WriteString("\x1b[2J\x1b[H")
	}
	b := w.scratch[:0]
	for row := 0; row < c.Rows; row++ {
		for col := 0; col < c.Cols; col++ {
			top := (row*2*c.Cols + col) * 3
			bot := ((row*2+1)*c.Cols + col) * 3
			var fgc, bgc [3]byte
			copy(fgc[:], c.Pix[top:top+3])
			copy(bgc[:], c.Pix[bot:bot+3])
			fg := w.colorOf(fgc)
			bg := w.colorOf(bgc)

			ci := row*c.Cols + col
			if w.prev[ci].fg == fg && w.prev[ci].bg == bg {
				continue
			}
			if w.curX != col || w.curY != row {
				b = append(b, "\x1b["...)
				b = strconv.AppendInt(b, int64(row+1), 10)
				b = append(b, ';')
				b = strconv.AppendInt(b, int64(col+1), 10)
				b = append(b, 'H')
				w.curX, w.curY = col, row
			}
			needFG := !w.haveFG || w.fg != fg
			needBG := !w.haveBG || w.bg != bg
			if needFG || needBG {
				b = w.appendSGR(b, needFG, needBG, fg, bg)
				w.fg, w.bg = fg, bg
				w.haveFG, w.haveBG = true, true
			}
			b = append(b, 0xe2, 0x96, 0x80)
			w.prev[ci] = cell{fg, bg}
			w.curX++
			if w.curX >= c.Cols {
				w.curX = -1
			}
		}
	}
	if len(b) > 0 {
		w.buf.Write(b)
	}
	w.scratch = b[:0]
	return w.buf.Flush()
}
