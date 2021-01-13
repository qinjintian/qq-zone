package helper

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"io/ioutil"
	"math/rand"
	"os/exec"
	"time"
)

// MD5加密
func Md5(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

/**
 * 生成随机的字符串
 * @param n int 随机字符串长度
 */
func GetRandomString(n int) string {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"
	b := make([]byte, n)
	rand.Seed(time.Now().UnixNano())
	for v := range b {
		b[v] = s[rand.Intn(len(s))]
	}
	return string(b)
}

/**
 * gbk编码转utf-8编码
 * @param string s gbk字符串
 */
func GbkToUtf8(s string) (string, error) {
	reader := transform.NewReader(bytes.NewReader([]byte(s)), simplifiedchinese.GBK.NewDecoder())
	d, err := ioutil.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(d), nil
}

/**
 * UTF-8编码转gbk编码
 * @param string s utf-8字符串
 */
func Utf8ToGbk(s string) (string, error) {
	reader := transform.NewReader(bytes.NewReader([]byte(s)), simplifiedchinese.GBK.NewEncoder())
	d, err := ioutil.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(d), nil
}

/**
 * exec 实时获取外部命令的执行输出到终端，参数和系统内置的exec.Command()用法基本一样
 * @param name string 系统内置exec.Command()第一个参数一样
 * @param mode int 运行模式，0：每一条命令执行完毕分别返回一次结果到终端  1：实时获取外部命令的执行输出到终端
 * @param ...string 系统内置exec.Command()第二个参数一样
 */
func Command(name string, mode int, arg ...string) error {
	cmd := exec.Command(name, arg...)
	// 获取输出对象，可以从该对象中读取输出结果
	stdout, err := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err != nil {
		return err
	}
	defer stdout.Close()

	// 运行命令
	if err = cmd.Start(); err != nil {
		return err
	}

	// 从管道中实时获取输出并打印到终端
	for {
		buf := make([]byte, 1024)
		_, err := stdout.Read(buf)
		if mode == 1 {
			fmt.Println(string(buf))
		}
		if err != nil {
			break
		}
	}

	// 等待执行完毕
	if err = cmd.Wait(); err != nil {
		return err
	}
	return nil
}
