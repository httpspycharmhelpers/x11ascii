package main

type patternSource struct {
	w, h int
	t    int
}

func newPatternSource(w, h int) *patternSource {
	return &patternSource{w: w, h: h}
}

func (p *patternSource) Size() (int, int) { return p.w, p.h }
func (p *patternSource) Close() error     { return nil }

func (p *patternSource) Grab(f *Frame) error {
	f.alloc(p.w, p.h)
	t := byte(p.t)
	dx := max(1, p.w-1)
	dy := max(1, p.h-1)
	ds := max(1, p.w+p.h-2)
	for y := 0; y < p.h; y++ {
		row := f.Pix[y*f.Stride : y*f.Stride+p.w*3]
		for x := 0; x < p.w; x++ {
			row[x*3+0] = byte(x*255/dx) ^ (t * 3)
			row[x*3+1] = byte(y*255/dy) ^ (t * 5)
			row[x*3+2] = byte((x+y)*255/ds) ^ (t * 7)
		}
	}
	p.t++
	return nil
}
