package main

import (
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
	conn  *xgb.Conn
	win   xproto.Window
	x, y  int
	w, h  int
	lsb   bool
	path  string
	fixed bool

	symMap     map[uint32]keySlot
	mod        [8]byte
	aw, ah     int32 // 最近一次抓帧的源像素尺寸（供鼠标坐标映射，原子访问）
	cols, rows int32 // 终端网格尺寸（原子访问）

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

func newX11Source(display, region string, renderOn bool) (*x11Source, error) {
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
		conn: conn,
		win:  root.Root,
		w:    int(root.WidthInPixels),
		h:    int(root.HeightInPixels),
		lsb:  setup.ImageByteOrder == 0,
		path: path,
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
	pic, err := render.NewPictureId(s.conn)
	if err != nil {
		return err
	}
	if err := render.CreatePictureChecked(s.conn, pic, xproto.Drawable(s.win), s.pictfmt, 0, nil).Check(); err != nil {
		return err
	}
	s.srcPic = pic
	name := "bilinear"
	if err := render.SetPictureFilterChecked(s.conn, pic, uint16(len(name)), name, nil).Check(); err != nil {
		name = "fast"
		if err := render.SetPictureFilterChecked(s.conn, pic, uint16(len(name)), name, nil).Check(); err != nil {
			render.FreePicture(s.conn, pic)
			s.srcPic = 0
			return err
		}
	}
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
	var t render.Transform
	t.Matrix11 = render.Fixed(int64(s.w) * 65536 / int64(gw))
	t.Matrix22 = render.Fixed(int64(s.h) * 65536 / int64(gh))
	t.Matrix33 = render.Fixed(1 << 16)
	if err := render.SetPictureTransformChecked(s.conn, s.srcPic, t).Check(); err != nil {
		return err
	}
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
	if err := render.CompositeChecked(s.conn, render.PictOpSrc, s.srcPic, 0, s.dstPic, 0, 0, 0, 0, 0, 0, uint16(gw), uint16(gh)).Check(); err != nil {
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

func (s *x11Source) getImage() (*xproto.GetImageReply, error) {
	return xproto.GetImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.win),
		int16(s.x), int16(s.y), uint16(s.w), uint16(s.h), 0xffffffff).Reply()
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
		if geo, gerr := xproto.GetGeometry(s.conn, xproto.Drawable(s.win)).Reply(); gerr == nil {
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
	s, err := newX11Source(display, "", false)
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
