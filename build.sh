#!/usr/bin/env sh
set -eu

OUTPUT_NAME="${1:-polypilot}"
TARGET_SYSTEM="${2:-linux}"
BUILD_MODE="${3:-dev}"  # 👈 新增（默认 dev）

case "$TARGET_SYSTEM" in
  linux)
    GOOS_VALUE="linux"
    ;;
  mac)
    GOOS_VALUE="darwin"
    ;;
  *)
    echo "不支持的目标系统: $TARGET_SYSTEM"
    echo "可选值: linux, mac"
    exit 1
    ;;
esac

# 👇 根据 build mode 设置参数
LDFLAGS=""
TAGS=""

case "$BUILD_MODE" in
  dev)
    echo "构建模式: dev"
    TAGS="dev"
    ;;
  release)
    echo "构建模式: release"
    TAGS="release"
    LDFLAGS="-s -w"
    ;;
  *)
    echo "不支持的构建模式: $BUILD_MODE"
    echo "可选值: dev, release"
    exit 1
    ;;
esac

echo "开始编译: 输出文件=$OUTPUT_NAME, 目标系统=$TARGET_SYSTEM, 模式=$BUILD_MODE"

CGO_ENABLED=0 GOOS="$GOOS_VALUE" GOARCH=amd64 \
go build -tags="$TAGS" -ldflags="$LDFLAGS" -o "$OUTPUT_NAME" .

echo "编译完成: $OUTPUT_NAME"