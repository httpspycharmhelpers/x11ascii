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

# 边看边操作：把终端键盘/鼠标回传给 X（仅 x11 源）
./x11ascii -source x11 -display :0 -input

# 包裹启动一个程序：显示并操作它，本程序退出时一并结束子进程
./x11ascii -source x11 -display :0 -input -w 120 -h 30 \
  -exec "dosbox -nosound -fullscreen -c 'mount c ~/dosgames/wolf' -c 'c:' -c 'WOLF3D.EXE'"

# 显示并操作 Firefox（Termux:X11 下必须关掉 GPU/GL，否则只会出现占位窗口）
./x11ascii -source x11 -display :0 -input \
  -exec "env GDK_BACKEND=x11 MOZ_ENABLE_WAYLAND=0 MOZ_DISABLE_GPU=1 MOZ_WEBRENDER=0 \
         MOZ_ACCELERATED=0 GDK_GL=disable LIBGL_ALWAYS_SOFTWARE=1 firefox about:blank"
```

交互模式下按 `q`（或 Ctrl+C）退出；开了 `-input` 后 `q` 等键会转发给 X，用 **Ctrl+C** 退出。

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
| `-input` | 把终端键盘/鼠标事件回传给 X（XTest，仅 `x11` 源）；Ctrl+C 退出 |
| `-mouse` | `-input` 模式下启用终端鼠标报告（默认开） |
| `-exec` | 先启动并包裹一个命令（如 `dosbox ...`），本程序退出时结束它 |
| `-render` | 用 XRender 在服务端把整屏缩到终端网格再回传（默认开；失败自动回退 `XGetImage`） |
| `-idle-fps` | 画面无变化时降到的帧率（默认 5；`0`=不降帧） |

## 颜色

- 默认 **24-bit 真彩**（`\x1b[38;2;R;G;Bm`），需要终端支持（`COLORTERM=truecolor`）。
- `-256` 改为 **ANSI-256**（`\x1b[38;5;Nm`），用 6×6×6 色立方 + 24 级灰阶做最近色量化，字节更少、老终端也能显示。

## 排错（`-source x11` 没画面时）

```sh
./x11ascii -probe -display :0     # 连接、打印 screen/depth/socket，再抓一帧验证
```

其它：

- `探测失败: DISPLAY 未设置` → 加 `-display :0`。
- 抓帧报 `不支持的像素字节数` → 根窗口不是 24/32bpp，把 `-probe` 输出发来。
- 首次抓帧在进入 alt screen **之前**，失败信息在普通终端上可见。

## 输入回传（`-input`）

用 XTest 扩展把终端按键 / 鼠标事件注入 X，让终端里看到的画面可以真正被操作（玩 X 游戏、点窗口等）。
仅 `x11` 源支持；启动时会读取服务器的键盘映射（keysym→keycode）与修饰键映射，自动处理大小写与 Shift、Ctrl、Alt 组合。

- 普通字符、`Enter`/`Tab`/`Backspace`/`Esc`、方向键、`Home/End/PageUp/PageDown/Insert/Delete`、`F1`–`F12`、`Alt+键`、UTF-8 字符均可。
- 鼠标：`-mouse`（默认开）会开启终端 SGR 鼠标报告，左/中/右键、滚轮、拖动都会按当前终端网格映射到源像素坐标。
- 退出用 **Ctrl+C**（`q` 等键会原样转发给 X）。

自测（需要真实 X 与 `xev`，`xorg-xev` 包）：

```sh
DISPLAY=:0 xev > /tmp/xev.log 2>&1 &
# 取日志里 "Outer window is 0x...." 的值
DISPLAY=:0 XEV_WINDOW=0x1800001 go test -tags integration -run TestXTestInputToXev -v
grep keysym /tmp/xev.log   # 应能看到 z / A / Up / F5 / Ctrl-a 等
```

## 性能

- 转换与输出零分配（复用缓冲）、只写变化格、颜色游程缓存；内置图案源本机可达 600+ fps。
- **服务端 XRender 缩放（`-render`，默认开）**：不再把整屏像素经 X 套接字拷回，而是让 X 服务端用
  RENDER 把整屏缩放/过滤到终端网格（`列 × 行×2`）后再回传，每帧数据量从数 MB 降到几十 KB。
  实测 Termux:X11 全屏 1080×1471、输出 100×30：**每 100 帧 7.5s → 0.42s（约 18×）**；
  RENDER 不可用时自动回退 `XGetImage`。
- **空闲降帧（`-idle-fps`，默认 5）**：画面与上一帧完全相同时主循环自动降帧，静止桌面几乎不耗 CPU，活动时立即恢复。
- 走 `XGetImage` 回退路径时，可用 `-region` 只抓目标窗口以减小回传量。
- **Android/Termux 无 SysV 共享内存**，故未启用 MIT-SHM；上面的 RENDER 服务端缩放已覆盖该场景。
  真正的 Linux 主机也可用 `-source cmd` 配合 `ffmpeg x11grab`（可让 ffmpeg 直接缩放到目标尺寸）。

## 运行任意程序

`-exec` 让你把要显示/操作的程序交给 `x11ascii` 一起启动、退出时整组清理（例如 DOSBox、浏览器）：

```sh
# 德军总部 3D（官方 shareware v1.4）经 DOSBox 全屏运行，方向键移动
./x11ascii -source x11 -display :0 -input -w 120 -h 30 \
  -exec "dosbox -nosound -fullscreen -c 'mount c ~/dosgames/wolf' -c 'c:' -c 'WOLF3D.EXE'"
```

注意（Termux:X11）：Firefox 默认的 GPU/GL 初始化会卡住导致只出 10×10 占位窗口，
用上面的 `GDK_GL=disable LIBGL_ALWAYS_SOFTWARE=1 …` 关掉即可正常出窗。

## 限制

- 只传输画面，不传输音频；不改变窗口内容。键鼠回传需显式开启 `-input`（仅 `x11` 源，走 XTest）。
- `x11` 后端假设 24/32bpp TrueColor；16bpp 未处理。
- 真彩需要终端支持 24-bit 颜色（`COLORTERM=truecolor`）。
