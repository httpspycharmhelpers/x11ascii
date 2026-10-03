package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BurntSushi/xgb"
	"github.com/BurntSushi/xgb/xproto"
)

type x11Source struct {
	conn *xgb.Conn
	win  xproto.Window
	x, y int
	w, h int
	lsb  bool
}

func newX11Source(display, region string) (*x11Source, error) {
	var (
		conn *xgb.Conn
		err  error
	)
	if display != "" {
		conn, err = xgb.NewConnDisplay(display)
	} else {
		conn, err = xgb.NewConn()
	}
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
	}
	return s, nil
}

func (s *x11Source) Size() (int, int) { return s.w, s.h }

func (s *x11Source) Grab(f *Frame) error {
	reply, err := xproto.GetImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.win),
		int16(s.x), int16(s.y), uint16(s.w), uint16(s.h), 0xffffffff).Reply()
	if err != nil {
		return err
	}
	data := reply.Data
	if len(data) < s.w*s.h {
		return fmt.Errorf("XGetImage 数据不足: %d", len(data))
	}
	stride := len(data) / s.h
	bpp := stride / s.w

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
