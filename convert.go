package main

type Canvas struct {
	Cols, Rows int
	Pix        []byte
	xe, ye     []int
	sw, sh     int
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

func convert(src *Frame, cols, rows int, dst *Canvas) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	dst.resize(cols, rows)
	gw, gh := cols, rows*2
	dst.edges(src.W, src.H, gw, gh)

	for gy := 0; gy < gh; gy++ {
		y0, y1 := dst.ye[gy], dst.ye[gy+1]
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for gx := 0; gx < gw; gx++ {
			x0, x1 := dst.xe[gx], dst.xe[gx+1]
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var sr, sg, sb, n int
			for sy := y0; sy < y1 && sy < src.H; sy++ {
				base := sy*src.Stride + x0*3
				for sx := x0; sx < x1 && sx < src.W; sx++ {
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
