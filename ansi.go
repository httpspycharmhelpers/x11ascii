package main

import (
	"bufio"
	"io"
	"strconv"
)

type cell struct {
	fg, bg [3]byte
}

type Writer struct {
	buf     *bufio.Writer
	cols    int
	rows    int
	prev    []cell
	fg, bg  [3]byte
	haveFG  bool
	haveBG  bool
	curX    int
	curY    int
	scratch []byte
}

func newWriter(w io.Writer) *Writer {
	return &Writer{
		buf:  bufio.NewWriterSize(w, 1<<18),
		curX: -1,
		curY: -1,
	}
}

func append3(b []byte, c [3]byte) []byte {
	b = strconv.AppendUint(b, uint64(c[0]), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(c[1]), 10)
	b = append(b, ';')
	b = strconv.AppendUint(b, uint64(c[2]), 10)
	return b
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
			var fg, bg [3]byte
			copy(fg[:], c.Pix[top:top+3])
			copy(bg[:], c.Pix[bot:bot+3])
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
				b = append(b, "\x1b["...)
				first := true
				if needFG {
					b = append(b, "38;2;"...)
					b = append3(b, fg)
					first = false
				}
				if needBG {
					if !first {
						b = append(b, ';')
					}
					b = append(b, "48;2;"...)
					b = append3(b, bg)
				}
				b = append(b, 'm')
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
