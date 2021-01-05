package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"fmt"
	pbar "github.com/cheggaaa/pb/v3"
	"github.com/tidwall/gjson"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	_ "net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const logPath = "storage/logs/qzone/log.log"

var (
	qq           string
	gtk          string
	cookie       string
	taskNum      string
	prevent      string
	photoAlbums  string
	reqHeader    map[string]string
	albumUrl     string
	waiterIn     sync.WaitGroup
	waiterOut    sync.WaitGroup
	haschan      chan int
	m            sync.Mutex
	albumSucc    uint64 = 0
	total        uint64 = 0        // 相片/视频总数
	succ         uint64 = 0        // 下载成功数
	fail         uint64 = 0        // 下载失败数
	newNum       uint64 = 0        // 新增数
	duplicateNum uint64 = 0        // 重复数
	videoNum     uint64 = 0        // 视频数
	imageNum     uint64 = 0        // 相片数
	localFiles   map[string]string // 当前本地相册已经存在的文件
	albumPhotos  []gjson.Result
)

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("请输入您的QQ号，结束请按回车键：")
	scanner.Scan()
	qq = scanner.Text()
	_, err := strconv.ParseFloat(qq, 64)
	if qq == "" || err != nil {
		reEnter("QQ账号错误，请按任意键退出后重新启动程序输入...")
	}

	fmt.Printf("请输入您的[g_tk]参数，结束请按回车键：")
	scanner.Scan()
	gtk = scanner.Text()
	if gtk == "" {
		reEnter("g_tk参数错误，请按任意键退出后重新启动程序输入...")
	}

	fmt.Printf("请输入您的[cookie]参数，结束请按回车键：")
	scanner.Scan()
	cookie = scanner.Text()
	if cookie == "" {
		reEnter("cookie参数无效，请按任意键退出后重新启动程序输入...")
	}

	fmt.Printf("请输入并行下载任务数[1至100范围内]，默认为1，结束请按回车键：")
	scanner.Scan()
	taskNum = scanner.Text()
	var tasks int
	if taskNum == "" {
		tasks = 1
	} else {
		var err error
		tasks, err = strconv.Atoi(taskNum)
		if err != nil || tasks < 1 || tasks > 100 {
			reEnter("并行下载任务数输入错误，请按任意键退出后重新启动程序输入...")
		}
	}

	fmt.Printf("请输入是否开启防重复下载，默认是y，结束请按回车键[y/n]：")
	scanner.Scan()
	prevent = scanner.Text()
	if prevent == "" {
		prevent = "y"
	} else {
		prevent = strings.ToLower(prevent)
		if prevent != "y" && prevent != "n" {
			reEnter("否开启防重复下载输入错误，只能输入[y/n]，请按任意键退出后重新启动程序输入...")
		}
	}

	fmt.Printf("请输入要下载的相册，多个相册请按空格键隔开，格式[相册1 相册2]，为空时默认下载全部相册：")
	scanner.Scan()
	photoAlbums = scanner.Text()

	var (
		xiangCe string
		albs    []string
	)

	if photoAlbums != "" {
		albs = strings.Split(photoAlbums, " ")
		xiangCe = photoAlbums
	} else {
		albs = []string{}
		xiangCe = "全部相册"
	}

	fmt.Println()
	fmt.Println("以下参数为终端输入的参数，程序将在3秒后开始进行下载")
	fmt.Println("QQ：", qq)
	fmt.Println("g_tk：", gtk)
	fmt.Println("cookie：", cookie)
	fmt.Println("并行下载任务数：", taskNum)
	fmt.Println("是否开启防重复下载：", prevent)
	fmt.Println("要下载的相册名：", xiangCe)
	fmt.Println()

	haschan = make(chan int, tasks)

	// 指定要下载的相册
	whitelist := make(map[string]bool)
	for _, name := range albs {
		whitelist[name] = true
	}

	time.Sleep(time.Second * 3)

	reqHeader = make(map[string]string)
	reqHeader["cookie"] = cookie
	reqHeader["user-agent"] = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.100 Safari/537.36"
	albumUrl = fmt.Sprintf("https://h5.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&callbackFun=shine", gtk, qq, qq)

	// 定时发送心跳，防止cookie过期
	ticker := time.NewTicker(time.Minute * 10)
	go Heartbeat(ticker)

	// 获取相册列表
	albumList, err := GetAlbumList()
	if err != nil {
		reEnter(fmt.Sprintf("%v 获取相册列表数据错误，请按任意键退出：%v", time.Now().Format("2006/01/02 15:04:05"), err.Error()))
	}

	var albumListArr []gjson.Result = gjson.Parse(albumList).Array()
	if len(albumListArr) < 1 {
		reEnter(fmt.Sprintf("%v 没有获取到任何相册数据，请检查cookie是否有效，请按任意键退出...", time.Now().Format("2006/01/02 15:04:05")))
	}

	for _, album := range albumListArr {
		name := album.Get("name").String()
		if len(whitelist) > 0 {
			if _, ok := whitelist[name]; !ok {
				continue
			}
		}

		albumPath := "./storage/qzone/album/" + name
		if !IsDir(albumPath) {
			os.MkdirAll(albumPath, os.ModePerm)
		}

		var (
			pageStart       int64 = 0
			pageNum         int64 = 500
			albumPaotoTotal int64 = 0
			totalInAlbum    int64 = album.Get("total").Int()
			i               int   = 1
		)

		albumPhotos = make([]gjson.Result, 0)
		for {
			photoListUrl := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_list_photo?g_tk=%v&callback=shine_Callback&mode=0&idcNum=4&hostUin=%v&topicId=%v&noTopic=0&uin=%v&pageStart=%v&pageNum=%v&skipCmtCount=0&singleurl=1&batchId=&notice=0&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&outstyle=json&format=jsonp&json_esc=1&callbackFun=shine", gtk, qq, album.Get("id").String(), qq, pageStart, pageNum)
			b, err := HttpGet(photoListUrl, reqHeader)
			if err != nil {
				reEnter(fmt.Sprintf("%v 获取相册图片[%s]第%d页错误，请按任意键退出:%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), i, err.Error()))
			}
			photoJson := string(b)
			photoJson = photoJson[15:]
			photoJson = photoJson[:strings.LastIndex(photoJson, ")")]
			photoData := gjson.Parse(photoJson)
			photoData = photoData.Get("data")
			photoList := photoData.Get("photoList").Array()
			albumPhotos = append(albumPhotos, photoList...)
			totalInPage := photoData.Get("totalInPage").Int()
			albumPaotoTotal += totalInPage
			if totalInAlbum == albumPaotoTotal { // 说明这个相册下载完成了
				break
			}
			i++
			pageStart += 500
		}
		total += uint64(len(albumPhotos)) // 累加相片/视频总数

		if prevent == "y" {
			localFiles = make(map[string]string, 0)
			fpaths, _ := GetAllFiles(albumPath)
			for _, fPath := range fpaths {
				fName := path.Base(fPath)
				fName = fName[:strings.LastIndex(fName, ".")]
				localFiles[fName] = fPath
			}
		} else {
			os.RemoveAll(albumPath) // 把当前本地相册删掉重新创建空相册然后下载文件，相当于清空目录资源
			os.MkdirAll(albumPath, os.ModePerm)
		}

		albumSucc = 0 // 重新初始化为0
		// 正在下载处理
		for key, photo := range albumPhotos {
			waiterIn.Add(1)
			waiterOut.Add(1)
			haschan <- 1
			go StartDownload(key, photo, albumPhotos, album, albumPath)
		}
		waiterIn.Wait() // 等待当前相册相片下载完之后才能继续下载下一个相册
	}

	close(haschan)

	waiterOut.Wait()
	ticker.Stop()

	if prevent == "y" {
		if duplicateNum > 0 {
			fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d, 检测到%d张相片/视频本地已存在并忽略下载", time.Now().Format("2006/01/02 15:04:05"), qq, total, succ, imageNum, videoNum, newNum, duplicateNum, (total-succ)))
		} else {
			fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d，重复%d", time.Now().Format("2006/01/02 15:04:05"), qq, total, succ, imageNum, videoNum, newNum, duplicateNum, (total-succ)))
		}
	} else {
		fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d，失败%d", time.Now().Format("2006/01/02 15:04:05"), qq, total, succ, imageNum, videoNum, newNum, (total-succ)))
	}

	fmt.Println()

	var (
		serial int
		input string
	)

	for {
		if serial > 0 {
			fmt.Println("\n请输入stop退出程序...")
		} else {
			fmt.Println("请输入stop退出程序...")
		}
		_, err := fmt.Scanln(&input)
		if err != nil {
			continue
		}
		fmt.Println("您输入的是：", input)
		if input == "stop" {
			break
		}
		serial++
	}
}

func StartDownload(key int, photo gjson.Result, albumPhotos []gjson.Result, album gjson.Result, albumPath string) {
	defer func() {
		<-haschan
		waiterIn.Done()
		waiterOut.Done()

		if e := recover(); e != nil {
			// 打印栈信息
			buf := make([]byte, 1024)
			buf = buf[:runtime.Stack(buf, false)]
			err := fmt.Errorf("[PANIC]%v\n%s\n", e, buf)
			fmt.Println(fmt.Sprintf("%v 相册[%s]第%d个相片/视频下载过程报错引起恐慌，相片/视频名：%v  错误相关信息：%v", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), err.Error()))
			WriteLog(logPath, fmt.Sprintf("%v 相册[%s]第%d个相片/视频下载过程报错引起恐慌，相片/视频名：%v  错误相关信息：%v", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), err.Error()), 1)
		}
	}()

	sloc := photo.Get("sloc").String()
	// 获取相片/视频拍摄时间
	rawshti := photo.Get("rawshoottime").Value()
	rawShoottime := ""
	if reflect.TypeOf(rawshti).Kind() == reflect.String && rawshti.(string) != "" {
		rawShoottime = rawshti.(string)
	} else {
		rawShoottime = photo.Get("uploadtime").String()
	}

	loc, _ := time.LoadLocation("Local")                                           // 重要：获取时区
	shoottime, _ := time.ParseInLocation("2006-01-02 15:04:05", rawShoottime, loc) // 使用模板在对应时区转化为time.time类型
	shootdate := time.Unix(shoottime.Unix(), 0).Format("20060102150405")
	source, resourceType, fileName := "", "", ""
	if photo.Get("is_video").Bool() {
		resourceType = "视频"
		videoUrl := fmt.Sprintf("https://h5.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_floatview_photo_list_v2?g_tk=%v&callback=viewer_Callback&topicId=%v&picKey=%v&cmtOrder=1&fupdate=1&plat=qzone&source=qzone&cmtNum=0&inCharset=utf-8&outCharset=utf-8&callbackFun=viewer&uin=%v&hostUin=%v&appid=4&isFirst=1", gtk, album.Get("id").String(), sloc, qq, qq)
		b, err := HttpGet(videoUrl, reqHeader)
		if err != nil {
			fmt.Println(logPath, time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：%s", album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, err.Error()))
			WriteLog(logPath, fmt.Sprintf("%v 相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, err.Error()), 1)
			return
		}
		videoJson := string(b)
		videoJson = videoJson[16:]
		videoJson = videoJson[:strings.LastIndex(videoJson, ")")]
		videoData := gjson.Parse(videoJson).Get("data")
		videos := videoData.Get("photos").Array()
		if len(videos) < 1 {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d部视频链接未找到，视频名：%s  视频地址：%s", album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl))
			WriteLog(logPath, fmt.Sprintf("%v 相册[%s]第%d部视频链接未找到，视频名：%s  视频地址：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl), 1)
			return
		}
		picPosInPage := videoData.Get("picPosInPage").Int()
		videoInfo := (videos[picPosInPage]).Get("video_info").Map()
		status := videoInfo["status"].Int()
		// 状态为2的表示可以正常播放的视频，也就是已经转换并上传在QQ空间服务器上
		if status != 2 {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d个视频文件无效，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s", album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, photo.Get("name").String()))
			WriteLog(logPath, fmt.Sprintf("%v 相册[%s]第%d个视频文件无效，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, photo.Get("url").String()), 1)
			return
		}
		source = videoInfo["video_url"].String()
		// 目前QQ空间所有视频都是MP4格式，所以暂时固定后缀名都是.mp4
		fileName = fmt.Sprintf("VID_%s_%s_%s.mp4", shootdate[:8], shootdate[8:], Md5(sloc)[8:24])
	} else {
		resourceType = "相片"
		if raw := photo.Get("raw").String(); raw != "" {
			source = raw
		} else if originUrl := photo.Get("origin_url").String(); originUrl != "" {
			source = originUrl
		} else {
			source = photo.Get("url").String()
		}
		// QQ空间相片有不同的文件后缀名，那么不传后缀名的文件名下载的时候会自动获取到对应的文件扩展名
		fileName = fmt.Sprintf("IMG_%s_%s_%s", shootdate[:8], shootdate[8:], Md5(sloc)[8:24])
	}

	// 检查是否启用了防重复下载开关,如果开启就忽略下载已经存在的
	if prevent == "y" && len(localFiles) > 0 {
		pos := strings.LastIndex(fileName, ".")
		tmpName := fileName
		if pos != -1 {
			tmpName = fileName[:pos]
		}

		if p, ok := localFiles[tmpName]; ok {
			// 假如本地已经存在改文件名，那就匹配文件大小是否一致
			fileInfo, _ := os.Stat(p)
			fsize := fileInfo.Size()
			respHeader, err := http.Get(source)
			if err != nil || respHeader == nil || respHeader.ContentLength != fsize {
				os.RemoveAll(localFiles[tmpName])
			} else {
				respHeader.Body.Close()
				m.Lock()
				if resourceType == "相片" {
					imageNum++
				} else {
					videoNum++
				}
				succ++
				albumSucc++
				duplicateNum++
				output := fmt.Sprintf("[%d/%d]相册[%s]第%d个%s文件下载完成_跳过同名文件", albumSucc, len(albumPhotos), album.Get("name").String(), (key + 1), resourceType) + "\n" +
					"下载/完成时间：" + time.Now().Format("2006/01/02 15:04:05") + "\n" +
					"相片/视频原名：" + photo.Get("name").String() + "\n" +
					"相片/视频名称：" + tmpName + path.Ext(p) + "\n" +
					"相片/视频大小：" + FormatSize(fsize) + "\n" +
					"相片/视频地址：" + source + "\n"
				fmt.Println(output)
				m.Unlock()
				return
			}
		}
	}

	target := fmt.Sprintf("%s/%s", albumPath, fileName)
	resp, err := Download(source, target, 5, 600, false)
	if err != nil {
		// 记录 某个相册 下载失败的相片
		fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d个%s文件下载出错，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s  错误信息：%s\n", album.Get("name").String(), (key + 1), resourceType, photo.Get("name").String(), source, photo.Get("url").String(), err.Error()))
		WriteLog(logPath, fmt.Sprintf("%v 相册[%s]第%d个%s文件下载出错，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s  错误信息：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), resourceType, photo.Get("name").String(), source, photo.Get("url").String(), err.Error()), 1)
		return
	} else {
		m.Lock()
		succ++
		albumSucc++
		newNum++
		if photo.Get("is_video").Bool() {
			videoNum++
		} else {
			imageNum++
		}

		fileInfo, _ := os.Stat(resp["path"].(string))
		output := fmt.Sprintf("[%d/%d]相册[%s]第%d个%s文件下载完成", (albumSucc), len(albumPhotos), album.Get("name").String(), (key + 1), resourceType) + "\n" +
			"下载/完成时间：" + time.Now().Format("2006/01/02 15:04:05") + "\n" +
			"相片/视频原名：" + photo.Get("name").String() + "\n" +
			"相片/视频名称：" + resp["filename"].(string) + "\n" +
			"相片/视频大小：" + FormatSize(fileInfo.Size()) + "\n" +
			"相片/视频地址：" + source + "\n"
		fmt.Println(output)
		m.Unlock()
	}
}

func reEnter(msg interface{}) {
	fmt.Println(msg)
	b := make([]byte, 1)
	os.Stdin.Read(b)
	os.Exit(0)
}

// 定时发送心跳，防止cookie过期
func Heartbeat(ticker *time.Ticker) {
	for t := range ticker.C {
		t.Format("2006/01/02 15:04:05")
		HttpGet(albumUrl, reqHeader)
	}
}

func GetAlbumList() (string, error) {
	bytes, err := HttpGet(albumUrl, reqHeader)
	if err != nil {
		fmt.Println("获取相册列表出错：" + err.Error())
		os.Exit(0)
	}
	str := string(bytes)
	str = str[15:]
	str = str[:strings.LastIndex(str, ")")]
	albumList := gjson.Get(str, "data.albumListModeSort")
	return albumList.String(), nil
}

func HttpGet(url string, msgs ...map[string]string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string)
	if len(msgs) > 0 {
		headers = msgs[0]
	}

	for key, val := range headers {
		req.Header.Set(key, val)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP请求失败. 状态码：%s", resp.Status)
	}

	var buffer [512]byte
	result := bytes.NewBuffer(nil)
	for {
		n, err := resp.Body.Read(buffer[0:])
		result.Write(buffer[0:n])
		if err != nil && err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
	}
	return result.Bytes(), nil
}

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

/**
* 远程文件下载，支持断点续传，支持实时进度显示
* @param string uri 远程资源地址
* @param string target 调用时传入文件名，如果支持断点续传时当程序超时程序会自动调用该方法重新下载，此时传入的是文件句柄
* @param interface{} msgs 可变参数，参数顺序 0: retry int（下载失败后重试次数） 1：timeout int 超时，默认300s 2：progressbar bool 是否开启进度条，默认false
 */
func Download(uri string, target string, msgs ...interface{}) (map[string]interface{}, error) {
	filename := filepath.Base(target)
	entension := filepath.Ext(target)
	var targetDir string
	if entension != "" {
		filename = strings.Replace(filename, entension, "", 1)
		targetDir = filepath.Dir(target)
	} else {
		lasti := strings.LastIndex(target, "/")
		if lasti == -1 {
			return nil, fmt.Errorf("Not the correct file address")
		}
		targetDir = target[:lasti]
	}

	if (!IsDir(targetDir)) {
		os.MkdirAll(targetDir, os.ModePerm)
	}

	retry := 0
	if len(msgs) > 0 {
		retry = msgs[0].(int)
	}

	timeout := 300
	if len(msgs) > 1 {
		timeout = msgs[1].(int)
	}

	progressbar := false
	if len(msgs) > 2 {
		progressbar = msgs[2].(bool)
	}

	hresp, err := http.Get(uri)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, retry-1, timeout, progressbar)
		} else {
			return nil, fmt.Errorf("Failed to get response header, Error message → ", err.Error())
		}
	}
	hresp.Body.Close()

	contentRange := hresp.Header.Get("Content-Range")
	acceptRanges := hresp.Header.Get("Accept-Ranges")
	var ranges bool
	if contentRange != "" || acceptRanges == "bytes" {
		ranges = true
	}

	contentType := hresp.Header.Get("Content-Type")
	if contentType != "" && entension == "" {
		exts, err := mime.ExtensionsByType(contentType)
		if err == nil && len(exts) > 0 {
			entension = exts[0]
			filename = fmt.Sprintf("%s%s", filename, entension)
			target = fmt.Sprintf("%s/%s", targetDir, filename)
		}
	}

	var (
		size          int64 = 0
		contentLength int64 = hresp.ContentLength
	)

	if IsFile(target) {
		if ranges {
			fileInfo, _ := os.Stat(target)
			if fileInfo != nil {
				size = fileInfo.Size()
			}
		} else {
			if err := os.Remove(target); err != nil {
				if retry > 0 {
					return Download(uri, target, retry-1, timeout, progressbar)
				} else {
					return nil, err
				}
			}
		}
	}

	res := make(map[string]interface{})
	if size == contentLength {
		res["filename"] = filename
		res["dir"] = targetDir
		res["path"] = target
		return res, nil
	}

	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, retry-1, timeout, progressbar)
		} else {
			return nil, err
		}
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36")
	if ranges {
		req.Header.Set("Accept-Ranges", "bytes")
		req.Header.Set("Range", fmt.Sprintf("bytes=%v-", size))
	}

	client := &http.Client{
		Timeout: time.Second * time.Duration(timeout),
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, retry-1, timeout, progressbar)
		} else {
			return nil, err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if retry > 0 {
			return Download(uri, target, retry-1, timeout, progressbar)
		} else {
			return nil, fmt.Errorf("Download error，http status code：%v，Status：%v", resp.StatusCode, resp.Status)
		}
	}

	file, err := os.OpenFile(target, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, retry-1, timeout, progressbar)
		} else {
			return nil, err
		}
	}
	defer file.Close()

	if progressbar {
		reader := io.LimitReader(io.MultiReader(resp.Body), int64(resp.ContentLength))
		bar := pbar.Full.Start64(resp.ContentLength)
		barReader := bar.NewProxyReader(reader)
		_, err := io.Copy(file, barReader)
		bar.Finish()
		if err != nil {
			if retry > 0 {
				return Download(uri, target, retry-1, timeout, progressbar)
			} else {
				return nil, err
			}
		}
	} else {
		_, err = io.Copy(file, resp.Body)
		if err != nil {
			if retry > 0 {
				return Download(uri, target, retry-1, timeout, progressbar)
			} else {
				return nil, err
			}
		}
	}

	fi, _ := os.Stat(target)
	if fi != nil {
		size = fi.Size()
	}

	if contentLength != size {
		return nil, fmt.Errorf("The source file and the target file size are inconsistent")
	}

	res["filename"] = filename
	res["dir"] = targetDir
	res["path"] = target
	return res, nil
}
