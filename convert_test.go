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

func TestConvertLetterboxVertical(t *testing.T) {
	// 源 4x2（宽高比 2），终端 4x2 → 有效像素 4x4（宽高比 1）。
	// 按宽高比应上下留黑边，中间两行放源。
	src := &Frame{W: 4, H: 2, Stride: 12, Aspect: true, Pix: make([]byte, 24)}
	for x := 0; x < 4; x++ {
		src.Pix[x*3] = 200      // 第一行红
		src.Pix[12+x*3+2] = 200 // 第二行蓝
	}
	var c Canvas
	convert(src, 4, 2, &c)
	at := func(r, x int) byte { return c.Pix[(r*4+x)*3] }
	if at(0, 0) != 0 || at(0, 3) != 0 {
		t.Fatalf("顶行应为黑边: %d %d", at(0, 0), at(0, 3))
	}
	if at(3, 0) != 0 || at(3, 3) != 0 {
		t.Fatalf("底行应为黑边: %d %d", at(3, 0), at(3, 3))
	}
	if at(1, 0) != 200 || at(1, 3) != 200 {
		t.Fatalf("中间上行为源红: %d %d", at(1, 0), at(1, 3))
	}
	if at(2, 0) != 0 || c.Pix[(2*4+0)*3+2] != 200 {
		t.Fatalf("中间下行为源蓝: %d %d", at(2, 0), c.Pix[(2*4+0)*3+2])
	}
}

func TestConvertLetterboxHorizontal(t *testing.T) {
	// 源 2x4（宽高比 0.5），终端 4x2 → 有效像素 4x4。按宽高比应左右留黑边。
	src := &Frame{W: 2, H: 4, Stride: 6, Aspect: true, Pix: make([]byte, 24)}
	for y := 0; y < 4; y++ {
		src.Pix[y*6] = 200   // 左列红
		src.Pix[y*6+3] = 200 // 右列红
	}
	var c Canvas
	convert(src, 4, 2, &c)
	rd := func(r, x int) byte { return c.Pix[(r*4+x)*3] }
	if rd(0, 0) != 0 || rd(1, 3) != 0 {
		t.Fatalf("左右应为黑边: %d %d", rd(0, 0), rd(1, 3))
	}
	if rd(0, 1) != 200 || rd(1, 2) != 200 {
		t.Fatalf("中间列应为源红: %d %d", rd(0, 1), rd(1, 2))
	}
}
