package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriterClearRedraws(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf, modeTrue)
	c := &Canvas{Cols: 2, Rows: 1, Pix: make([]byte, 12)}
	fill(c, [3]byte{255, 0, 0}, [3]byte{0, 0, 255})
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	n := buf.Len()
	w.Clear()
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String()[n:], "\x1b[2J") {
		t.Fatal("Clear 后应清屏重画")
	}
}
