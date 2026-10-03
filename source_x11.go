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

func newX11Source(display, region string) (*x11Source, error) {
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
	return s, nil
}

func (s *x11Source) Size() (int, int) { return s.w, s.h }

func (s *x11Source) getImage() (*xproto.GetImageReply, error) {
	return xproto.GetImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.win),
		int16(s.x), int16(s.y), uint16(s.w), uint16(s.h), 0xffffffff).Reply()
}

func (s *x11Source) Grab(f *Frame) error {
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
	s, err := newX11Source(display, "")
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
