package main

import "testing"

func TestConvertIdenticalPixels(t *testing.T) {
	src := &Frame{W: 2, H: 2, Stride: 6, Pix: []byte{
		1, 2, 3, 4, 5, 6,
		7, 8, 9, 10, 11, 12,
	}}
	var c Canvas
	convert(src, 2, 1, &c)
	if c.Cols != 2 || c.Rows != 1 {
		t.Fatalf("尺寸 %dx%d, 期望 2x1", c.Cols, c.Rows)
	}
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	if string(c.Pix) != string(want) {
		t.Fatalf("像素 %v, 期望 %v", c.Pix, want)
	}
}

func TestConvertBoxAverage(t *testing.T) {
	src := &Frame{W: 4, H: 4, Stride: 12, Pix: make([]byte, 48)}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := byte(10)
			if y >= 2 {
				v = 20
			}
			src.Pix[y*12+x*3] = v
		}
	}
	var c Canvas
	convert(src, 1, 1, &c)
	if c.Pix[0] != 10 || c.Pix[3] != 20 {
		t.Fatalf("上下半块平均 %d/%d, 期望 10/20", c.Pix[0], c.Pix[3])
	}
}

func TestConvertClampZero(t *testing.T) {
	src := &Frame{W: 2, H: 2, Stride: 6, Pix: make([]byte, 12)}
	var c Canvas
	convert(src, 0, 0, &c)
	if c.Cols != 1 || c.Rows != 1 {
		t.Fatalf("clamp 后 %dx%d, 期望 1x1", c.Cols, c.Rows)
	}
}
