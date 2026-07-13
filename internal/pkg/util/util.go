/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-07-02
 * @LastEditors: qinjintian<514092640@qq.com>
 * @LastEditTime: 2026-07-03 17:30:00
 * @FileName: util.go
 * @Description: [项目通用工具函数集，涵盖文件校验、路径检查、字节格式化及随机数生成]
 */

package util

import (
	"crypto/md5"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"time"
)

// MD5 计算并返回给定字符串的 MD5 哈希摘要（十六进制字符串）
func MD5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

// FileMD5 以流式读取的方式计算并返回本地文件的 MD5 哈希摘要
// 适用于大文件校验，避免将整个文件加载到内存中
func FileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// RandInt 返回一个位于 [min, max) 区间的伪随机整数
func RandInt(min, max int) int {
	if min >= max {
		return min
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(max-min) + min
}

// Exists 检查指定的本地文件或目录路径是否存在
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || os.IsExist(err)
}

// IsDir 检查指定的本地路径是否存在且为一个目录
func IsDir(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// ListFiles 递归遍历指定目录，并返回该目录下所有文件的绝对或相对路径列表
func ListFiles(dirPath string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// FormatBytes 将字节大小转换为人类易读的格式（使用 1024 进制的 KiB/MiB/GiB 标准）
func FormatBytes(bytes int64) string {
	const (
		KiB = 1024
		MiB = KiB * 1024
		GiB = MiB * 1024
	)
	if bytes >= GiB {
		return fmt.Sprintf("%.2f GiB", float64(bytes)/GiB)
	}
	if bytes >= MiB {
		return fmt.Sprintf("%.2f MiB", float64(bytes)/MiB)
	}
	if bytes >= KiB {
		return fmt.Sprintf("%.2f KiB", float64(bytes)/KiB)
	}
	return fmt.Sprintf("%d B", bytes)
}

// VisualLength 返回字符串在终端显示的视觉长度（中文占 2 位）
func VisualLength(s string) int {
	length := 0
	for _, r := range s {
		if r > 127 {
			length += 2
		} else {
			length++
		}
	}
	return length
}

// PadRight 将字符串填充到指定的视觉长度
func PadRight(s string, targetLen int) string {
	currLen := VisualLength(s)
	if currLen >= targetLen {
		return s
	}
	return s + fmt.Sprintf("%*s", targetLen-currLen, "")
}
