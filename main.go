package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

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

func buildSource(kind, display, region, command, srcsize string, verbose bool) (Source, error) {
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
		s, err := newX11Source(display, region)
		if err != nil {
			return nil, err
		}
		w, h := s.Size()
		logf("source: X11 display=%q region=%q %dx%d\n", x11Display(display), region, w, h)
		return s, nil
	case "auto":
		if display != "" || os.Getenv("DISPLAY") != "" {
			s, err := newX11Source(display, region)
			if err == nil {
				w, h := s.Size()
				logf("source: X11(自动) display=%q %dx%d\n", x11Display(display), w, h)
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
	command := flag.String("cmd", "", "cmd 源的命令，stdout 输出 rgb24 原始帧")
	srcsize := flag.String("srcsize", "320x200", "源帧尺寸 WxH（cmd/test 用）")
	fps := flag.Int("fps", 30, "目标帧率")
	frames := flag.Int("frames", 0, "渲染多少帧后退出（0=不限）")
	once := flag.Bool("once", false, "只渲染一帧后退出")
	use256 := flag.Bool("256", false, "用 ANSI-256 调色板量化颜色（省带宽、兼容老终端）")
	verbose := flag.Bool("v", true, "输出源/显示等诊断信息到 stderr")
	probe := flag.Bool("probe", false, "连接 X11 并打印 screen/depth/抓帧自检后退出")
	input := flag.Bool("input", false, "把终端键盘/鼠标事件回传给 X（XTest，仅 x11 源；Ctrl+C 退出）")
	mouse := flag.Bool("mouse", true, "input 模式下启用终端鼠标报告")
	flag.Parse()

	if *probe {
		if err := x11Probe(*display); err != nil {
			fmt.Fprintln(os.Stderr, "探测失败:", err)
			return 1
		}
		return 0
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

	src, err := buildSource(*source, *display, *region, *command, *srcsize, *verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		return 1
	}
	defer src.Close()

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
	var canvas Canvas
	if err := src.Grab(&frame); err != nil {
		fmt.Fprintln(os.Stderr, "首次抓屏失败:", err)
		return 1
	}

	var rs *rawState
	interactive := tty && !*once
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
	inputOn := interactive && xsrc != nil
	if inputOn {
		if *mouse {
			os.Stdout.WriteString("\x1b[?1000h\x1b[?1002h\x1b[?1006h")
			defer os.Stdout.WriteString("\x1b[?1006l\x1b[?1002l\x1b[?1000l")
		}
		pump := &inputPump{
			src:    fdSource{fd: stdin},
			sink:   xsrc,
			onQuit: func() { onceQuit.Do(func() { close(quit) }) },
		}
		go pump.run()
		xsrc.setTermSize(cols, rows)
	} else if tty {
		go readQuit(stdin, quit, &onceQuit)
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
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

	interval := time.Second / time.Duration(*fps)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := 0; ; i++ {
		select {
		case <-quit:
			return 0
		default:
		}
		if !interactive && *frames > 0 && i >= *frames {
			break
		}

		if tty {
			if c, r, ok := termSize(stdin); ok {
				if *fw == 0 {
					cols = c
				}
				if *fh == 0 {
					rows = r
				}
			}
		}
		if xsrc != nil {
			xsrc.setTermSize(cols, rows)
		}

		if err := src.Grab(&frame); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return 0
			}
			fmt.Fprintln(os.Stderr, "抓屏错误:", err)
			return 1
		}
		convert(&frame, cols, rows, &canvas)
		if interactive && atomic.SwapInt32(&needClear, 0) == 1 {
			out.Clear()
		}
		if err := out.Write(&canvas); err != nil {
			return 1
		}
		if *frames > 0 && i+1 >= *frames {
			break
		}
		if !interactive {
			continue
		}
		select {
		case <-quit:
			return 0
		case <-ticker.C:
		}
	}
	return 0
}
