package main

import "testing"

// cellToPix 必须和 convert 用同一套 letterbox 视口，否则 -fit 留黑边时
// 点击位置会整体偏移——浏览器点链接/按钮全靠这个。
func TestCellToPixLetterbox(t *testing.T) {
	s := &x11Source{w: 640, h: 400, fit: 1, aw: 640, ah: 400, cols: 100, rows: 40}
	// 网格 100x80，源 640x400(1.6) 比网格(1.25) 更宽 → 上下留黑边
	offX, offY, dw, dh := viewport(640, 400, 100, 40, true)
	if offX != 0 || offY == 0 || dw != 100 || dh >= 80 {
		t.Fatalf("视口算错: offX=%d offY=%d dw=%d dh=%d", offX, offY, dw, dh)
	}

	// 左上角单元格应落在画面内左上角附近，而不是黑边上
	if px, py := s.cellToPix(1, 1); px < 0 || px >= 640 || py < 0 || py >= 400 {
		t.Fatalf("左上角映射越界: (%d,%d)", px, py)
	}
	// 第 1 行在 letterbox 里其实是上黑边，内容要从 offY 开始，映射不能跑到画面外
	if _, py := s.cellToPix(50, 1); py < 0 || py >= 400 {
		t.Fatalf("顶部黑边映射越界: %d", py)
	}

	// 正中间单元格应接近画面中心
	px, py := s.cellToPix(50, 20)
	if dx, dy := px-320, py-200; dx < -40 || dx > 40 || dy < -40 || dy > 40 {
		t.Fatalf("中心映射偏移过大: (%d,%d) 期望≈(320,200)", px, py)
	}

	// 点在底部黑边（第 40 行）时必须夹到画面内，不能跑到窗口外
	if _, py := s.cellToPix(50, 40); py > 399 {
		t.Fatalf("黑边点击未夹紧: py=%d", py)
	}
	// 点在黑边上时也要夹住 X
	if px, _ := s.cellToPix(1, 40); px < 0 || px > 639 {
		t.Fatalf("黑边点击 X 未夹紧: px=%d", px)
	}
}

// -fit=false 时画面被拉伸铺满，映射应覆盖整个源区域
func TestCellToPixStretch(t *testing.T) {
	s := &x11Source{w: 640, h: 400, fit: 0, aw: 640, ah: 400, cols: 100, rows: 40}
	px, py := s.cellToPix(50, 20)
	if px < 280 || px > 360 || py < 160 || py > 240 {
		t.Fatalf("拉伸模式中心映射不对: (%d,%d)", px, py)
	}
	if px, _ := s.cellToPix(100, 20); px < 600 || px > 639 {
		t.Fatalf("最右列未映射到右边缘: %d", px)
	}
}

// 修饰键参数解析：xterm 用 CSI 1;5A 表示 Ctrl+Up
func TestParseMod(t *testing.T) {
	cases := []struct {
		param        string
		ctrl, al, sh bool
	}{
		{"5", true, false, false},  // Ctrl
		{"3", false, true, false},  // Alt
		{"2", false, false, true},  // Shift
		{"6", true, false, true},   // Ctrl+Shift
		{"8", true, true, true},    // Shift+Alt+Ctrl（xterm: 8 = 1+1+2+4）
		{"1", false, false, false}, // 无修饰
		{"", false, false, false},
	}
	for _, c := range cases {
		ctrl, al, sh := parseMod(c.param)
		if ctrl != c.ctrl || al != c.al || sh != c.sh {
			t.Errorf("parseMod(%q) = (%v,%v,%v) 期望 (%v,%v,%v)", c.param, ctrl, al, sh, c.ctrl, c.al, c.sh)
		}
	}
}
