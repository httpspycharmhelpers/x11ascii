package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// resizeTTY 用 TIOCSWINSZ 真的把终端改成 cols x rows（Termux 会立刻重排画面）。
// 这才是「分辨率」的意思：格子数变了，每个字符占的物理像素也跟着变。
// 以前只改渲染格子数、不动终端，所以放大后字还是原来那么大，看着像没放大。
func resizeTTY(cols, rows int) bool {
	fd := int(os.Stdout.Fd())
	ws := struct{ Row, Col, X, Y uint16 }{
		uint16(rows), uint16(cols), uint16(cols * 8), uint16(rows * 16),
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(fd), uintptr(syscall.TIOCSWINSZ), uintptr(unsafe.Pointer(&ws)))
	return errno == 0
}

func main() {
	os.Exit(run())
}

func parseSize(s string) (w, h int, err error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'x' || r == 'X' || r == '*' })
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("尺寸格式应为 WxH: %q", s)
	}
	w, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, err
	}
	h, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, err
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("尺寸必须为正: %q", s)
	}
	return w, h, nil
}

func buildSource(kind, display, region, windowSel, command, srcsize string, renderOn, fit bool, holdMS, ctrlHoldMS int, verbose bool) (Source, error) {
	logf := func(format string, a ...any) {
		if verbose {
			fmt.Fprintf(os.Stderr, format, a...)
		}
	}
	switch kind {
	case "test":
		w, h, err := parseSize(srcsize)
		if err != nil {
			return nil, err
		}
		logf("source: 内置测试图案 %dx%d\n", w, h)
		return newPatternSource(w, h), nil
	case "cmd":
		if command == "" {
			return nil, errors.New("cmd 源需要 -cmd")
		}
		w, h, err := parseSize(srcsize)
		if err != nil {
			return nil, err
		}
		logf("source: 命令管道 %dx%d: %s\n", w, h, command)
		return newCmdSource(command, w, h)
	case "x11":
		s, err := newX11Source(display, region, windowSel, renderOn, fit, holdMS)
		if err == nil && ctrlHoldMS > 0 {
			s.modHoldMS = ctrlHoldMS
		}
		if err != nil {
			return nil, err
		}
		w, h := s.Size()
		logf("source: X11 display=%q region=%q window=%q %dx%d\n", x11Display(display), region, windowSel, w, h)
		if renderOn && s.rnd {
			logf("source: X11 服务端 RENDER 缩放已启用\n")
		} else if s.renderErr != nil {
			logf("source: X11 服务端缩放不可用(%v)，用 XGetImage\n", s.renderErr)
		}
		return s, nil
	case "auto":
		if display != "" || os.Getenv("DISPLAY") != "" {
			s, err := newX11Source(display, region, windowSel, renderOn, fit, holdMS)
			if err == nil && ctrlHoldMS > 0 {
				s.modHoldMS = ctrlHoldMS
			}
			if err == nil {
				w, h := s.Size()
				logf("source: X11(自动) display=%q %dx%d\n", x11Display(display), w, h)
				if renderOn && s.rnd {
					logf("source: X11 服务端 RENDER 缩放已启用\n")
				}
				return s, nil
			}
			logf("X11 连接失败(%v)，尝试回退\n", err)
		}
		if command != "" {
			w, h, err := parseSize(srcsize)
			if err == nil {
				logf("source: 命令管道(自动) %dx%d\n", w, h)
				return newCmdSource(command, w, h)
			}
		}
		w, h, _ := parseSize(srcsize)
		logf("source: 内置测试图案(回退) %dx%d\n", w, h)
		return newPatternSource(w, h), nil
	default:
		return nil, fmt.Errorf("未知 -source %q（auto|x11|cmd|test）", kind)
	}
}

func x11Display(display string) string {
	if display != "" {
		return display
	}
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	return "(未设置)"
}

func readQuit(fd int, quit chan struct{}, once *sync.Once) {
	buf := make([]byte, 1)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil || n == 0 {
			return
		}
		switch buf[0] {
		case 'q', 'Q', 3:
			once.Do(func() { close(quit) })
			return
		}
	}
}

func run() int {
	fw := flag.Int("w", 0, "输出列数（0=终端宽度）")
	fh := flag.Int("h", 0, "输出行数（0=终端高度）")
	source := flag.String("source", "auto", "抓屏源：auto|x11|cmd|test")
	display := flag.String("display", "", "X display，如 :0（空则用 $DISPLAY）")
	region := flag.String("region", "", "抓屏区域 WxH 或 WxH+X+Y")
	windowSel := flag.String("window", "", "只抓单个窗口：auto=最大窗口、name:子串、0xID（空=整个屏幕）")
	command := flag.String("cmd", "", "cmd 源的命令，stdout 输出 rgb24 原始帧")
	srcsize := flag.String("srcsize", "320x200", "源帧尺寸 WxH（cmd/test 用）")
	fps := flag.Int("fps", 30, "目标帧率")
	frames := flag.Int("frames", 0, "渲染多少帧后退出（0=不限）")
	once := flag.Bool("once", false, "只渲染一帧后退出")
	use256 := flag.Bool("256", false, "用 ANSI-256 调色板量化颜色（省带宽、兼容老终端）")
	renderOn := flag.Bool("render", true, "用 XRender 在服务端缩放整屏（大幅省带宽，失败自动回退 XGetImage）")
	idleFPS := flag.Int("idle-fps", 5, "画面无变化时降到的帧率（0=不降帧）")
	fit := flag.Bool("fit", true, "保持源画面宽高比（letterbox 留黑边），避免被拉伸变形")
	holdMS := flag.Int("hold", 60, "input 模式下按键“按住”时长（毫秒），利于游戏识别移动")
	ctrlHold := flag.Int("ctrl-hold", 250, "-ctrl 冒充的 Ctrl 按住时长（毫秒），游戏要“按住持续射击”")
	escWait := flag.Int("esc-wait", 150, "ESC 后等待转义序列后续字节的毫秒数（方向键/F 键/扩展键靠它，太小会被拆成单键）")
	quitKey := flag.String("quit-key", "", "单键退出（keysym 名，如 F12/q）；不设=只用 Ctrl+C、双 Esc、连点")
	quitTaps := flag.Int("quit-taps", 3, "快速连点几下退出（纯触屏唯一可靠的退路）；0=关闭")
	ctrlAs := flag.String("ctrl", "", "把普通键当成长按 Ctrl（逗号分隔，如 \"a\" 或 \"q,w\"）。终端不会上报「单独按 Ctrl」，游戏开枪/斜跑要用")
	var maps multiFlag
	flag.Var(&maps, "map", "自定义键位，可重复：-map 't=ctrl-tab' -map 'j=down,k=up'（手机上的键 → 程序实际收到的组合键）")
	resizeTerm := flag.Bool("resize-term", true, "缩放/切分辨率时真的把终端改成对应尺寸（像素跟着变大变小；-resize-term=false 只改画面不动终端）")
	keybarRows := flag.Int("keybar", -1, "画面下方虚拟键盘条占几行（-1=input 模式默认 2，0=关闭）")
	typeKey := flag.String("typekey", "`", "打开/关闭输入层的按键（输入层里可以打字、挪光标、粘贴；默认反引号）")
	fireKey := flag.String("fire-key", "ctrl", "虚拟键盘 FIRE 键发出的组合键（默认 ctrl=长按 Ctrl 开火；游戏用别的键就改这里，如 space/alt）")
	keymapFile := flag.String("keymap", "", "键位表文件（默认 ~/.config/x11ascii/keymap，存在才读）；每行 \"键 = 组合键\"")
	verbose := flag.Bool("v", true, "输出源/显示等诊断信息到 stderr")
	probe := flag.Bool("probe", false, "连接 X11 并打印 screen/depth/抓帧自检后退出")
	input := flag.Bool("input", false, "把终端键盘/鼠标事件回传给 X（XTest，仅 x11 源；Ctrl+C 退出）")
	mouse := flag.Bool("mouse", true, "input 模式下启用终端鼠标报告")
	execCmd := flag.String("exec", "", "启动并包裹一个命令（如 dosbox ...），退出时结束它")
	sessionOn := flag.Bool("session", false, "一条命令跑完整流程：开 X11（可选经典桌面）→ 起 -exec 程序 → 定位其窗口 → 渲染；退出时按 程序→桌面→X11 全部关掉")
	desktop := flag.String("desktop", "xfwm4", "-session 时的桌面/窗口管理器命令；空=不启（注意：没有 WM 时很多程序不会真正绘制，只会剩一片空白/花屏）。填 \"dbus-launch --exit-with-session xfce4-session\" 可用完整 XFCE 桌面")
	x11Args := flag.String("x11-args", "-legacy-drawing -force-bgra", "-session 时传给 termux-x11 的参数")
	waitSec := flag.Int("wait", 0, "首次抓屏前先等几秒，给被启动的程序留出绘制时间")
	flag.Parse()

	if *probe {
		if err := x11Probe(*display); err != nil {
			fmt.Fprintln(os.Stderr, "探测失败:", err)
			return 1
		}
		return 0
	}

	if *sessionOn {
		s, err := startX11Session(sessionOpts{display: *display, x11Args: *x11Args})
		if err != nil {
			fmt.Fprintln(os.Stderr, "启动 X11 会话失败:", err)
			return 1
		}
		defer s.stop() // 后注册 → 先执行；程序(child)会先于它被停掉
		*display = s.display
		how := "已启动"
		if !s.owned {
			how = "复用已有"
		}
		fmt.Fprintf(os.Stderr, "X11: display=%s（%s）\n", s.display, how)
		if strings.TrimSpace(*desktop) != "" {
			if err := s.startDesktop(*desktop); err != nil {
				fmt.Fprintln(os.Stderr, "警告:", err)
			} else {
				fmt.Fprintln(os.Stderr, "桌面: 已就绪")
			}
		}
	}

	var child *exec.Cmd
	if *execCmd != "" {
		d := *display
		if d == "" {
			d = os.Getenv("DISPLAY")
		}
		var err error
		child, err = startChild(*execCmd, d)
		if err != nil {
			fmt.Fprintln(os.Stderr, "启动命令失败:", err)
			return 1
		}
		defer stopChild(child)
		// 没指定 -window 时，默认只显示我们启动的这个程序的窗口（“定位那个窗口”）。
		if *windowSel == "" && child.Process != nil {
			*windowSel = "pid:" + strconv.Itoa(child.Process.Pid)
		}
	}

	if *once {
		*frames = 1
	}
	if *fps < 1 {
		*fps = 1
	}
	mode := modeTrue
	if *use256 {
		mode = mode256
	}

	stdin := int(os.Stdin.Fd())
	tty := isTTY(stdin)
	if !tty && *frames == 0 {
		*frames = 1
	}

	ui := newUI()
	if *typeKey != "" {
		if r, ok := symRune(uint32((*typeKey)[0])); ok {
			ui.typ = uint32(r)
		}
	}
	cols, rows := *fw, *fh
	if tty {
		if c, r, ok := termSize(stdin); ok {
			if cols == 0 {
				cols = c
			}
			if rows == 0 {
				rows = r
			}
		}
	}
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}

	interactive := tty && !*once
	manualSize := false
	// canvas 是 convert 的输出，placed 是居中摆放的输出。必须分开：
	// 以前摆完直接 canvas = screen，两块内存共用同一段 Pix，下一帧
	// convert 写进 screen、blitCenter 又拿 screen 当 dst 先清空再自拷贝 → 全黑。
	// 缩放改终端尺寸后最容易触发（尺寸一变就走居中分支）。
	var canvas, placed Canvas

	// 虚拟键盘条：吃掉底部若干行，剩下的才是画面
	barRows := *keybarRows
	if barRows < 0 {
		barRows = 0
		if interactive && *source == "x11" {
			barRows = 2
		}
	}
	imgRows := rows - barRows
	if imgRows < 4 {
		imgRows = 4
	}
	ui.vc.syncTerm(cols, rows, barRows)
	ui.vc.setRestore(cols, rows)

	// 缩放/切分辨率 → 终端也跟着变尺寸（像素随之变大变小）。
	// 回调里同时更新 cols/rows，下一帧直接按新尺寸画，不用等 SIGWINCH。
	if tty && *resizeTerm {
		ui.vc.resizeTerm = true
		ui.vc.onSize = func(c, r int) {
			if c < 20 {
				c = 20
			}
			if r < 8 {
				r = 8
			}
			if resizeTTY(c, r) {
				fmt.Fprintf(os.Stderr, "终端尺寸 %dx%d\n", c, r)
			}
			cols, rows = c, r
			barRows = ui.barRows(barRows)
			imgRows = rows - barRows
			if imgRows < 4 {
				imgRows = 4
			}
			ui.vc.syncTerm(cols, rows, barRows)
		}
	}

	fitOn := *fit
	src, err := buildSource(*source, *display, *region, *windowSel, *command, *srcsize, *renderOn, fitOn, *holdMS, *ctrlHold, *verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	defer src.Close()

	if xs, ok := src.(*x11Source); ok {
		xs.setTermSize(cols, imgRows)
		if xs.windowMode() {
			if err := xs.syncWindow(); err != nil {
				fmt.Fprintln(os.Stderr, "定位窗口:", err)
			}
		}
	}

	var xsrc *x11Source
	if *input {
		if xs, ok := src.(*x11Source); ok {
			if err := xs.initInput(); err != nil {
				fmt.Fprintln(os.Stderr, "启用输入失败:", err)
			} else {
				xsrc = xs
			}
		} else {
			fmt.Fprintln(os.Stderr, "警告: -input 仅对 x11 源有效")
		}
	}

	// 先进 alt screen 之前先抓一帧：失败时错误信息在普通终端上可见
	var frame Frame
	if *waitSec > 0 {
		fmt.Fprintf(os.Stderr, "等待 %d 秒让程序绘制…\n", *waitSec)
		time.Sleep(time.Duration(*waitSec) * time.Second)
	}
	if err := src.Grab(&frame); err != nil {
		fmt.Fprintln(os.Stderr, "首次抓屏失败:", err)
		return 1
	}

	var rs *rawState
	if interactive {
		rs, err = makeRaw(stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "无法进入原始模式:", err)
			return 1
		}
		defer rs.restore()
		os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")
		defer os.Stdout.WriteString("\x1b[?25h\x1b[?1049l")
	}

	quit := make(chan struct{})
	var onceQuit sync.Once
	// 最近一次鼠标/触摸时间：避免焦点被周期性地抢走
	var lastMouse atomic.Int64
	// 最近一次点击位置：触屏没有指针，要在画面上反白出点的地方
	var tapCol, tapRow, tapAt atomic.Int64
	var drawnTap int64
	lastMouseAt := func() time.Duration {
		v := lastMouse.Load()
		if v == 0 {
			return time.Hour // 从没点过：视为"很久没点"，允许程序自己设焦点
		}
		return time.Since(time.Unix(0, v))
	}
	// 键位表：先读配置文件，再用 -map 覆盖/追加。启动时打出来方便确认。
	keymap := map[uint32]keyAction{}
	if *keymapFile == "" {
		*keymapFile = defaultKeymapPath()
	}
	if km, n, err := loadKeymapFile(*keymapFile); err == nil {
		for k, v := range km {
			keymap[k] = v
		}
		if n > 0 && *verbose {
			fmt.Fprintf(os.Stderr, "键位表 %s: %d 条\n", *keymapFile, n)
		}
	}
	for _, spec := range maps {
		km, ok := parseKeymapLine(spec)
		if !ok {
			fmt.Fprintf(os.Stderr, "看不懂 -map %q（应形如 t=ctrl-tab）\n", spec)
			continue
		}
		for k, v := range km {
			keymap[k] = v
		}
	}
	if *verbose {
		for _, l := range keymapSummary(keymap) {
			fmt.Fprintf(os.Stderr, "键位映射: %s\n", l)
		}
	}

	var kb *KeyBar
	if barRows > 0 {
		var fireSym uint32 = ksControlL
		if *fireKey != "" && *fireKey != "ctrl" {
			if s, ok := keysymByNameExtended(*fireKey); ok {
				fireSym = s
			} else {
				fmt.Fprintf(os.Stderr, "无法识别 -fire-key %q，用 ctrl\n", *fireKey)
			}
		}
		kb = newKeyBar(barRows, fireSym)
		kb.layout(cols)
	}
	inputOn := interactive && xsrc != nil
	// 界面层（输入层 + 缩放热键）在任何交互模式下都要能用，
	// 所以没有 X 源、也没开 -input 时也照样读键盘，只是把按键丢掉。
	if interactive && !inputOn {
		p := &inputPump{
			src:       fdSource{fd: stdin},
			sink:      discardSink{},
			onQuit:    func() { onceQuit.Do(func() { close(quit) }) },
			quitTaps:  *quitTaps,
			escWaitMS: *escWait,
			hook:      ui.key,
		}
		go p.run()
		ui.vc.syncTerm(cols, rows, barRows)
		ui.say("缩放/分辨率热键可用（+- 缩放、1-6 预设、0 自适应、f 拉伸、? 说明）；按 ` 打开输入层")
	} else if inputOn {
		if *mouse {
			os.Stdout.WriteString("\x1b[?1000h\x1b[?1002h\x1b[?1006h")
			defer os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l")
		}
		// 输入层的三件事：打字/逐字发送、剪贴板双向、缩放热键
		ui.send = func(t string) bool { return xsrc.typeText(t) }
		ui.cp = func(t string) bool {
			if !xsrc.setClipboard(t) {
				return false
			}
			xsrc.key(ksControlL, false, false, false)
			xsrc.key('v', false, false, false)
			xsrc.key(ksControlL, false, false, true)
			return true
		}
		ui.get = func() string { return xsrc.getClipboard() }
		pump := &inputPump{
			src:       fdSource{fd: stdin},
			sink:      xsrc,
			hook:      ui.key,
			onQuit:    func() { onceQuit.Do(func() { close(quit) }) },
			quitTaps:  *quitTaps,
			escWaitMS: *escWait,
			ctrlKeys:  parseCtrlKeys(*ctrlAs),
			remap:     keymap,
			onMouse:   func() { lastMouse.Store(time.Now().UnixNano()) },
			onTap: func(col, row int) bool {
				if it := kb.hit(col, row, imgRows); it != nil {
					if kb.toggle(it) {
						return true
					}
					ctrl, alt, shift := kb.mods()
					switch {
					case it.action == "fire":
						// 开火键：直接注入长按（游戏要的是“按住”）
						xsrc.key(kb.fireSym, ctrl, alt, shift)
					case it.sym != 0:
						xsrc.key(it.sym, ctrl, alt, shift)
					}
					return true
				}
				tapAt.Store(time.Now().UnixNano())
				tapCol.Store(int64(col))
				tapRow.Store(int64(row))
				return false
			},
		}
		if *quitKey != "" {
			if sym, ok := keysymByName(*quitKey); ok {
				pump.quitSym = sym
			} else {
				fmt.Fprintf(os.Stderr, "无法识别 -quit-key %q，已忽略\n", *quitKey)
			}
		}
		if *quitTaps > 0 {
			if *verbose {
				fmt.Fprintf(os.Stderr, "退出方式: Ctrl+C / 双击 Esc / 快速连点 %d 下 / 关闭目标窗口 / 另一终端 pkill -INT -f x11ascii\n", *quitTaps)
			}
		}
		go pump.run()
		xsrc.setTermSize(cols, rows)
	} else if tty && !interactive {
		go readQuit(stdin, quit, &onceQuit)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	go func() {
		select {
		case <-sigs:
			onceQuit.Do(func() { close(quit) })
		case <-quit:
		}
	}()

	var needClear int32
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			atomic.StoreInt32(&needClear, 1)
		}
	}()

	out := newWriter(os.Stdout, mode)
	missing := 0

	interval := time.Second / time.Duration(*fps)
	idleInterval := interval
	if *idleFPS > 0 {
		if d := time.Second / time.Duration(*idleFPS); d > idleInterval {
			idleInterval = d
		}
	}

	for i := 0; ; i++ {
		select {
		case <-quit:
			return 0
		default:
		}
		if !interactive && *frames > 0 && i >= *frames {
			break
		}

		// 焦点策略：
		// -window / -exec 锁定窗口模式：周期性把焦点交回锁定窗口。必须无条件抢回来——
		// 之前要求「用户 2 秒没碰屏幕」，可手机上一直在点虚拟键盘条，于是焦点永远抢不回来，
		// 窗口管理器一抢走焦点，XTest 注入的按键就石沉大海，表现为「看得到、操作不了」。
		// 整个桌面模式（没有 -window）：用户刚点过屏幕就不抢，靠点击切换窗口。
		if inputOn && i%30 == 0 {
			if xsrc.capLocked() {
				xsrc.focusLocked()
			} else if child != nil && !xsrc.windowMode() && lastMouseAt() > 2*time.Second {
				xsrc.FocusBiggest()
			}
		}

		if tty {
			if c, r, ok := termSize(stdin); ok {
				if *fw == 0 && !manualSize {
					cols = c
				}
				if *fh == 0 && !manualSize {
					rows = r
				}
			}
		}
		barRows = ui.barRows(barRows)
		imgRows = rows - barRows
		if imgRows < 4 {
			imgRows = 4
		}
		ui.vc.syncTerm(cols, rows, barRows)
		imgCols, imgH, offX, offY := ui.vc.img()
		manualSize = ui.vc.manualSize()
		// fit 可以用 f 键随时切，不用退出重开
		if xs, ok := src.(*x11Source); ok {
			xs.setFit(ui.vc.fitOn())
		}
		if xs, ok := src.(*x11Source); ok {
			xs.setTermSize(imgCols, imgH)
			if xs.windowMode() {
				if i%15 == 0 {
					_ = xs.syncWindow()
				}
				// 目标窗口被关掉了（浏览器点 X、dosbox 自己退出）→ 自动回 Termux。
				// 这是纯触屏用户最自然的退出方式，不用非得找快捷键或另开终端 kill。
				if xs.capLocked() {
					if !xs.capAlive() {
						missing++
						if missing >= 4 { // 连续约 1 秒不在才算真没了，避免窗口重建时闪退
							fmt.Fprintln(os.Stderr, "目标窗口已关闭，退出。")
							return 0
						}
					} else {
						missing = 0
					}
				}
			}
		}

		if err := src.Grab(&frame); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return 0
			}
			fmt.Fprintln(os.Stderr, "抓屏错误:", err)
			return 1
		}
		convert(&frame, imgCols, imgH, &canvas)
		draw := &canvas
		// 画面比终端小的时候居中摆放，四周留黑；比终端大的时候居中裁掉
		if imgCols != cols || imgH != rows-offY {
			blitCenter(&canvas, &placed, cols, rows, offX, offY)
			draw = &placed
		}
		// 触屏没有指针，把最后点到的位置反白出来（纯触屏操作浏览器/桌面必需）
		if ts := tapAt.Load(); ts != drawnTap {
			drawnTap = ts
			draw.setMark(int(tapCol.Load()), int(tapRow.Load()))
		}
		if ts := tapAt.Load(); ts != 0 && time.Since(time.Unix(0, ts)) < 1800*time.Millisecond {
			draw.drawMark()
		}
		if interactive && atomic.SwapInt32(&needClear, 0) == 1 {
			if xs, ok := src.(*x11Source); ok {
				xs.refreshGeometry()
			}
			out.Clear()
		}
		if err := out.Write(draw); err != nil {
			return 1
		}
		if kb != nil && kb.dirty {
			if err := out.WriteStatus(kb.render(cols)); err != nil {
				return 1
			}
		}
		if line := ui.statusLine(cols); line != "" {
			if err := out.WriteStatus([]string{line}); err != nil {
				return 1
			}
		}
		if *frames > 0 && i+1 >= *frames {
			break
		}
		if !interactive {
			continue
		}
		wait := interval
		if idleInterval > interval {
			if xs, ok := src.(*x11Source); ok && xs.Idle() {
				wait = idleInterval
			}
		}
		select {
		case <-quit:
			return 0
		case <-time.After(wait):
		}
	}
	return 0
}

// multiFlag 收集可重复的 -map 参数。
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }

func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}
