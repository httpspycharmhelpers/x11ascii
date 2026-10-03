# x11ascii

把 **任意 X11 画面**（游戏、桌面、程序窗口）实时转成**彩色 ASCII / 块字符**显示在终端里的通用渲染器。

- 抓屏后端可插拔：纯 Go `xgb`（`XGetImage`）/ 外部命令管道（`ffmpeg x11grab`、`import`、`xwd` 等）/ 内置测试图案。
- 显示用**上下半块字符 `▀` + 真彩前景/背景色**，每格两个像素，纵向分辨率翻倍。
- **增量输出**：只重画变化过的格子，并缓存当前颜色，大幅减少字节。
- 输出**分辨率可指定**（`-w` 列 / `-h` 行）。

## 构建

```sh
go build -o x11ascii .
```

## 用法

```sh
# 自动挑选后端（有 $DISPLAY 用 X11，否则用 -cmd，再否则用测试图案）
./x11ascii

# 指定输出 120x30，目标 30fps
./x11ascii -w 120 -h 30 -fps 30

# 抓某个 X display 的根窗口
./x11ascii -source x11 -display :0

# 只抓左上角 1280x720 区域
./x11ascii -source x11 -region 1280x720+0+0

# 命令管道：任何能向 stdout 吐 rgb24 原始帧的命令
./x11ascii -source cmd -srcsize 1280x720 \
  -cmd "ffmpeg -loglevel error -f x11grab -video_size 1280x720 -i :0.0 -f rawvideo -pix_fmt rgb24 -"

# 无 X 环境自测（内置动态图案）
./x11ascii -source test -w 80 -h 24
```

运行时按 `q`（或 Ctrl+C）退出。

## 参数

| 参数 | 说明 |
| --- | --- |
| `-w`, `-h` | 输出列数 / 行数，`0` 表示跟随终端尺寸（默认） |
| `-source` | `auto`(默认) \| `x11` \| `cmd` \| `test` |
| `-display` | X display，如 `:0`；留空用 `$DISPLAY` |
| `-region` | 抓屏区域 `WxH` 或 `WxH+X+Y` |
| `-cmd` | `cmd` 源的命令，stdout 必须是 rgb24 原始帧 |
| `-srcsize` | 源帧尺寸 `WxH`（`cmd`/`test` 用，默认 320x200） |
| `-fps` | 目标帧率（默认 30） |
| `-frames` | 渲染多少帧后退出（`0`=不限） |
| `-once` | 只渲染一帧后退出 |
| `-256` | 用 ANSI-256 调色板量化颜色（省带宽、兼容不支持真彩的终端） |
| `-v` | 输出源/显示等诊断信息到 stderr（默认开） |
| `-probe` | 连接 X11、打印 screen/depth/抓帧自检后退出 |

## 颜色

- 默认 **24-bit 真彩**（`\x1b[38;2;R;G;Bm`），需要终端支持（`COLORTERM=truecolor`）。
- `-256` 改为 **ANSI-256**（`\x1b[38;5;Nm`），用 6×6×6 色立方 + 24 级灰阶做最近色量化，字节更少、老终端也能显示。

## 排错（`-source x11` 没画面时）

```sh
echo $DISPLAY                 # 必须非空，例如 :0
./x11ascii -probe             # 连接 X11 并打印 screen/depth，再抓一帧验证
```

- `探测失败: empty display string` → 当前 shell 没有 `DISPLAY`。用**启动 X 的同一个 shell**运行，或先
  `export DISPLAY=:0`（按 `startx11` 实际打印的编号改）。
- 若 X11 连接成功但抓帧报 `不支持的像素字节数` → 该 X 的根窗口不是 24/32bpp，把 `-probe` 输出发来。
- 首次抓帧会发生在进入 alt screen **之前**，失败信息在普通终端上可见，不会再"一闪而过"。

## 性能

- 转换与输出是零分配（复用缓冲）、只写变化格、颜色游程缓存，本机 80×24 合成源可达 600+ fps。
- 抓屏：`x11` 后端的 `XGetImage` 每帧要把整幅图经 X 套接字拷回，大分辨率下是瓶颈。真正的 X 主机上建议用
  `-source cmd` 配合 `ffmpeg x11grab`（可用性更好，且可让 ffmpeg 直接缩放到目标尺寸）。
- **Android/Termux 无 SysV 共享内存**，故未启用 MIT-SHM；`x11` 后端在 Termux 上走 `XGetImage`。

## 限制

- 只传输画面，不传输音频；不改变窗口内容，也不接管 X 输入（键鼠回传留待后续）。
- `x11` 后端假设 24/32bpp TrueColor；16bpp 未处理。
- 真彩需要终端支持 24-bit 颜色（`COLORTERM=truecolor`）。
