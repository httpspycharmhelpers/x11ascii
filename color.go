package main

var cubeVals = [6]int{0, 95, 135, 175, 215, 255}

func dist2(r, g, b, r2, g2, b2 int) int {
	dr, dg, db := r-r2, g-g2, b-b2
	return dr*dr + dg*dg + db*db
}

func nearest6(v int) int {
	best, bestD := 0, 1<<30
	for i, cv := range cubeVals {
		d := cv - v
		if d < 0 {
			d = -d
		}
		if d < bestD {
			bestD, best = d, i
		}
	}
	return best
}

func nearest256(r, g, b uint8) uint8 {
	ri, gi, bi := int(r), int(g), int(b)

	cri, cgi, cbi := nearest6(ri), nearest6(gi), nearest6(bi)
	best := 16 + 36*cri + 6*cgi + cbi
	bestD := dist2(ri, gi, bi, cubeVals[cri], cubeVals[cgi], cubeVals[cbi])

	step := (ri + gi + bi) / 3
	step = (step - 8 + 5) / 10
	if step < 0 {
		step = 0
	}
	if step > 23 {
		step = 23
	}
	gv := 8 + 10*step
	if d := dist2(ri, gi, bi, gv, gv, gv); d < bestD {
		bestD, best = d, 232+step
	}

	return uint8(best)
}
