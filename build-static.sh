#!/data/data/com.termux/files/usr/bin/bash
# 静态编译 x11ascii：CGO_ENABLED=0，不链接任何 Termux/系统库。
# 产物是纯 Go 二进制：
#   - Android/arm64  → 挂 Android 自带的 linker64，MT 管理器、任何第三方终端都能直接跑，
#                      不需要装 Termux 的包（MT 管理器里没法 pkg install，这是刚需）
#   - Linux/arm64、Linux/amd64 → 给 Termux 以外的设备（SSH 上去的服务器等）
# 用法:
#   ./build-static.sh              # 编译本机架构 + android/arm64
#   ./build-static.sh all          # 编译全部架构
#   ./build-static.sh android/arm64
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"
OUT="$DIR/dist"
mkdir -p "$OUT"

targets=()
case "${1:-native}" in
	native)      targets=( "android/$(uname -m | sed 's/aarch64/arm64/;s/x86_64/amd64/')" ) ;;
	all)         targets=( android/arm64 linux/arm64 linux/amd64 ) ;;
	android*)    targets=( "$1" ) ;;
	linux*)      targets=( "$1" ) ;;
	*)           targets=( "$1" ) ;;
esac

for t in "${targets[@]}"; do
	goos="${t%/*}"
	goarch="${t#*/}"
	name="x11ascii-${goos}-${goarch}"
	[ "$goos" = "android" ] && name="x11ascii-arm64"
	echo "编译 $t ..."
	env -u LD_LIBRARY_PATH -u LD_PRELOAD \
		CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
		go build -trimpath -ldflags="-s -w" -o "$OUT/$name" . || {
			echo "  失败: $t"; continue; }
	need=$(readelf -d "$OUT/$name" 2>/dev/null | grep -ci 'NEEDED.*libc\|libX11\|libtermux' || true)
	echo "  -> $OUT/$name  $(stat -c%s "$OUT/$name") 字节  动态库依赖: $need"
done

echo
echo "自检：模拟一个「没有 Termux 的环境」——清空环境变量跑一下"
HOSTM="$(uname -m)"
for f in "$OUT"/x11ascii-*; do
	[ -x "$f" ] || continue
	base="$(basename "$f")"
	case "$base" in
		*arm64)   [ "$HOSTM" = "aarch64" ] || [ "$HOSTM" = "arm64" ] || { printf '  %-28s 本机架构不同，跳过\n' "$base"; continue; } ;;
		*amd64)   [ "$HOSTM" = "x86_64" ] || { printf '  %-28s 本机架构不同，跳过\n' "$base"; continue; } ;;
	esac
	printf '  %-28s ' "$base"
	if env -i PATH=/system/bin "$f" -source test -srcsize 16x8 -once >/dev/null 2>&1; then
		echo "可独立运行 ✓（无 Termux 环境）"
	else
		echo "跑不起来 ✗"
	fi
done
