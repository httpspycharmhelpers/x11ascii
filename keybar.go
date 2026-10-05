package main

// 屏幕底部的“虚拟键盘条”。
//
// 手机上能发出来的键很有限：软键盘没有 Ctrl/Alt/F 键，Termux 扩展键也只有
// ESC/TAB/CTRL/ALT/方向键这几个，而游戏要的偏偏是 Ctrl/Alt 组合键和“按住不放”。
// 所以这里把常用键直接画在画面下方，点哪发哪：
//
//   - 普通键（ESC/TAB/SPACE/方向键/ENTER…）点一下就发出去；
//   - CTRL/ALT/SHIFT 是“锁定键”，点一下打开（高亮），再点一下关闭，
//     打开期间点别的键都会带上这个修饰键，单手也能打出组合键；
//   - FIRE 点一下发“开火键”（默认 Ctrl，按住 -ctrl-hold 毫秒），解决
//     -ctrl 需要一个手机上按不出来的字母键的问题。

type kbKey struct {
	label  string
	sym    uint32 // 普通键的 keysym
	mod    byte   // 'c'/'a'/'s'：可锁定的修饰键
	action string // "fire"：开火键
}

type kbItem struct {
	kbKey
	row, col, span int
}

type KeyBar struct {
	rows    int
	fireSym uint32
	keys    []kbKey
	items   []kbItem // 布局结果
	latch   map[byte]bool
	dirty   bool
}

func newKeyBar(rows int, fireSym uint32) *KeyBar {
	kb := &KeyBar{
		rows:    rows,
		fireSym: fireSym,
		latch:   map[byte]bool{},
		keys: []kbKey{
			{label: "ESC", sym: ksEscape},
			{label: "TAB", sym: ksTab},
			{label: "CTRL", mod: 'c'},
			{label: "ALT", mod: 'a'},
			{label: "SHIFT", mod: 's'},
			{label: "SPACE", sym: ' '},
			{label: "FIRE", action: "fire"},
			{label: "ENTER", sym: ksReturn},
			{label: "↑", sym: ksUp},
			{label: "↓", sym: ksDown},
			{label: "←", sym: ksLeft},
			{label: "→", sym: ksRight},
			{label: "PGUP", sym: ksPageUp},
			{label: "PGDN", sym: ksPageDown},
			{label: "HOME", sym: ksHome},
			{label: "END", sym: ksEnd},
			{label: "1", sym: '1'},
			{label: "2", sym: '2'},
			{label: "3", sym: '3'},
			{label: "BKSP", sym: ksBackSpace},
		},
	}
	return kb
}

// layout 按终端宽度排布按键（放不下就换到下一行）。
func (kb *KeyBar) layout(cols int) {
	kb.items = kb.items[:0]
	if kb.rows <= 0 || cols < 8 {
		return
	}
	row, col := 0, 1
	for _, k := range kb.keys {
		span := len([]rune(k.label)) + 1
		if col > 1 && col+span-1 > cols {
			row++
			col = 1
		}
		if row >= kb.rows || col+span-1 > cols {
			break
		}
		kb.items = append(kb.items, kbItem{kbKey: k, row: row, col: col, span: span})
		col += span
	}
	kb.dirty = true
}

// hit 判断触点落在哪个键上（col/row 为 1-based 单元格坐标，imgRows 为画面行数）。
func (kb *KeyBar) hit(col, row, imgRows int) *kbItem {
	if kb == nil || kb.rows <= 0 || row <= imgRows {
		return nil
	}
	r := row - imgRows - 1
	if r < 0 || r >= kb.rows {
		return nil
	}
	for i := range kb.items {
		it := &kb.items[i]
		if it.row == r && col >= it.col && col < it.col+it.span {
			return it
		}
	}
	return nil
}

// toggle 切换锁定键，返回切换后的状态（普通键返回 false）。
func (kb *KeyBar) toggle(it *kbItem) bool {
	if it.mod == 0 {
		return false
	}
	kb.latch[it.mod] = !kb.latch[it.mod]
	kb.dirty = true
	return kb.latch[it.mod]
}

func (kb *KeyBar) mods() (ctrl, alt, shift bool) {
	return kb.latch['c'], kb.latch['a'], kb.latch['s']
}

// render 生成按键条文本（每行一条），锁定中的修饰键用黄底高亮。
func (kb *KeyBar) render(cols int) []string {
	lines := make([]string, kb.rows)
	for r := 0; r < kb.rows; r++ {
		var b []byte
		col := 1
		for i := range kb.items {
			it := &kb.items[i]
			if it.row != r || it.col < col {
				continue
			}
			if it.col > col {
				b = append(b, spaces(it.col-col)...)
				col = it.col
			}
			switch {
			case it.mod != 0 && kb.latch[it.mod]:
				b = append(b, "\x1b[43m\x1b[30m"...)
			case it.action == "fire":
				b = append(b, "\x1b[41m\x1b[97m"...)
			default:
				b = append(b, "\x1b[7m"...)
			}
			b = append(b, ' ')
			b = append(b, it.label...)
			if pad := it.span - 1 - len([]rune(it.label)); pad > 0 {
				b = append(b, spaces(pad)...)
			}
			b = append(b, "\x1b[0m"...)
			col += it.span
		}
		if pad := cols - col + 1; pad > 0 {
			b = append(b, spaces(pad)...)
		}
		lines[r] = string(b)
	}
	kb.dirty = false
	return lines
}

func spaces(n int) []byte {
	if n <= 0 {
		return nil
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return b
}
