package logger

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/Unknwon/goconfig"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

var DefaultSavePath = "storage/logs/log.log" // 日志默认保存路径

type Logger struct {}

func init() {
	cfg, err := goconfig.LoadConfigFile("config/logger.ini")
	if err != nil {
		return
	}

	path, err := cfg.GetValue(goconfig.DEFAULT_SECTION, "DefaultSavePath")
	if err != nil {
		return
	}

	DefaultSavePath = path
}

func (l *Logger) record(msg interface{}, target string) error {
	entension := filepath.Ext(target)
	if entension == "" {
		return errors.New(fmt.Sprintf("File name cannot be empty %s", target))
	}

	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}

	file, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE, 666)
	if err != nil {
		return errors.New(fmt.Sprintf("Could not create filer %s", target))
	}
	defer file.Close()

	logger := log.New(file, "", log.LstdFlags)
	logger.Println(msg)
	return nil
}

func makepath(args ...interface{}) string {
	target := DefaultSavePath
	if len(args) > 0 {
		target = args[0].(string)
	}
	return target
}

func Println(msg interface{}, args ...interface{}) {
	Info(msg, args...)
}

func Info(msg interface{}, args ...interface{}) {
	if err := new(Logger).record(msg, makepath(args...)); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// 获取Panic堆栈信息追踪
func PanicTrace(args ...int) []byte {
	kb := 1
	if len(args) > 0 {
		kb = args[0]
	}
	s := []byte("/src/runtime/panic.go")
	e := []byte("\ngoroutine ")
	line := []byte("\n")
	stack := make([]byte, kb<<10) // 4KB
	length := runtime.Stack(stack, true)
	start := bytes.Index(stack, s)
	stack = stack[start:length]
	start = bytes.Index(stack, line) + 1
	stack = stack[start:]
	end := bytes.LastIndex(stack, line)
	if end != -1 {
		stack = stack[:end]
	}
	end = bytes.Index(stack, e)
	if end != -1 {
		stack = stack[:end]
	}
	stack = bytes.TrimRight(stack, "\n")
	return stack
}