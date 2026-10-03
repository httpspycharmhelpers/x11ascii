package main

import "testing"

func TestParseSize(t *testing.T) {
	w, h, err := parseSize("320x200")
	if err != nil || w != 320 || h != 200 {
		t.Fatalf("得到 %d,%d,%v", w, h, err)
	}
	if _, _, err := parseSize("nope"); err == nil {
		t.Fatal("非法尺寸应报错")
	}
	if _, _, err := parseSize("0x10"); err == nil {
		t.Fatal("零尺寸应报错")
	}
}

func TestParseRegion(t *testing.T) {
	x, y, w, h, err := parseRegion("640x480+10+20")
	if err != nil || x != 10 || y != 20 || w != 640 || h != 480 {
		t.Fatalf("得到 %d,%d,%d,%d,%v", x, y, w, h, err)
	}
	x, y, w, h, err = parseRegion("100x50")
	if err != nil || x != 0 || y != 0 || w != 100 || h != 50 {
		t.Fatalf("得到 %d,%d,%d,%d,%v", x, y, w, h, err)
	}
	if _, _, _, _, err := parseRegion("bad"); err == nil {
		t.Fatal("非法区域应报错")
	}
}

func TestPatternSource(t *testing.T) {
	s := newPatternSource(8, 4)
	if w, h := s.Size(); w != 8 || h != 4 {
		t.Fatalf("尺寸 %dx%d", w, h)
	}
	var f Frame
	if err := s.Grab(&f); err != nil {
		t.Fatal(err)
	}
	if f.W != 8 || f.H != 4 || f.Stride != 24 || len(f.Pix) != 96 {
		t.Fatalf("帧 %dx%d stride %d len %d", f.W, f.H, f.Stride, len(f.Pix))
	}
	prev := append([]byte(nil), f.Pix[1:2]...)
	if err := s.Grab(&f); err != nil {
		t.Fatal(err)
	}
	_ = prev
}
