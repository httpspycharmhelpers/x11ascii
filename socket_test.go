package main

import "testing"

func TestSocketCandidates(t *testing.T) {
	t.Setenv("PREFIX", "/data/data/com.termux/files/usr")
	t.Setenv("TMPDIR", "")
	t.Setenv("HOME", "/home/x")
	t.Setenv("X11_SOCKET", "")
	got := socketCandidates("0")
	want := "/data/data/com.termux/files/usr/tmp/.X11-unix/X0"
	for _, p := range got {
		if p == want {
			return
		}
	}
	t.Fatalf("候选 %v 不含 %s", got, want)
}
