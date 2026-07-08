<#
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-07-08
 * @FileName: build.ps1
 * @Description: [QQ 空间相册备份工具全平台交叉编译脚本]
 #>

# Set output encoding
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8

$RootPath = Get-Location
$BinPath = Join-Path $RootPath "bin"

# Cleanup
if (Test-Path $BinPath) {
    Write-Host "[*] Cleaning existing bin/ directory..." -ForegroundColor Yellow
    Remove-Item -Path "$BinPath\*" -Recurse -Force
} else {
    New-Item -ItemType Directory -Path $BinPath
}

Write-Host "[>] Starting cross-platform build task..." -ForegroundColor Cyan

# Windows (64-bit)
Write-Host "[+] Building Windows (amd64)..."
$env:GOOS="windows"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-win.exe") ./cmd/qq-zone

# Linux (64-bit)
Write-Host "[+] Building Linux (amd64)..."
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-linux") ./cmd/qq-zone

# macOS (Intel)
Write-Host "[+] Building macOS (intel)..."
$env:GOOS="darwin"; $env:GOARCH="amd64"; go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-macos-intel") ./cmd/qq-zone

# macOS (M-Series)
Write-Host "[+] Building macOS (m-series)..."
$env:GOOS="darwin"; $env:GOARCH="arm64"; go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-macos-m-series") ./cmd/qq-zone

# Restore defaults
$env:GOOS="windows"; $env:GOARCH="amd64"

Write-Host "[!] Build completed! Executables are in 'bin/' directory." -ForegroundColor Green
