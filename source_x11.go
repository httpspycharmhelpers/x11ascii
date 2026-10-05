package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/render"
	"github.com/BurntSushi/xgb/xproto"
)

type x11Source struct {
	conn *xgb.Conn
	// inConn 只用来做 XTEST 注入。必须和抓画面的连接分开：实测 Termux X11 里
	// 同一连接一旦 GetImage 抓过画面，后续 XTEST 的指针事件就不再投递给客户端
	// （键盘不受影响），表现就是"点了没反应"。
	inConn *xgb.Conn
	disp   string
	win    xproto.Window
	cap    xproto.Window // 抓取目标：root（默认）或某个窗口
	x, y   int
	w, h   int
	gx, gy int // GetImage 相对 cap 的偏移（窗口=0，root=区域偏移）
	lsb    bool
	path   string
	fixed  bool

	capSel  string // 窗口选择：""=root、auto=最大窗口、name:子串、0xID
	capName string

	symMap     map[uint32]keySlot
	mod        [8]byte
	aw, ah     int32 // 最近一次抓帧的源像素尺寸（供鼠标坐标映射，原子访问）
	cols, rows int32 // 终端网格尺寸（原子访问）

	fit       int32 // 保持宽高比（letterbox），用 atomic 读写以便 f 键随时切
	holdMS    int   // 按键注入的“按住”时长
	modHoldMS int   // 单独修饰键（-ctrl 冒充的长按 Ctrl）按住时长

	// 服务端 RENDER 缩放：整屏在 X 端缩到 gw×gh 再回传，避免 6MB/帧的 XGetImage
	rnd       bool
	srcPic    render.Picture
	dstPic    render.Picture
	pix       xproto.Pixmap
	pictfmt   render.Pictformat
	gw, gh    int
	idle      int32
	lastSum   uint64
	renderErr error
}

// dialX11 自己解析 DISPLAY 并连接，避免 xgb 硬编码 /tmp/.X11-unix（Termux 无 /tmp）。
func dialX11(display string) (*xgb.Conn, string, error) {
	xgb.Logger = log.New(io.Discard, "", 0)
	d := display
	if d == "" {
		d = os.Getenv("DISPLAY")
	}
	if d == "" {
		return nil, "", fmt.Errorf("DISPLAY 未设置（可用 -display :0，或 export DISPLAY=:0）")
	}
	if strings.HasPrefix(d, "/") {
		c, err := dialUnix(d)
		return c, d, err
	}
	if strings.HasPrefix(d, ":") || strings.HasPrefix(d, "unix:") {
		num := d
		if i := strings.IndexByte(num, ':'); i >= 0 {
			num = num[i+1:]
		}
		if i := strings.IndexByte(num, '.'); i >= 0 {
			num = num[:i]
		}
		cands := socketCandidates(num)
		var lastErr error
		for _, p := range cands {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			c, err := dialUnix(p)
			if err == nil {
				return c, p, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, "", fmt.Errorf("连接 X socket 失败: %w", lastErr)
		}
		return nil, "", fmt.Errorf("找不到 X socket（尝试过 %v）", cands)
	}
	c, err := xgb.NewConnDisplay(d)
	return c, d, err
}

func dialUnix(path string) (*xgb.Conn, error) {
	nc, err := net.Dial("unix", path)
	if err != nil {
		return nil, err
	}
	return xgb.NewConnNet(nc)
}

func socketCandidates(num string) []string {
	name := "X" + num
	var out []string
	if p := os.Getenv("X11_SOCKET"); p != "" {
		out = append(out, p)
	}
	if pref := os.Getenv("PREFIX"); pref != "" {
		out = append(out, filepath.Join(pref, "tmp", ".X11-unix", name))
	}
	if td := os.Getenv("TMPDIR"); td != "" {
		out = append(out, filepath.Join(td, ".X11-unix", name))
	}
	if home := os.Getenv("HOME"); home != "" {
		out = append(out, filepath.Join(home, ".X11-unix", name))
	}
	out = append(out, filepath.Join("/tmp", ".X11-unix", name))
	return out
}

func newX11Source(display, region, windowSel string, renderOn, fit bool, holdMS int) (*x11Source, error) {
	conn, path, err := dialX11(display)
	if err != nil {
		return nil, err
	}
	setup := xproto.Setup(conn)
	if len(setup.Roots) == 0 {
		conn.Close()
		return nil, fmt.Errorf("X11 无可用 screen")
	}
	root := setup.Roots[0]
	s := &x11Source{
		conn:      conn,
		disp:      display,
		win:       root.Root,
		cap:       root.Root,
		w:         int(root.WidthInPixels),
		h:         int(root.HeightInPixels),
		lsb:       setup.ImageByteOrder == 0,
		path:      path,
		fit:       int32(b2i(fit)),
		holdMS:    holdMS,
		modHoldMS: 250,
		capSel:    windowSel,
	}
	if strings.HasPrefix(windowSel, "name:") {
		s.capName = strings.TrimPrefix(windowSel, "name:")
	}
	if region != "" {
		x, y, w, h, err := parseRegion(region)
		if err != nil {
			conn.Close()
			return nil, err
		}
		if w > 0 {
			s.w = w
		}
		if h > 0 {
			s.h = h
		}
		s.x, s.y = x, y
		s.gx, s.gy = x, y
		s.fixed = true
	}
	if renderOn && !s.fixed {
		if err := s.initRender(); err != nil {
			s.renderErr = err
		}
	}
	return s, nil
}

func (s *x11Source) Size() (int, int) { return s.w, s.h }

// initRender 打开 RENDER 缩放通道：建一个整屏 source picture。
func (s *x11Source) initRender() error {
	if err := render.Init(s.conn); err != nil {
		return err
	}
	root := xproto.Setup(s.conn).Roots[0]
	rep, err := render.QueryPictFormats(s.conn).Reply()
	if err != nil {
		return err
	}
	for _, sc := range rep.Screens {
		for _, d := range sc.Depths {
			for _, v := range d.Visuals {
				if v.Visual == root.RootVisual {
					s.pictfmt = v.Format
				}
			}
		}
	}
	if s.pictfmt == 0 {
		return fmt.Errorf("找不到 root visual 的 Pictformat")
	}
	return s.bindSource()
}

// bindSource 在 s.cap 上（重新）创建 RENDER source picture。
func (s *x11Source) bindSource() error {
	if s.srcPic != 0 {
		render.FreePicture(s.conn, s.srcPic)
		s.srcPic = 0
	}
	pic, err := render.NewPictureId(s.conn)
	if err != nil {
		return err
	}
	if err := render.CreatePictureChecked(s.conn, pic, xproto.Drawable(s.win), s.pictfmt, 0, nil).Check(); err != nil {
		return err
	}
	name := "bilinear"
	if err := render.SetPictureFilterChecked(s.conn, pic, uint16(len(name)), name, nil).Check(); err != nil {
		name = "fast"
		if err := render.SetPictureFilterChecked(s.conn, pic, uint16(len(name)), name, nil).Check(); err != nil {
			render.FreePicture(s.conn, pic)
			return err
		}
	}
	s.srcPic = pic
	s.rnd = true
	return nil
}

// ensureRenderSize 保证离屏 pixmap 尺寸为 gw×gh，并把 source 的缩放矩阵设好。
func (s *x11Source) ensureRenderSize(gw, gh int) error {
	if s.pix != 0 && gw == s.gw && gh == s.gh {
		return nil
	}
	if s.dstPic != 0 {
		render.FreePicture(s.conn, s.dstPic)
		s.dstPic = 0
	}
	if s.pix != 0 {
		xproto.FreePixmap(s.conn, s.pix)
		s.pix = 0
	}
	root := xproto.Setup(s.conn).Roots[0]
	pix, err := xproto.NewPixmapId(s.conn)
	if err != nil {
		return err
	}
	if err := xproto.CreatePixmapChecked(s.conn, root.RootDepth, pix, xproto.Drawable(root.Root), uint16(gw), uint16(gh)).Check(); err != nil {
		return err
	}
	dst, err := render.NewPictureId(s.conn)
	if err != nil {
		return err
	}
	if err := render.CreatePictureChecked(s.conn, dst, xproto.Drawable(pix), s.pictfmt, 0, nil).Check(); err != nil {
		return err
	}
	// 先把离屏图填黑：letterbox 留边区域一直保持黑色。
	solid, err := render.NewPictureId(s.conn)
	if err != nil {
		return err
	}
	render.CreateSolidFillChecked(s.conn, solid, render.Color{Red: 0, Green: 0, Blue: 0, Alpha: 0xffff}).Check()
	render.CompositeChecked(s.conn, render.PictOpSrc, solid, 0, dst, 0, 0, 0, 0, 0, 0, uint16(gw), uint16(gh)).Check()
	render.FreePicture(s.conn, solid)
	s.pix, s.dstPic, s.gw, s.gh = pix, dst, gw, gh
	return nil
}

// grabRender 用 RENDER 在服务端把整屏缩到 gw×gh，只回传几十 KB。
func (s *x11Source) grabRender(f *Frame) error {
	cols := int(atomic.LoadInt32(&s.cols))
	rows := int(atomic.LoadInt32(&s.rows))
	if cols < 1 || rows < 1 {
		return fmt.Errorf("终端网格尺寸未知")
	}
	gw, gh := cols, rows*2
	if err := s.ensureRenderSize(gw, gh); err != nil {
		return err
	}
	// 保持宽高比：把源按比例缩放并居中，留边保持黑色（已预先填黑）。
	offX, offY, dw, dh := 0, 0, gw, gh
	if atomic.LoadInt32(&s.fit) != 0 && s.w > 0 && s.h > 0 {
		sAsp := float64(s.w) / float64(s.h)
		gAsp := float64(gw) / float64(gh)
		if sAsp > gAsp {
			dh = int(float64(gw)/sAsp + 0.5)
			if dh < 1 {
				dh = 1
			}
			offY = (gh - dh) / 2
		} else {
			dw = int(float64(gh)*sAsp + 0.5)
			if dw < 1 {
				dw = 1
			}
			offX = (gw - dw) / 2
		}
	}
	sx := float64(s.w) / float64(dw)
	sy := float64(s.h) / float64(dh)
	var t render.Transform
	t.Matrix11 = render.Fixed(int32(sx*65536 + 0.5))
	t.Matrix22 = render.Fixed(int32(sy*65536 + 0.5))
	t.Matrix13 = render.Fixed(int32(s.gx) * 65536)
	t.Matrix23 = render.Fixed(int32(s.gy) * 65536)
	t.Matrix33 = render.Fixed(1 << 16)
	if err := render.SetPictureTransformChecked(s.conn, s.srcPic, t).Check(); err != nil {
		return err
	}
	if err := render.CompositeChecked(s.conn, render.PictOpSrc, s.srcPic, 0, s.dstPic, 0, 0, 0, 0, int16(offX), int16(offY), uint16(dw), uint16(dh)).Check(); err != nil {
		return err
	}
	reply, err := xproto.GetImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.pix), 0, 0, uint16(gw), uint16(gh), 0xffffffff).Reply()
	if err != nil {
		return err
	}
	data := reply.Data
	if len(data) < gw*gh {
		return fmt.Errorf("渲染回传数据不足: %d", len(data))
	}
	stride := len(data) / gh
	bpp := stride / gw
	atomic.StoreInt32(&s.aw, int32(s.w))
	atomic.StoreInt32(&s.ah, int32(s.h))
	f.alloc(gw, gh)
	f.Aspect = true
	for y := 0; y < gh; y++ {
		src := data[y*stride : y*stride+gw*bpp]
		dst := f.Pix[y*f.Stride : y*f.Stride+gw*3]
		switch bpp {
		case 4:
			for x := 0; x < gw; x++ {
				p := src[x*4 : x*4+4]
				if s.lsb {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[2], p[1], p[0]
				} else {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[1], p[2], p[3]
				}
			}
		case 3:
			for x := 0; x < gw; x++ {
				p := src[x*3 : x*3+3]
				if s.lsb {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[2], p[1], p[0]
				} else {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[0], p[1], p[2]
				}
			}
		default:
			return fmt.Errorf("不支持的像素字节数 %d", bpp)
		}
	}
	h := uint64(14695981039346656037)
	for _, b := range data {
		h ^= uint64(b)
		h *= 1099511628211
	}
	if h == s.lastSum {
		atomic.StoreInt32(&s.idle, 1)
	} else {
		atomic.StoreInt32(&s.idle, 0)
		s.lastSum = h
	}
	return nil
}

// Idle 报告上一帧与再上一帧完全相同（供主循环降帧省电）。
func (s *x11Source) Idle() bool { return atomic.LoadInt32(&s.idle) == 1 }

// refreshGeometry 重新读取尺寸（横竖屏切换/旋转/窗口变化后调用）。
func (s *x11Source) refreshGeometry() {
	if s.windowMode() {
		_ = s.syncWindow()
		return
	}
	if s.fixed {
		return
	}
	if geo, err := xproto.GetGeometry(s.conn, xproto.Drawable(s.win)).Reply(); err == nil {
		if geo.Width > 0 && geo.Height > 0 {
			s.w, s.h = int(geo.Width), int(geo.Height)
		}
	}
}

// windowMode 报告是否在抓取单个窗口而非整个 root。
func (s *x11Source) windowMode() bool {
	return s.capSel != "" && s.capSel != "root"
}

// syncWindow 定位/更新目标窗口：把 s.cap 指向窗口，更新尺寸与 root 内偏移。
// 每次调用都会重新解析（auto/name 模式可发现晚出现的窗口）。
// capAlive 报告当前锁定的目标窗口是否还在（被关掉后 X 查询会直接报错）。
func (s *x11Source) capAlive() bool {
	if !s.windowMode() {
		return true
	}
	w := s.cap
	if w == 0 || w == s.win {
		return true // 还没锁定到具体窗口
	}
	attr, err := xproto.GetWindowAttributes(s.conn, w).Reply()
	if err != nil {
		return false
	}
	return attr.MapState == xproto.MapStateViewable
}

// capLocked 是否已经锁定到某个具体窗口。
func (s *x11Source) capLocked() bool {
	return s.windowMode() && s.cap != 0 && s.cap != s.win
}

func (s *x11Source) syncWindow() error {
	if !s.windowMode() {
		return nil
	}
	target := xproto.Window(0)
	if id, ok := parseWindowID(s.capSel); ok {
		target = xproto.Window(id)
	} else {
		// pid:N 表示“按 _NET_WM_PID 精确定位被我们启动的那个程序的窗口”，
		// 匹配不上（不少程序不写这个属性）时退回最大窗口，保证总能出画面。
		byPID := strings.HasPrefix(s.capSel, "pid:")
		var wantPID uint32
		pids := map[uint32]bool{}
		if byPID {
			v, _ := strconv.ParseUint(strings.TrimPrefix(s.capSel, "pid:"), 10, 32)
			wantPID = uint32(v)
			pids = descendantPIDs(int(v))
		}
		tree, err := xproto.QueryTree(s.conn, s.win).Reply()
		if err != nil {
			return err
		}
		fallback := xproto.Window(0)
		bestArea := -1
		for _, w := range tree.Children {
			attr, err := xproto.GetWindowAttributes(s.conn, w).Reply()
			if err != nil || attr.MapState != xproto.MapStateViewable {
				continue
			}
			if s.capName != "" && !strings.Contains(s.windowName(w), s.capName) {
				continue
			}
			if byPID {
				pid := s.windowPID(w)
				if pid == wantPID || pids[pid] {
					target = w
					break
				}
			}
			geo, err := xproto.GetGeometry(s.conn, xproto.Drawable(w)).Reply()
			if err != nil {
				continue
			}
			// 窗口管理器会造 1x1/5x5 之类的辅助窗口，别锁到它们上面（会闪一片垃圾）
			if geo.Width < 16 || geo.Height < 16 {
				continue
			}
			if area := int(geo.Width) * int(geo.Height); area > bestArea {
				bestArea = area
				fallback = w
			}
		}
		if target == 0 {
			target = fallback
		}
		if target == 0 {
			return fmt.Errorf("未找到目标窗口 (%s)", s.capSel)
		}
	}
	if target != s.cap {
		s.cap = target
		fmt.Fprintf(os.Stderr, "抓取窗口: 0x%x\n", uint32(target))
	}
	geo, err := xproto.GetGeometry(s.conn, xproto.Drawable(s.cap)).Reply()
	if err != nil {
		return err
	}
	if geo.Width > 0 && geo.Height > 0 {
		if int(geo.Width) != s.w || int(geo.Height) != s.h {
			fmt.Fprintf(os.Stderr, "抓取窗口 0x%x: %dx%d\n", uint32(s.cap), geo.Width, geo.Height)
		}
		s.w, s.h = int(geo.Width), int(geo.Height)
	}
	// 注意：这里始终从**根窗口**读像素、只按窗口矩形裁剪。
	// 直接对窗口 drawable 做 XGetImage/RENDER 读到的是窗口后备存储，在 Termux:X11
	// 上会拿到过期内容（表现为画面下半截发黑、图案重复拼贴）。
	s.x, s.y = 0, 0
	if tr, err := xproto.TranslateCoordinates(s.conn, s.cap, s.win, 0, 0).Reply(); err == nil {
		s.x, s.y = int(tr.DstX), int(tr.DstY)
	}
	s.gx, s.gy = s.x, s.y
	return nil
}

// windowName 读取窗口标题（优先 _NET_WM_NAME，其次 WM_NAME）。
func (s *x11Source) windowName(w xproto.Window) string {
	if atom, err := xproto.InternAtom(s.conn, false, uint16(len("_NET_WM_NAME")), "_NET_WM_NAME").Reply(); err == nil && atom != nil {
		if rep, err := xproto.GetProperty(s.conn, false, w, atom.Atom, xproto.AtomAny, 0, 256).Reply(); err == nil && rep.ValueLen > 0 {
			return string(rep.Value)
		}
	}
	if rep, err := xproto.GetProperty(s.conn, false, w, xproto.AtomWmName, xproto.AtomAny, 0, 256).Reply(); err == nil && rep.ValueLen > 0 {
		return string(rep.Value)
	}
	return ""
}

// windowPID 读取窗口的 _NET_WM_PID（0=没写这个属性）。
func (s *x11Source) windowPID(w xproto.Window) uint32 {
	atom, err := xproto.InternAtom(s.conn, false, uint16(len("_NET_WM_PID")), "_NET_WM_PID").Reply()
	if err != nil || atom == nil {
		return 0
	}
	rep, err := xproto.GetProperty(s.conn, false, w, atom.Atom, xproto.AtomCardinal, 0, 1).Reply()
	if err != nil || rep.Format != 32 || rep.ValueLen < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(rep.Value)
}

// descendantPIDs 收集 root 及其所有子孙进程的 PID。
// -exec "env A=1 dosbox ..." 实际是 sh -c → env → dosbox，
// 窗口上的 _NET_WM_PID 是 dosbox 的 PID，跟我们记的 sh 的 PID 不是一回事，
// 只按父子链匹配就会「找不到目标窗口」，只能退回最大窗口（焦点可能落到别处，按键就没用）。
func descendantPIDs(root int) map[uint32]bool {
	out := map[uint32]bool{}
	if root <= 0 {
		return out
	}
	out[uint32(root)] = true
	frontier := []int{root}
	for depth := 0; depth < 8 && len(frontier) > 0; depth++ {
		var next []int
		for _, pid := range frontier {
			children, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
			if err != nil {
				continue
			}
			for _, f := range strings.Fields(string(children)) {
				if c, err := strconv.Atoi(f); err == nil && !out[uint32(c)] {
					out[uint32(c)] = true
					next = append(next, c)
				}
			}
		}
		frontier = next
	}
	return out
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// setFit 让 f 键能随时切「保持比例 / 拉伸铺满」，不用退出重开。
func (s *x11Source) setFit(v bool) {
	atomic.StoreInt32(&s.fit, int32(b2i(v)))
}

// parseWindowID 解析 "0x1234" / "1234" 形式的固定窗口 ID。
func parseWindowID(sel string) (uint32, bool) {
	sv := strings.TrimPrefix(sel, "0x")
	sv = strings.TrimPrefix(sv, "0X")
	if sv == "" {
		return 0, false
	}
	v, err := strconv.ParseUint(sv, 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(v), true
}

// focusLocked 把焦点交回我们正在抓的那个窗口。
func (s *x11Source) focusLocked() {
	if !s.capLocked() {
		return
	}
	xproto.SetInputFocus(s.conn, xproto.InputFocusPointerRoot, s.cap, xproto.TimeCurrentTime)
}

// FocusBiggest 把 X 输入焦点设到 root 下面积最大的已映射子窗口（通常是全屏游戏窗口）。
// 否则 XTest 注入的按键会因焦点在桌面/面板而丢失，表现为“按了没反应”。
func (s *x11Source) FocusBiggest() {
	tree, err := xproto.QueryTree(s.conn, s.win).Reply()
	if err != nil {
		return
	}
	var best xproto.Window
	bestArea := 0
	for _, w := range tree.Children {
		attr, err := xproto.GetWindowAttributes(s.conn, w).Reply()
		if err != nil || attr.MapState != xproto.MapStateViewable {
			continue
		}
		geo, err := xproto.GetGeometry(s.conn, xproto.Drawable(w)).Reply()
		if err != nil {
			continue
		}
		if geo.Width < 16 || geo.Height < 16 {
			continue
		}
		area := int(geo.Width) * int(geo.Height)
		if area > bestArea {
			bestArea = area
			best = w
		}
	}
	if best != 0 {
		xproto.SetInputFocus(s.conn, xproto.InputFocusPointerRoot, best, xproto.TimeCurrentTime)
	}
}

func (s *x11Source) getImage() (*xproto.GetImageReply, error) {
	return xproto.GetImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.win),
		int16(s.gx), int16(s.gy), uint16(s.w), uint16(s.h), 0xffffffff).Reply()
}

func (s *x11Source) Grab(f *Frame) error {
	if s.rnd {
		if err := s.grabRender(f); err == nil {
			return nil
		} else {
			fmt.Fprintf(os.Stderr, "RENDER 缩放不可用(%v)，回退 XGetImage\n", err)
			if s.srcPic != 0 {
				render.FreePicture(s.conn, s.srcPic)
				s.srcPic = 0
			}
			s.rnd = false
		}
	}
	reply, err := s.getImage()
	if err != nil && !s.fixed {
		if s.windowMode() {
			if serr := s.syncWindow(); serr == nil {
				reply, err = s.getImage()
			}
		} else if geo, gerr := xproto.GetGeometry(s.conn, xproto.Drawable(s.win)).Reply(); gerr == nil {
			nw, nh := int(geo.Width), int(geo.Height)
			if nw > 0 && nh > 0 && (nw != s.w || nh != s.h) {
				s.w, s.h = nw, nh
				reply, err = s.getImage()
			}
		}
	}
	if err != nil {
		return err
	}
	data := reply.Data
	if len(data) < s.w*s.h {
		return fmt.Errorf("XGetImage 数据不足: %d", len(data))
	}
	stride := len(data) / s.h
	bpp := stride / s.w

	atomic.StoreInt32(&s.aw, int32(s.w))
	atomic.StoreInt32(&s.ah, int32(s.h))

	f.alloc(s.w, s.h)
	f.Aspect = atomic.LoadInt32(&s.fit) != 0
	for row := 0; row < s.h; row++ {
		src := data[row*stride : row*stride+s.w*bpp]
		dst := f.Pix[row*f.Stride : row*f.Stride+s.w*3]
		switch bpp {
		case 4:
			for x := 0; x < s.w; x++ {
				p := src[x*4 : x*4+4]
				if s.lsb {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[2], p[1], p[0]
				} else {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[1], p[2], p[3]
				}
			}
		case 3:
			for x := 0; x < s.w; x++ {
				p := src[x*3 : x*3+3]
				if s.lsb {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[2], p[1], p[0]
				} else {
					dst[x*3+0], dst[x*3+1], dst[x*3+2] = p[0], p[1], p[2]
				}
			}
		default:
			return fmt.Errorf("不支持的像素字节数 %d（需 24/32bpp）", bpp)
		}
	}
	return nil
}

func (s *x11Source) Close() error {
	s.conn.Close()
	return nil
}

func x11Probe(display string) error {
	s, err := newX11Source(display, "", "", false, false, 0)
	if err != nil {
		return err
	}
	defer s.Close()
	setup := xproto.Setup(s.conn)
	root := setup.Roots[0]
	fmt.Printf("X11 连接成功\n")
	fmt.Printf("  display        : %s\n", func() string {
		if display != "" {
			return display
		}
		return "(来自 $DISPLAY)"
	}())
	fmt.Printf("  screen         : %dx%d\n", root.WidthInPixels, root.HeightInPixels)
	fmt.Printf("  socket         : %s\n", s.path)
	fmt.Printf("  root depth     : %d\n", root.RootDepth)
	fmt.Printf("  image byteorder: %d (0=LSB,1=MSB)\n", setup.ImageByteOrder)
	fmt.Printf("  扩展           :")
	for _, ext := range []string{"MIT-SHM", "DAMAGE", "XTEST", "RANDR", "RENDER", "XFIXES"} {
		rep, err := xproto.QueryExtension(s.conn, uint16(len(ext)), ext).Reply()
		if err == nil && rep.Present {
			fmt.Printf(" %s(op=%d)", ext, rep.MajorOpcode)
		}
	}
	fmt.Printf("\n")
	var f Frame
	if err := s.Grab(&f); err != nil {
		return fmt.Errorf("抓帧失败: %w", err)
	}
	fmt.Printf("抓帧成功: %dx%d stride=%d 像素0=(%d,%d,%d)\n",
		f.W, f.H, f.Stride, f.Pix[0], f.Pix[1], f.Pix[2])
	return nil
}

func parseRegion(s string) (x, y, w, h int, err error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'x' || r == 'X' || r == '+' })
	if len(parts) < 2 {
		return 0, 0, 0, 0, fmt.Errorf("区域格式应为 WxH 或 WxH+X+Y: %q", s)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		v, e := strconv.Atoi(p)
		if e != nil {
			return 0, 0, 0, 0, fmt.Errorf("区域数字非法: %q", p)
		}
		nums[i] = v
	}
	w, h = nums[0], nums[1]
	if len(nums) >= 3 {
		x = nums[2]
	}
	if len(nums) >= 4 {
		y = nums[3]
	}
	return x, y, w, h, nil
}
