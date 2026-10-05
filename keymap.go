package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// keyAction 是最终发给 X 的按键：键名 + 修饰键。
type keyAction struct {
	sym              uint32
	ctrl, alt, shift bool
}

// parseKeySpec 解析 "ctrl-tab" / "alt-left" / "f5" / "shift-a" / "space" 这类按键描述。
// 修饰键与键名可以用连字符或空格分隔，两种都认：
//
//	ctrl-tab    ctrl shift-f5    alt-left    f5    space    enter    a
func parseKeySpec(spec string) (keyAction, bool) {
	s := strings.ToLower(strings.TrimSpace(spec))
	if s == "" {
		return keyAction{}, false
	}
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '+' || r == ' ' || r == '\t'
	})
	if len(tokens) == 0 {
		return keyAction{}, false
	}
	var a keyAction
	mods := tokens[:len(tokens)-1]
	name := tokens[len(tokens)-1]
	if name == "" {
		return keyAction{}, false
	}
	for _, m := range mods {
		switch m {
		case "ctrl", "control", "c":
			a.ctrl = true
		case "alt", "meta", "m":
			a.alt = true
		case "shift", "s":
			a.shift = true
		default:
			return keyAction{}, false
		}
	}
	sym, ok := keysymByNameExtended(name)
	if !ok {
		return keyAction{}, false
	}
	a.sym = sym
	return a, true
}

// keysymByNameExtended 在 keysymByName 之外多认一些常用键名。
func keysymByNameExtended(name string) (uint32, bool) {
	switch strings.ToLower(name) {
	case "pageup", "pgup":
		return ksPageUp, true
	case "pagedown", "pgdn", "pgdown":
		return ksPageDown, true
	case "backspace", "bs":
		return ksBackSpace, true
	case "del", "delete":
		return ksDelete, true
	case "ins", "insert":
		return ksInsert, true
	case "home":
		return ksHome, true
	case "end":
		return ksEnd, true
	case "up", "down", "left", "right":
		return keysymByName(name)
	case "ctrl", "control":
		return ksControlL, true
	case "shift":
		return ksShiftL, true
	case "alt":
		return ksAltL, true
	case "space":
		return ' ', true
	}
	// 常用标点：手机上不好按、但映射里很常用的那些键
	if sym, ok := punctKeysym[name]; ok {
		return sym, true
	}
	return keysymByName(name)
}

// punctKeysym 是 keysymByName 不认、但键表里常写的一些标点键名。
var punctKeysym = map[string]uint32{
	"minus": '-', "hyphen": '-', "dash": '-', "subtract": '-',
	"dot": '.', "period": '.', "fullstop": '.',
	"comma": ',', "slash": '/', "backslash": '\\', "back_slash": '\\',
	"equal": '=', "equals": '=', "plus_sign": '+',
	"semicolon": ';', "colon": ':', "quote": '\'', "apostrophe": '\'',
	"grave": '`', "backquote": '`', "tilde": '~',
	"bracketleft": '[', "bracketright": ']', "less": '<', "greater": '>',
	"question": '?', "exclam": '!', "at": '@', "numbersign": '#',
	"dollar": '$', "percent": '%', "caret": '^', "underscore": '_',
	"ampersand": '&', "asterisk": '*', "bar": '|', "asciitilde": '~',
}

// parseKeymapLine 解析一行 "t = ctrl-tab" 或 "a,b = ctrl-alt-delete"。
// 左边是"你在手机上按的键"，右边是"发给被包裹程序的实际按键"。
func parseKeymapLine(line string) (map[uint32]keyAction, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
		return nil, false
	}
	i := strings.IndexAny(line, "=:")
	if i < 0 {
		return nil, false
	}
	lhs, rhs := strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:])
	act, ok := parseKeySpec(rhs)
	if !ok {
		return nil, false
	}
	out := map[uint32]keyAction{}
	for _, k := range strings.Split(lhs, ",") {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if sym, ok := keysymByNameExtended(k); ok {
			out[sym] = act
			// 手机键盘有时给的是大写（Shift 状态），大小写都认
			if sym >= 'a' && sym <= 'z' {
				out[sym-32] = act
			} else if sym >= 'A' && sym <= 'Z' {
				out[sym+32] = act
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func defaultKeymapPath() string {
	if d, err := os.UserConfigDir(); err == nil && d != "" {
		return filepath.Join(d, "x11ascii", "keymap")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "x11ascii", "keymap")
}

// loadKeymapFile 读 ~/.config/x11ascii/keymap；文件不存在不算错误。
func loadKeymapFile(path string) (map[uint32]keyAction, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	m := map[uint32]keyAction{}
	n := 0
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		if km, ok := parseKeymapLine(sc.Text()); ok {
			for k, v := range km {
				m[k] = v
			}
			n++
		} else if t := strings.TrimSpace(sc.Text()); t != "" &&
			!strings.HasPrefix(t, "#") && !strings.HasPrefix(t, ";") {
			return nil, 0, fmt.Errorf("第 %d 行看不懂: %q", line, t)
		}
	}
	return m, n, sc.Err()
}

// keymapSummary 把键表整理成一行行说明，启动时打给用户看，方便确认映射对不对。
func keymapSummary(m map[uint32]keyAction) []string {
	if len(m) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for sym, act := range m {
		var sb strings.Builder
		if act.ctrl {
			sb.WriteString("ctrl-")
		}
		if act.alt {
			sb.WriteString("alt-")
		}
		if act.shift {
			sb.WriteString("shift-")
		}
		sb.WriteString(symName(act.sym))
		line := symName(sym) + " → " + sb.String()
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	sortStrings(out)
	return out
}

func symName(sym uint32) string {
	if s, ok := keysymToName[sym]; ok {
		return s
	}
	if sym >= 0x20 && sym < 0x7f {
		return strconv.QuoteRune(rune(sym))
	}
	return "0x" + strconv.FormatUint(uint64(sym), 16)
}

var keysymToName = map[uint32]string{
	ksTab: "tab", ksReturn: "enter", ksEscape: "esc", ksBackSpace: "backspace",
	' ': "space", ksUp: "up", ksDown: "down", ksLeft: "left", ksRight: "right",
	ksHome: "home", ksEnd: "end", ksPageUp: "pgup", ksPageDown: "pgdn",
	ksInsert: "ins", ksDelete: "del", ksControlL: "ctrl", ksShiftL: "shift", ksAltL: "alt",
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
