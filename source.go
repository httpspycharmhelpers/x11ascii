package main

type Frame struct {
	W, H, Stride int
	Pix          []byte
}

func (f *Frame) alloc(w, h int) {
	f.W, f.H = w, h
	f.Stride = w * 3
	n := f.Stride * h
	if cap(f.Pix) < n {
		f.Pix = make([]byte, n)
	} else {
		f.Pix = f.Pix[:n]
	}
}

type Source interface {
	Size() (int, int)
	Grab(*Frame) error
	Close() error
}
