package main

import (
	"bytes"
	"strings"
	"testing"
)

func fill(c *Canvas, top, bot [3]byte) {
	gw := c.Cols
	for gy := 0; gy < c.Rows*2; gy++ {
		col := top
		if gy%2 == 1 {
			col = bot
		}
		for gx := 0; gx < gw; gx++ {
			o := (gy*gw + gx) * 3
			copy(c.Pix[o:o+3], col[:])
		}
	}
}

func TestWriterSkipsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf, modeTrue)
	c := &Canvas{Cols: 3, Rows: 1, Pix: make([]byte, 18)}
	fill(c, [3]byte{255, 0, 0}, [3]byte{0, 0, 255})

	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	first := buf.Len()
	if first == 0 {
		t.Fatal("首帧应输出")
	}
	if !strings.Contains(buf.String(), "\x1b[38;2;255;0;0") {
		t.Fatalf("应包含真彩前景码: %q", buf.String())
	}
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != first {
		t.Fatalf("相同帧不应有输出，增加了 %d 字节", buf.Len()-first)
	}
}

func TestWriterOnlyRewritesChangedCell(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf, modeTrue)
	c := &Canvas{Cols: 4, Rows: 1, Pix: make([]byte, 24)}
	fill(c, [3]byte{0, 0, 0}, [3]byte{0, 0, 0})
	w.Write(c)
	base := buf.Len()

	copy(c.Pix[6:9], []byte{255, 255, 255})
	copy(c.Pix[18:21], []byte{255, 255, 255})
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	added := buf.String()[base:]
	if !strings.Contains(added, "\x1b[1;3H") {
		t.Fatalf("应只定位到变化格，实际 %q", added)
	}
	if strings.Count(added, "\u2580") != 1 {
		t.Fatalf("只应重画一个块，实际 %q", added)
	}
}

func TestWriterResizeClears(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf, modeTrue)
	c := &Canvas{Cols: 2, Rows: 1, Pix: make([]byte, 12)}
	w.Write(c)
	n := buf.Len()
	c2 := &Canvas{Cols: 3, Rows: 2, Pix: make([]byte, 36)}
	w.Write(c2)
	if !strings.Contains(buf.String()[n:], "\x1b[2J") {
		t.Fatal("尺寸变化时应清屏")
	}
}
