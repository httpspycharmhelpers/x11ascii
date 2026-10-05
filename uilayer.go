package main

import (
	"fmt"
	"os"
	"strings"
)

// uiLayer 是「游戏模式 ↔ 输入模式」的那层胶水：
//   - 输入模式：自己吃掉按键，方向键挪光标，回车才发给 X 程序
//   - 两种模式下都能调缩放/分辨率（以前只能退出重开）
type uiLayer struct {
	vc   *viewCtl
	ti   *textInput
	typ  uint32 // 打开/关闭输入层的按键（默认 `）
	send func(string) bool
	cp   func(string) bool
	get  func() string
	note string
}

func newUI() *uiLayer {
	u := &uiLayer{vc: newViewCtl(), ti: newTextInput(), typ: '`'}
	u.note = "按 ` 打开输入层（打字/粘贴/挪光标）；+- 缩放，1-6 切分辨率，0 自适应，f 切换拉伸，? 看说明"
	return u
}

func symRune(sym uint32) (rune, bool) {
	switch {
	case sym >= 0x01000000:
		return rune(sym - 0x01000000), true
	case sym >= 0x20 && sym < 0x7f:
		return rune(sym), true
	case sym >= 0xa0 && sym <= 0xff:
		return rune(sym), true
	case sym == 0x09:
		return '\t', true
	}
	return 0, false
}

// key 返回 true=这一层吃掉了，别再发给 X 程序。
func (u *uiLayer) key(sym uint32, ctrl, alt, shift bool) bool {
	if u.ti.isOpen() {
		return u.keyInLayer(sym, ctrl, alt, shift)
	}
	// 输入模式没开时，只有这几个键归我们，其余照旧捅给 X 程序
	if sym == u.typ && !ctrl && !alt {
		u.ti.toggle()
		u.say("输入层已打开")
		return true
	}
	if u.zoomKey(sym, ctrl, alt, shift) {
		return true
	}
	return false
}

func (u *uiLayer) keyInLayer(sym uint32, ctrl, alt, shift bool) bool {
	if sym == ksReturn {
		s, _ := u.ti.commit()
		if strings.TrimSpace(s) == "" {
			u.ti.setStatus("空内容，已忽略")
			return true
		}
		if ctrl && u.cp != nil {
			if u.cp(s) {
				u.say("已写入 X 剪贴板并发送 Ctrl+V")
			} else {
				u.say("写入 X 剪贴板失败")
			}
			return true
		}
		if u.send != nil {
			if u.send(s) {
				u.say("已发送 " + itoa(len([]rune(s))) + " 字到 X 程序")
			} else {
				u.say("发送失败")
			}
		} else {
			u.say("没有 X 源，文字留在本地")
		}
		return true
	}
	if u.ti.wantClipGet() {
		u.ti.clearWantClip()
		if u.get != nil {
			if s := u.get(); s != "" {
				u.ti.setClip(s)
			} else {
				u.ti.setStatus("X 剪贴板是空的")
			}
		} else {
			u.ti.setStatus("这个源不支持剪贴板")
		}
		return true
	}
	if sym == u.typ && !ctrl && !alt {
		u.ti.close()
		u.say("回到游戏")
		return true
	}
	if u.zoomKey(sym, ctrl, alt, shift) {
		return true
	}
	if !ctrl && !alt {
		if r, ok := symRune(sym); ok && r >= 0x20 {
			u.ti.insertRune(r)
			return true
		}
	}
	u.ti.handle(sym, ctrl, alt, shift)
	return true
}

// 缩放/分辨率热键：+- 缩放、1-6 预设、0 自适应、f 拉伸/保比例。
// ? 打印完整说明（打印到 stderr，不清屏、不影响画面）。
func (u *uiLayer) zoomKey(sym uint32, ctrl, alt, shift bool) bool {
	if ctrl || alt {
		return false
	}
	switch sym {
	case '-', '_':
		u.say(u.vc.zoomed(0.1))
		return true
	case '=', '+':
		u.say(u.vc.zoomed(-0.1))
		return true
	case '0':
		u.say(u.vc.auto())
		return true
	case 'f', 'F':
		u.say(u.vc.toggleFit())
		return true
	case '?':
		u.help()
		return true
	}
	if sym >= '1' && sym <= '6' {
		if s := u.vc.preset(int(sym - '1')); s != "" {
			u.say(s)
			return true
		}
	}
	return false
}

func (u *uiLayer) help() {
	fmt.Fprint(os.Stderr, `
缩放 / 分辨率（不用退出重开）
  + -        放大 / 缩小画面（放大=格子少字大，缩小=格子多更细腻）
  1..6       切预设分辨率：60x18 80x24 100x30 120x36 160x48 200x60
  0          跟随终端大小（自适应）
  f          保持画面比例 <-> 拉伸铺满
输入（按 `+"`"+` 开关，或 -typekey 改键）
  左右方向键  移动光标      Home/End  行首/行尾
  上下方向键  翻输入历史    Backspace/Delete  删字
  Ctrl+A/E   行首/行尾      Ctrl+U 清空   Ctrl+W 删词   Ctrl+K 删到行尾
  回车        逐字发给 X 程序（打字）
  Ctrl+回车   写进 X 剪贴板并发 Ctrl+V（粘贴）
  Ctrl+X      从 X 剪贴板读回内容到输入框
  Esc 或 `+"`"+`   回到游戏
  手机剪贴板粘贴：终端里直接 Ctrl+Shift+V，文字会进输入框（终端送来的就是字节流）
`)
}

func (u *uiLayer) say(s string) {
	if s == "" {
		return
	}
	u.note = s
	fmt.Fprintln(os.Stderr, "  "+s)
}

// statusLine 给主循环画在底部（输入层开着时画输入框，否则画最近一条提示）。
func (u *uiLayer) statusLine(cols int) string {
	if u.ti.isOpen() {
		return u.ti.render(cols)
	}
	if u.note != "" {
		n := u.note
		u.note = ""
		return "\x1b[2m" + n + "\x1b[0m"
	}
	return ""
}

func (u *uiLayer) barRows(base int) int {
	if u.ti.isOpen() {
		return base + 1
	}
	return base
}
