package filer

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"io/ioutil"
	"os"
)

// 判断所给路径是否为文件夹
func IsDir(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// 判断所给路径是否为文件
func IsFile(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !stat.IsDir()
}

// 获取文件大小
func Size(path string) (int64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return stat.Size(), nil
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
 * 获取指定目录下的文件或目录或文件和目录（不包含子目录下的文件目录）
 * @param string dirPath 目录路径
 * @param int mode 资源类型 0：该目录下的所有文件 1：该目录下的目录 2：该目录下的所有文件和目录
 * @param interface{} msgs 可变参数，参数顺序 0：[]string files（字符串切片用于接收 目录路径 下所有文件，包含子目录下的文件）
 */
func GetDirFiles(dirPath string, mode int, msgs ...interface{}) ([]string, error) {
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
		if mode == 0 && !fi.IsDir() { // 文件
			files = append(files, dirPath+"/"+fi.Name())
		} else if mode == 1 && fi.IsDir() { // 目录
			files = append(files, dirPath+"/"+fi.Name())
		} else if mode == 2 { // 文件和目录
			files = append(files, dirPath+"/"+fi.Name())
		}
	}
	return files, nil
}

/**
 * 获取指定目录下的所有文件和文件夹（包含子目录下的文件和文件夹）
 * @param string dirPath 目录路径
 * @param interface{} msgs 可变参数，参数顺序 0：[]string files（字符串切片用于接收 目录路径 下所有文件，包含子目录下的文件）
 */
func GetFilesAndDirs(dirPath string, msgs ...interface{}) ([]string, error) {
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
			files = append(files, dirPath+"/"+fi.Name())
			files, _ = GetFilesAndDirs(dirPath+"/"+fi.Name(), files)
		} else {
			files = append(files, dirPath+"/"+fi.Name())
		}
	}
	return files, nil
}

/**
 * 文件单位大小转换
 * @param int64 bytes 字节(b)
 */
func FormatBytes(bytes int64) string {
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

/**
 * 复制文件
 * @param src string 文件源地址
 * @param dst string 文件新地址
 */
func Copy(src, dst string) (int64, error) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return 0, err
	}

	if !sourceFileStat.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer destination.Close()

	nBytes, err := io.Copy(destination, source)
	return nBytes, err
}

/*
 * 返回文件的MD5校验值(适合计算小文件和大文件 md5 值)
 * @param string filePath 文件地址
 */
func Md5(filePath string) (string, error) {
	if !IsFile(filePath) {
		return "", fmt.Errorf("%s 文件不存在，请检查", filePath)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	for buf, reader := make([]byte, 65536), bufio.NewReader(file); ; {
		n, err := reader.Read(buf)
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		hash.Write(buf[:n])
	}
	checksum := fmt.Sprintf("%x", hash.Sum(nil))
	return checksum, nil
}