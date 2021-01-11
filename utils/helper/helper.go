package helper

import (
	"crypto/md5"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
)

// 判断所给路径是否为文件夹
func IsDir(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.IsDir()
}

// 判断所给路径是否为文件
func IsFile(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !s.IsDir()
}

// MD5加密
func Md5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

/*
 * 获取指定目录下的所有文件（包含子目录下的文件）
 * @param string dirPath 目录路径
 * @param interface{} msgs 可变参数，参数顺序 0：[]string files（字符串切片用于接收 目录路径 下所有文件，包含子目录下的文件）
 */
func GetAllFiles(dirPath string, msgs ...interface{}) ([]string, error) {
	fis, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return nil, err
	}

	var files []string
	if len(msgs) > 0 {
		files = msgs[0].([]string)
	} else {
		files = make([]string, 0)
	}

	for _, fi := range fis {
		if fi.IsDir() { // 目录, 递归遍历
			files, _ = GetAllFiles(dirPath+"/"+fi.Name(), files)
		} else {
			files = append(files, dirPath+"/"+fi.Name())
		}
	}
	return files, nil
}

/**
 * 创建文件并逐行写入内容
 * @param string filename 文件路径
 * @param string s 要写入的内容
 * @param int mode 写入模式，默认为0，0：覆盖，1：末尾追加
 */
func WriteLog(filename string, s string, mode int) error {
	if !IsFile(filename) {
		dir := filepath.Dir(filename)
		if !IsDir(dir) {
			err := os.MkdirAll(dir, os.ModePerm)
			if err != nil {
				return err
			}
		}
	}

	flag := os.O_WRONLY | os.O_CREATE // 默认覆盖模式
	if mode == 1 {
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND // 追加模式
	}

	file, err := os.OpenFile(filename, flag, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write([]byte(s + "\n"))
	if err != nil {
		return err
	}
	return nil
}

/**
 * 文件单位大小转换
 * @param int64 bytes 字节(b)
 */
func FormatSize(bytes int64) string {
	var size string
	if bytes >= 1073741824 {
		size = fmt.Sprintf("%.2f %s", (float64(bytes) / 1073741824), "GB")
	} else if bytes >= 1048576 {
		size = fmt.Sprintf("%.2f %s", (float64(bytes) / 1048576), "MB")
	} else if bytes >= 1024 {
		size = fmt.Sprintf("%.2f %s", (float64(bytes) / 1024), "KB")
	} else if bytes > 1 {
		size = fmt.Sprintf("%f %s", float64(bytes), "bytes")
	} else if bytes == 1 {
		size = fmt.Sprintf("%f %s", float64(bytes), "byte")
	} else {
		size = "0 bytes"
	}
	return size
}