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

// MD5 returns MD5 hash of string
func MD5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

// FileMD5 returns MD5 hash of file
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

// RandInt returns a random integer between min and max
func RandInt(min, max int) int {
	if min >= max {
		return min
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return r.Intn(max-min) + min
}

// Exists checks if file or directory exists
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || os.IsExist(err)
}

// IsDir checks if path is a directory
func IsDir(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// ListFiles returns all files in directory recursively
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

// FormatBytes formats bytes to human readable string (using binary prefixes)
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
