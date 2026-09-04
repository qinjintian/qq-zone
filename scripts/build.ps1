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
 * @LastEditors: qinjintian<514092640@qq.com>
 * @LastEditTime: 2026-09-04 11:39:00
 * @FileName: build.ps1
 * @Description: [QQ 空间相册备份工具全平台交叉编译脚本（Windows / pwsh）]
 #>

# Set output encoding
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$OutputEncoding = [System.Text.Encoding]::UTF8
$ErrorActionPreference = "Stop"

$RootPath = Split-Path -Parent $PSScriptRoot
Set-Location $RootPath
$BinPath = Join-Path $RootPath "bin"
$SysoPath = "cmd/qq-zone/resource_windows.syso"

$OrigGOOS = $env:GOOS
$OrigGOARCH = $env:GOARCH

function Restore-GoEnv {
    if ($null -eq $OrigGOOS -or $OrigGOOS -eq "") {
        Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    } else {
        $env:GOOS = $OrigGOOS
    }
    if ($null -eq $OrigGOARCH -or $OrigGOARCH -eq "") {
        Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    } else {
        $env:GOARCH = $OrigGOARCH
    }
}

# Cleanup
if (Test-Path $BinPath) {
    Write-Host "[*] Cleaning existing bin/ directory..." -ForegroundColor Yellow
    Remove-Item -Path "$BinPath\*" -Recurse -Force
} else {
    New-Item -ItemType Directory -Path $BinPath | Out-Null
}

Write-Host "[>] Starting cross-platform build task..." -ForegroundColor Cyan

# Generate Windows resource file for icon and manifest
if (Test-Path $SysoPath) {
    Remove-Item -Path $SysoPath -Force
}

if (Test-Path "build/windows/app.ico") {
    Write-Host "[*] Generating Windows resource file for icon..."
    if (Test-Path "build/windows/app.manifest") {
        go run github.com/akavel/rsrc@latest -manifest build/windows/app.manifest -ico build/windows/app.ico -o $SysoPath
    } else {
        go run github.com/akavel/rsrc@latest -ico build/windows/app.ico -o $SysoPath
    }
}

# Windows (64-bit)
Write-Host "[+] Building Windows (amd64)..."
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-win.exe") ./cmd/qq-zone

# .syso is a Windows COFF resource; leave it in place and Linux/macOS links may fail.
if (Test-Path $SysoPath) {
    Remove-Item -Path $SysoPath -Force
}

# Linux (64-bit)
Write-Host "[+] Building Linux (amd64)..."
$env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-linux") ./cmd/qq-zone

# macOS (Intel)
Write-Host "[+] Building macOS (intel)..."
$env:GOOS = "darwin"; $env:GOARCH = "amd64"
go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-macos-intel") ./cmd/qq-zone

# macOS (M-Series)
Write-Host "[+] Building macOS (m-series)..."
$env:GOOS = "darwin"; $env:GOARCH = "arm64"
go build -ldflags="-s -w" -o (Join-Path $BinPath "qq-zone-macos-m-series") ./cmd/qq-zone

Restore-GoEnv

Write-Host "[!] Build completed! Executables are in 'bin/' directory." -ForegroundColor Green
