package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNearest256Exact(t *testing.T) {
	cases := []struct {
		r, g, b uint8
		want    uint8
	}{
		{0, 0, 0, 16},
		{255, 255, 255, 231},
		{255, 0, 0, 196},
		{0, 255, 0, 46},
		{95, 135, 215, 68},
		{128, 128, 128, 244},
	}
	for _, c := range cases {
		if got := nearest256(c.r, c.g, c.b); got != c.want {
			t.Errorf("nearest256(%d,%d,%d)=%d, 期望 %d", c.r, c.g, c.b, got, c.want)
		}
	}
}

func TestWriter256Output(t *testing.T) {
	var buf bytes.Buffer
	w := newWriter(&buf, mode256)
	c := &Canvas{Cols: 1, Rows: 1, Pix: make([]byte, 6)}
	copy(c.Pix[0:3], []byte{95, 135, 215})
	copy(c.Pix[3:6], []byte{0, 0, 0})
	if err := w.Write(c); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, "38;5;68") {
		t.Fatalf("256 前景码缺失: %q", s)
	}
	if !strings.Contains(s, "48;5;16") {
		t.Fatalf("256 背景码缺失: %q", s)
	}
	if strings.Contains(s, "38;2;") {
		t.Fatalf("256 模式不应出现真彩码: %q", s)
	}
}
