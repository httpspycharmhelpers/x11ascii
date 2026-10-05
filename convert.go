package main

// blitCenter 把 src 摆到 dst 的指定位置（越界部分直接裁掉），
// 用来实现「画面比终端小就居中留黑、比终端大就居中裁切」。
func blitCenter(src, dst *Canvas, cols, rows, offX, offY int) {
	need := cols * rows * 6
	if len(dst.Pix) < need {
		dst.Pix = make([]byte, need)
	}
	dst.Cols, dst.Rows = cols, rows
	dst.xe = dst.xe[:0]
	dst.ye = dst.ye[:0]
	dst.sw, dst.sh = cols, rows
	clear(dst.Pix[:need])
	sw, sh := src.Cols, src.Rows
	if sw > cols {
		sw = cols
	}
	if sh > rows {
		sh = rows
	}
	for r := 0; r < sh; r++ {
		so := (r * src.Cols) * 6
		do := ((r+offY)*cols + offX) * 6
		if do < 0 {
			continue
		}
		n := sw * 6
		if do+n > need {
			n = need - do
		}
		if n <= 0 {
			break
		}
		copy(dst.Pix[do:do+n], src.Pix[so:so+n])
	}
	dst.markCol, dst.markRow = src.markCol, src.markRow
}

type Canvas struct {
	Cols, Rows int
	Pix        []byte
	xe, ye     []int
	sw, sh     int

	// 触屏准星：纯触屏没有鼠标指针，点下去看不见点哪儿了，所以把最后点击的
	// 单元格反色显示一会儿（-1 表示不显示）。
	markCol, markRow int
}

// setMark 设置/清除触屏准星（1-based 单元格坐标）。
func (c *Canvas) setMark(col, row int) {
	c.markCol, c.markRow = col, row
}

// drawMark 在准星位置画一个十字（反白），在 convert 之后调用。
func (c *Canvas) drawMark() {
	if c.markCol < 1 || c.markRow < 1 || c.markCol > c.Cols || c.markRow > c.Rows {
		return
	}
	// 一个单元格 = 1 列 × 2 个纵向像素（半块字符上半/下半），不是 2×2
	gw := c.Cols
	px := c.markCol - 1
	for dy := 0; dy < 2; dy++ {
		o := (((c.markRow-1)*2+dy)*gw + px) * 3
		if o+2 >= len(c.Pix) {
			continue
		}
		c.Pix[o] = 255 - c.Pix[o]
		c.Pix[o+1] = 255 - c.Pix[o+1]
		c.Pix[o+2] = 255 - c.Pix[o+2]
	}
}

func (c *Canvas) resize(cols, rows int) {
	c.Cols, c.Rows = cols, rows
	n := cols * rows * 2 * 3
	if cap(c.Pix) < n {
		c.Pix = make([]byte, n)
	} else {
		c.Pix = c.Pix[:n]
	}
}

func (c *Canvas) edges(srcW, srcH, gw, gh int) {
	if c.sw != srcW || c.sh != srcH || len(c.xe) != gw+1 || len(c.ye) != gh+1 {
		c.sw, c.sh = srcW, srcH
		c.xe = make([]int, gw+1)
		c.ye = make([]int, gh+1)
		for i := 0; i <= gw; i++ {
			c.xe[i] = i * srcW / gw
		}
		for i := 0; i <= gh; i++ {
			c.ye[i] = i * srcH / gh
		}
	}
}

// viewport 计算源画面在输出网格里实际占的格子范围（列 offX..offX+dw、行 offY..offY+dh）。
// aspect=true 时按源宽高比居中留黑边。convert 和鼠标坐标映射共用，保证点击位置和画面一致。
func viewport(srcW, srcH, cols, rows int, aspect bool) (offX, offY, dw, dh int) {
	gw, gh := cols, rows*2
	dw, dh = gw, gh
	if !aspect || srcW <= 0 || srcH <= 0 {
		return 0, 0, gw, gh
	}
	sAsp := float64(srcW) / float64(srcH)
	gAsp := float64(gw) / float64(gh)
	if sAsp > gAsp {
		dh = int(float64(gw)/sAsp + 0.5)
		if dh < 1 {
			dh = 1
		}
		offY = (gh - dh) / 2
	} else {
		dw = int(float64(gh)*sAsp + 0.5)
		if dw < 1 {
			dw = 1
		}
		offX = (gw - dw) / 2
	}
	return
}

func convert(src *Frame, cols, rows int, dst *Canvas) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	dst.resize(cols, rows)
	gw, gh := cols, rows*2

	// 计算源在输出网格里的视口；Aspect=true 时按源宽高比居中（letterbox），
	// 避免把画面拉伸填满导致的变形（软键盘弹出/横竖屏切换时尤其明显）。
	offX, offY, dw, dh := viewport(src.W, src.H, cols, rows, src.Aspect)
	letterbox := offX != 0 || offY != 0 || dw != gw || dh != gh

	if letterbox {
		for i := range dst.Pix {
			dst.Pix[i] = 0
		}
	}

	for gy := offY; gy < offY+dh; gy++ {
		y0 := (gy - offY) * src.H / dh
		y1 := (gy - offY + 1) * src.H / dh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > src.H {
			y1 = src.H
		}
		for gx := offX; gx < offX+dw; gx++ {
			x0 := (gx - offX) * src.W / dw
			x1 := (gx - offX + 1) * src.W / dw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > src.W {
				x1 = src.W
			}
			var sr, sg, sb, n int
			for sy := y0; sy < y1; sy++ {
				base := sy*src.Stride + x0*3
				for sx := x0; sx < x1; sx++ {
					sr += int(src.Pix[base])
					sg += int(src.Pix[base+1])
					sb += int(src.Pix[base+2])
					base += 3
					n++
				}
			}
			o := (gy*gw + gx) * 3
			if n > 0 {
				dst.Pix[o] = byte(sr / n)
				dst.Pix[o+1] = byte(sg / n)
				dst.Pix[o+2] = byte(sb / n)
			}
		}
	}
}
