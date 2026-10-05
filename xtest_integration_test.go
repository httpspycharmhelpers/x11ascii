//go:build integration

package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/BurntSushi/xgb/xproto"
)

// 需要真实 X server 和 xev：
//
//	DISPLAY=:0 xev > xev.log 2>&1 &
//	# 取 "Outer window is 0x...." 的值
//	DISPLAY=:0 XEV_WINDOW=0x.... go test -tags integration -run TestXTestInputToXev -v
//
// 然后在 xev.log 里应能看到对应 keysym。
func TestXTestInputToXev(t *testing.T) {
	disp := os.Getenv("DISPLAY")
	winStr := os.Getenv("XEV_WINDOW")
	if disp == "" || winStr == "" {
		t.Skip("需要 DISPLAY 和 XEV_WINDOW（如 0x1800001）")
	}
	var win uint32
	if _, err := fmt.Sscanf(strings.TrimPrefix(winStr, "0x"), "%x", &win); err != nil {
		t.Fatalf("解析 XEV_WINDOW: %v", err)
	}
	s, err := newX11Source(disp, "", "", false, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.initInput(); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.symMap['z']; !ok {
		t.Fatal("keyboard map 缺少 'z'")
	}
	xproto.SetInputFocus(s.conn, 2, xproto.Window(win), xproto.TimeCurrentTime)
	s.conn.Sync()
	keys := []struct {
		sym           uint32
		ctrl, alt, sh bool
	}{
		{'z', false, false, false},
		{'A', false, false, false},
		{ksUp, false, false, false},
		{ksF1 + 4, false, false, false},
		{'a', true, false, false},
	}
	for _, k := range keys {
		s.key(k.sym, k.ctrl, k.alt, k.sh)
		s.conn.Sync()
	}
}
