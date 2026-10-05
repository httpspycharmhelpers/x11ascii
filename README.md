# x11ascii

把 **任意 X11 画面**（游戏、桌面、程序窗口）实时转成**彩色 ASCII / 块字符**显示在终端里的通用渲染器。

- 抓屏后端可插拔：纯 Go `xgb`（`XGetImage`）/ 外部命令管道（`ffmpeg x11grab`、`import`、`xwd` 等）/ 内置测试图案。
- 显示用**上下半块字符 `▀` + 真彩前景/背景色**，每格两个像素，纵向分辨率翻倍。
- **增量输出**：只重画变化过的格子，并缓存当前颜色，大幅减少字节。
- 输出**分辨率可指定**（`-w` 列 / `-h` 行）。


## 静态编译（给没有 Termux 的设备用）

MT 管理器之类的应用里没法 `pkg install`，所以提供不依赖任何 Termux 库的静态二进制：

```bash
./build-static.sh            # 本机架构 + android/arm64
./build-static.sh all        # android/arm64、linux/arm64、linux/amd64
```

产物在 `dist/`。`android/arm64` 那个只挂 Android 系统自带的 `linker64`，
拷到任何有终端的 App（MT 管理器、Termux、新建终端都能跑）里 `chmod +x` 即可，
不需要装 Termux、不需要 X11 包（只抓屏/看图不需要 X 服务，注入输入才需要）。

## 运行中随时调：缩放 / 分辨率 / 输入层

以前这些只能在启动时用 `-w/-h` 定死，想改得退出重开。现在不用了：

| 按键 | 作用 |
|---|---|
| `+` / `-` | 放大 / 缩小画面（放大=格子少字大，缩小=格子多更细腻） |
| `1`..`6` | 切预设分辨率：60x18 / 80x24 / 100x30 / 120x36 / 160x48 / 200x60 |
| `0` | 跟随终端大小（自适应） |
| `f` | 保持画面比例 <-> 拉伸铺满 |
| `?` | 打印这张表 |
| `` ` `` | 打开/关闭**输入层**（`-typekey` 可改这个键；关掉时它不会发给程序） |

画面比终端小就居中留黑，比终端大就居中裁掉，不会顶坏终端布局。
这些热键在没开 `-input`、甚至没有 X 源（`-source test/cmd`）时也照样能用。

### 输入层：打字、挪光标、粘贴

按 `` ` `` 打开，底部出现输入框并接管键盘（此时按键不会发给被包裹的程序）：

| 按键 | 作用 |
|---|---|
| `←` `→` | 移动光标（**这是之前完全缺失的能力**） |
| `Home` / `End` | 行首 / 行尾 |
| `↑` `↓` | 翻输入历史 |
| `Backspace` / `Delete` | 删光标前 / 后 |
| `Ctrl+A` / `Ctrl+E` | 行首 / 行尾 |
| `Ctrl+U` / `Ctrl+W` / `Ctrl+K` | 清空 / 删一个词 / 删到行尾 |
| `回车` | 逐字发给 X 程序（中文走 XSendEvent Unicode 码点） |
| `Ctrl+回车` | 写进 X 的 CLIPBOARD 并发 `Ctrl+V`（粘贴） |
| `Ctrl+X` | 从 X 的 CLIPBOARD 把内容读回输入框 |
| `Esc` 或 `` ` `` | 回游戏 |

手机剪贴板：终端里按 `Ctrl+Shift+V`，粘贴进来的文字会直接进输入框（终端送来的就是字节流，
多字节也支持）；在输入框里用方向键改完再回车发出去。

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

# 一条命令完整流程：开 X11 + 轻量窗口管理器 → 起 DOSBox → 定位其窗口 → 渲染，退出时全部关掉
./x11ascii -session -display :1 -input -w 120 -h 30 \
  -exec "env SDL_VIDEODRIVER=x11 dosbox -fullscreen -c 'mount c ~/dosgames/wolf' -c 'c:' -c 'WOLF3D.EXE'"
```

交互模式下按 `q`（或 Ctrl+C）退出；开了 `-input` 后 `q` 等键会转发给 X，用 **Ctrl+C** 退出。

## 参数

| 参数 | 说明 |
| --- | --- |
| `-w`, `-h` | 输出列数 / 行数，`0` 表示跟随终端尺寸（默认） |
| `-source` | `auto`(默认) \| `x11` \| `cmd` \| `test` |
| `-display` | X display，如 `:0`；留空用 `$DISPLAY` |
| `-region` | 抓屏区域 `WxH` 或 `WxH+X+Y` |
| `-window` | 只抓单个窗口：`auto`(最大窗口) \| `name:子串`(标题匹配) \| `0xID`；留空=整个屏幕 |
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
| `-session` | 一条命令跑完整流程：开 X11（+窗口管理器）→ 起 `-exec` 程序 → 定位其窗口 → 渲染；退出时按 程序→桌面→X11 全部关掉 |
| `-desktop` | `-session` 时的窗口管理器命令（默认 `xfwm4`，很轻）；**没有 WM 时很多程序不会真正绘制，只会剩空白/花屏**。填 `dbus-launch --exit-with-session xfce4-session` 可用完整 XFCE 桌面 |
| `-x11-args` | `-session` 时传给 `termux-x11` 的参数（默认 `-legacy-drawing -force-bgra`） |
| `-wait` | 首次抓屏前先等几秒，给被启动的程序留出绘制时间 |
| `-fit` | 保持源画面宽高比，居中留黑边，避免被拉伸变形（默认开） |
| `-hold` | `-input` 模式下按键“按住”时长（毫秒，默认 60），利于游戏识别移动 |
| `-ctrl` | 把普通键当成长按 Ctrl，如 `-ctrl z`。**终端不会上报“单独按 Ctrl”**，DOS 里“按住 Ctrl 射击/斜跑”的游戏必须靠它 |
| `-ctrl-hold` | `-ctrl` 冒充出来的 Ctrl 按住多久（毫秒，默认 250）。按住不放会自动重复，等于持续射击 |
| `-quit-taps` | 快速连点几下就退出（默认 3，`0` 关闭）。纯触屏用户唯一的退路 |
| `-quit-key` | 指定单键退出，如 `-quit-key F12` |
| `-keybar` | 画面下方画一条可点的虚拟键盘条，占几行（默认 `-input` 时 2，`0` 关闭）。带 `FIRE` 键和可锁定的 `CTRL`/`ALT`/`SHIFT` |
| `-fire-key` | 虚拟键盘 `FIRE` 键发出什么组合键（默认 `ctrl`，即长按 Ctrl）。游戏用别的键就写 `-fire-key space` |
| `-esc-wait` | ESC 后等转义序列后续字节的毫秒数（默认 150）。方向键/F 键/扩展键靠它，**调太小会被拆成单键**（方向键失灵还乱打字） |

### 手机上怎么打字和按出组合键

- **字母/数字/标点**：输入法直接打，全部支持（`a-z`、`A-Z`、`0-9`、所有 ASCII 标点），大写字母自动带 Shift。
- **方向键 / Home / End / PgUp / PgDn / F1-F12 / Shift+Tab**：Termux 扩展键行发的转义序列全部解析，`-input` 下会正确注入到 X 程序。
- **组合键**：扩展键行的 `CTRL+方向键` 这类序列解析正确（`CSI 1;5A` = Ctrl+↑），游戏里的斜跑、加速都能用。
- **裸 Ctrl / Alt（输入法给不了的）**：两种办法
  1. 屏幕底部的虚拟键盘条（`-keybar`，默认开）：`CTRL` 点一下锁定（变黄底），再点方向键就是 Ctrl+方向；`SHIFT` 同理，用来打大写；`FIRE` 直接发长按 Ctrl。
  2. `-map` / 键位表把一个普通键当修饰键：`-map 'x=ctrl'`（点 x 就是长按 Ctrl，射击用）。

### 怎么退出（重要）

不会再出现“只能靠另一个终端 kill”的情况，任何一种都能退：

| 方式 | 怎么用 | 适合 |
|---|---|---|
| 关闭目标窗口 | 浏览器点右上角 X、游戏自己退出 → 工具自动回 Termux | **触屏最自然** |
| 快速连点 | 连点 3 下（次数用 `-quit-taps` 调） | 纯触屏，没有键盘 |
| 双击 Esc | 400ms 内按两下 Esc | 有键盘或软键盘 |
| `Ctrl+C` | 照旧 | 有键盘 |
| 单键 | `-quit-key F12` / `-quit-key q` | 自定义 |
| 兜底 | 另一终端 `pkill -INT -f x11ascii` | 极端情况 |

退出时终端会自动恢复（退出备用屏、关掉鼠标上报、还原 raw 模式），不会把 shell 弄坏。

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

## 一条命令跑完整流程

`-session` 把整条链路串起来，退出时自动收干净（程序 → 窗口管理器 → X11）：

```sh
./x11ascii -session -input -w 120 -h 30 \
  -exec "env SDL_VIDEODRIVER=x11 dosbox -fullscreen -c 'mount c ~/dosgames/wolf' -c 'c:' -c 'WOLF3D.EXE'"
```

流程：**输入命令 → 开启 X11（+窗口管理器）→ 启动 DOSBox → 定位它的窗口 → 缩放/拼接/回传按键 → 回到 Termux 操作 → Ctrl+C 退出并全部关掉**。

关键点：

- **必须有窗口管理器**。Termux:X11 上没有 WM 时，DOSBox/SDL 之类的程序**不会真正绘制**，
  读到的只是一片未初始化的窗口内存（表现为下半截发黑、图案重复拼贴）。默认用 `xfwm4`（纯 WM，很轻，
  不带 XFCE 面板/桌面）；想要完整桌面就 `-desktop "dbus-launch --exit-with-session xfce4-session"`。
- **窗口定位**：`-exec` 启动程序后默认按 `_NET_WM_PID` 精确定位它的窗口，匹配不到则退回“最大的可见窗口”，
  窗口出现得晚也会自动切过去。也可显式指定：`-window auto` / `-window name:火狐` / `-window 0xID`。
- **像素从根窗口读、按窗口矩形裁剪**。直接对窗口 drawable 做 `XGetImage`/RENDER 在 Termux:X11 上会读到
  过期内容，所以只用窗口的尺寸和位置做裁剪，读取源始终是根窗口。
- **声音与本工具无关**：音频是程序自己直接输出到 Android 的，不经过 ASCII 管线。
  想听声音就别加 `-nosound`，必要时 `SDL_AUDIODRIVER=pulseaudio`；工具退出时子进程被杀，声音也随之停止。

## 单窗口模式

只抓某个窗口、放到终端里：

```sh
# auto=最大窗口，name:子串=按标题，0xID=固定窗口 ID
./x11ascii -source x11 -display :0 -window auto -input -w 120 -h 30
```

`-window` 模式下会自动跟随窗口的移动/缩放；窗口还没出现时会先黑屏，出现后自动显示。
`-fit`（默认开）保证按原始宽高比显示，终端变形或横竖屏切换时也不会被压扁。

## 限制

- 只传输画面，不传输音频；不改变窗口内容。键鼠回传需显式开启 `-input`（仅 `x11` 源，走 XTest）。
- `x11` 后端假设 24/32bpp TrueColor；16bpp 未处理。
- 真彩需要终端支持 24-bit 颜色（`COLORTERM=truecolor`）。
