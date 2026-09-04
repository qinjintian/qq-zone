#!/usr/bin/env bash
#
# Copyright (c) 2026 qinjintian. All rights reserved.
#
# No Part of this file may be reproduced, stored
# in a retrieval system, or transmitted, in any form, or by any means,
# electronic, mechanical, photocopying, recording, or otherwise,
# without the prior consent of qinjintian.
#
# @Author: qinjintian<514092640@qq.com>
# @Date: 2026-09-04
# @FileName: build.sh
# @Description: [QQ 空间相册备份工具全平台交叉编译脚本（Linux / macOS）]
#

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
BIN="$ROOT/bin"
SYSO="cmd/qq-zone/resource_windows.syso"

ORIG_GOOS="${GOOS-}"
ORIG_GOARCH="${GOARCH-}"

restore_env() {
	if [[ -n "${ORIG_GOOS}" ]]; then
		export GOOS="$ORIG_GOOS"
	else
		unset GOOS || true
	fi
	if [[ -n "${ORIG_GOARCH}" ]]; then
		export GOARCH="$ORIG_GOARCH"
	else
		unset GOARCH || true
	fi
	rm -f "$SYSO"
}
trap restore_env EXIT

if [[ -d "$BIN" ]]; then
	echo "[*] Cleaning existing bin/ directory..."
	rm -rf "${BIN:?}/"*
else
	mkdir -p "$BIN"
fi

echo "[>] Starting cross-platform build task..."

rm -f "$SYSO"
if [[ -f "build/windows/app.ico" ]]; then
	echo "[*] Generating Windows resource file for icon..."
	if [[ -f "build/windows/app.manifest" ]]; then
		go run github.com/akavel/rsrc@latest -manifest build/windows/app.manifest -ico build/windows/app.ico -o "$SYSO"
	else
		go run github.com/akavel/rsrc@latest -ico build/windows/app.ico -o "$SYSO"
	fi
fi

echo "[+] Building Windows (amd64)..."
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o "$BIN/qq-zone-win.exe" ./cmd/qq-zone

# .syso 是 Windows COFF 资源，留着会干扰 Linux/macOS 链接。
rm -f "$SYSO"

echo "[+] Building Linux (amd64)..."
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$BIN/qq-zone-linux" ./cmd/qq-zone

echo "[+] Building macOS (intel)..."
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o "$BIN/qq-zone-macos-intel" ./cmd/qq-zone

echo "[+] Building macOS (m-series)..."
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o "$BIN/qq-zone-macos-m-series" ./cmd/qq-zone

echo "[!] Build completed! Executables are in 'bin/' directory."
