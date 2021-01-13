package main

import (
	"bufio"
	"bytes"
	"fmt"
	"github.com/tidwall/gjson"
	"net/http"
	_ "net/url"
	"os"
	"path"
	"qq-zone/utils/filer"
	"qq-zone/utils/helper"
	"qq-zone/utils/logger"
	myhttp "qq-zone/utils/net/http"
	"qq-zone/utils/qzone"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	qq          string // 你的QQ号，要下载相册相片/QQ号
	gtk         string // 登陆成功后通过算法拿到的g_tk参数
	tasks       int    // 并行下载的任务数
	cookie      string // 登陆成功跳转到空间主页的cookie
	prevent     string // 是否开启防重复下载，本地已存在即跳过
	mutex       sync.Mutex
	haschan     chan int          // 缓冲信道控制并行下载的任务数
	waiterIn    sync.WaitGroup    // 等待当前相册下载完才能继续下一个相册
	waiterOut   sync.WaitGroup    // 等待所有相片下载完才能继续往下执行
	headers     map[string]string // 公共请求头
	total       uint64 = 0        // 相片/视频总数
	addTotal    uint64 = 0        // 新增数
	succTotal   uint64 = 0        // 下载成功数
	repeatTotal uint64 = 0        // 重复数
	videoTotal  uint64 = 0        // 视频数
	imageTotal  uint64 = 0        // 相片数
	albumSucc   uint64 = 0        // 正在下载的相册相片成功数
	photos      []gjson.Result
	localFiles  map[string]string // 当前本地相册已经存在的文件
)

func main() {
	BeforeDownload()
}

func BeforeDownload() {
	dotted := `
                   .::::.
                 .::::::::.
                :::::::::::
             ..:::::::::::'
          '::::::::::::'
            .::::::::::
       '::::::::::::::..
            ..::::::::::::.
          ` + "``" + `::::::::::::::::
           ::::` + "``" + `:::::::::'        .:::.
          ::::'   ':::::'       .::::::::.
        .::::'      ::::     .:::::::'::::.
       .:::'       :::::  .:::::::::' ':::::.
      .::'        :::::.:::::::::'      ':::::.
     .::'         ::::::::::::::'         ` + "``" + `::::.
 ...:::           ::::::::::::'              ` + "``" + `::.
` + "````" + ` ':.          ':::::::::'                  ::::..
                   '.:::::'                    ':'` + "````" + `..

※※※※※※※※※※ QQ空间相册相片/ 视频下载器 ※※※※※※※※※

说明：本程序基于GO语言多协程开发，绿色无毒，不存在收录用户数据等情况，请放心使用 ^_^
使用：双击运行.exe可执行文件，然后根据终端提示操作即可，相片和日志文件默认存放在根目录storage文件夹
技巧：为了能占满带宽满速下载，100兆宽带最佳并行下载数为8~15，200兆16~30，以此类推，实际使用可根据自身情况调整

※※※※※※※※※※※※※※※※※※※※※※※※※※※※※※※※※`

	fmt.Println(dotted)

Start:
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("请输入QQ号：")
		scanner.Scan()
		qq = scanner.Text()
		_, err := strconv.ParseFloat(qq, 64)
		if qq == "" || err != nil {
			fmt.Println("QQ号不正确，请重新输入~")
			continue
		}
		break
	}

	for {
		fmt.Printf("请输入1~100之间的并行下载任务数，默认为1：")
		scanner.Scan()
		task := scanner.Text()
		if task == "" {
			tasks = 1
		} else {
			var err error
			tasks, err = strconv.Atoi(task)
			if err != nil || tasks < 1 || tasks > 100 {
				fmt.Println("并行下载任务数不正确，输入范围为1~100之间的整数，请重新输入~")
				continue
			}
		}
		break
	}

	for {
		fmt.Printf("是否开启防重复下载，可选[y/n]，默认是y：")
		scanner.Scan()
		prevent = scanner.Text()
		if prevent == "" {
			prevent = "y"
		} else {
			prevent = strings.ToLower(prevent)
			if prevent != "y" && prevent != "n" {
				fmt.Println("防重复下载输入不正确，可选[y/n]，请重新输入~")
				continue
			}
		}
		break
	}

	fmt.Printf("请输入要下载的相册名，多个相册用空格键隔开，格式[相册1 相册2]，不输入默认下载全部相册：")
	scanner.Scan()
	albumNameStrs := scanner.Text()

	var albumNames []string
	if albumNameStrs != "" {
		albumNames = strings.Split(albumNameStrs, " ")
	} else {
		albumNames = []string{}
	}

	res, err := qzone.Login()
	if err != nil {
		fmt.Println("登录QQ空间异常，正在根据提示重新输入，退出请按Ctrl+Z")
		goto Start
	}

	qrcode := "qrcode.png"
	if filer.IsFile(qrcode) {
		os.Remove(qrcode)
	}

	gtk = res["g_tk"]
	cookie = res["cookie"]
	haschan = make(chan int, tasks)

	fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("登录成功，^_^欢迎%s，%s", res["nickname"], "程序即将开始下载~~~~"))
	fmt.Println()

	// 指定要下载的相册
	whitelist := make(map[string]bool)
	for _, name := range albumNames {
		whitelist[name] = true
	}

	time.Sleep(time.Second * 3)

	headers = make(map[string]string)
	headers["cookie"] = cookie
	headers["user-agent"] = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.100 Safari/537.36"
	albumListUrl := fmt.Sprintf("https://h5.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&callbackFun=shine", gtk, qq, qq)

	// 定时发送心跳，防止cookie过期
	ticker := time.NewTicker(time.Minute * 10)
	go Heartbeat(albumListUrl, ticker)

	// 获取相册列表
	albumList, err := GetAlbumList(albumListUrl)
	if err != nil {
		fmt.Println(fmt.Sprintf("（。・＿・。）ﾉ获取相册列表数据错误，：%v", err.Error()))
		MenuSelection()
	}

	var albumListArr []gjson.Result = gjson.Parse(albumList).Array()
	if len(albumListArr) < 1 {
		fmt.Println(fmt.Sprintf("（。・＿・。）ﾉ没有获取到任何相册数据，可能输入参数有误或cookie已失效~~~", ))
		MenuSelection()
	}

	for _, album := range albumListArr {
		name := album.Get("name").String()
		if len(whitelist) > 0 {
			if _, ok := whitelist[name]; !ok {
				continue
			}
		}

		albumPath := fmt.Sprintf("./storage/qzone/%v/album/%s", qq, name)
		if !filer.IsDir(albumPath) {
			os.MkdirAll(albumPath, os.ModePerm)
		}

		var (
			pageStart       int64 = 0
			pageNum         int64 = 500
			albumPaotoTotal int64 = 0
			totalInAlbum          = album.Get("total").Int()
			photoPageNum          = 1
		)

		photos = make([]gjson.Result, 0)
		for {
			photoListUrl := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_list_photo?g_tk=%v&callback=shine_Callback&mode=0&idcNum=4&hostUin=%v&topicId=%v&noTopic=0&uin=%v&pageStart=%v&pageNum=%v&skipCmtCount=0&singleurl=1&batchId=&notice=0&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&outstyle=json&format=jsonp&json_esc=1&callbackFun=shine", gtk, qq, album.Get("id").String(), qq, pageStart, pageNum)
			b, err := myhttp.Get(photoListUrl, headers)
			if err != nil {
				fmt.Println(fmt.Sprintf("（。・＿・。）ﾉ获取相册图片[%s]第%d页错误:%s", album.Get("name").String(), photoPageNum, err.Error()))
				MenuSelection()
			}
			photoJson := string(b)
			photoJson = photoJson[15:]
			photoJson = photoJson[:strings.LastIndex(photoJson, ")")]
			photoData := gjson.Parse(photoJson)
			photoData = photoData.Get("data")
			photoList := photoData.Get("photoList").Array()
			photos = append(photos, photoList...)
			totalInPage := photoData.Get("totalInPage").Int()
			albumPaotoTotal += totalInPage
			if totalInAlbum == albumPaotoTotal { // 说明这个相册下载完成了
				break
			}
			photoPageNum++
			pageStart += 500
		}
		total += uint64(len(photos)) // 累加相片/视频总数

		if prevent == "y" {
			localFiles = make(map[string]string, 0)
			fpaths, _ := filer.GetAllFiles(albumPath)
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
		for key, photo := range photos {
			waiterIn.Add(1)
			waiterOut.Add(1)
			haschan <- 1
			go StartDownload(key, photo, photos, album, albumPath)
		}
		waiterIn.Wait() // 等待当前相册相片下载完之后才能继续下载下一个相册
	}

	close(haschan)

	waiterOut.Wait()
	ticker.Stop()

	if prevent == "y" {
		if repeatTotal > 0 {
			fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d, 已存在%d", time.Now().Format("2006/01/02 15:04:05"), qq, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal), repeatTotal))
		} else {
			fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d，已存在%d", time.Now().Format("2006/01/02 15:04:05"), qq, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal), repeatTotal))
		}
	} else {
		fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d，失败%d", time.Now().Format("2006/01/02 15:04:05"), qq, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal)))
	}

	fmt.Println()

	MenuSelection()
}

func StartDownload(key int, photo gjson.Result, photos []gjson.Result, album gjson.Result, albumPath string) {
	defer func() {
		<-haschan
		waiterIn.Done()
		waiterOut.Done()

		if e := recover(); e != nil {
			// 打印栈信息
			fmt.Println(fmt.Sprintf("%v 相册[%s]第%d个相片/视频下载过程异常，相片/视频名：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), string(PanicTrace(1))))
			logger.Println(fmt.Sprintf("%v 相册[%s]第%d个相片/视频下载过程异常，相片/视频名：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), string(PanicTrace(1))))
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
		b, err := myhttp.Get(videoUrl, headers)
		if err != nil {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：%s", album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, err.Error()))
			logger.Println(fmt.Sprintf("%v 相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, err.Error()))
			return
		}
		videoJson := string(b)
		videoJson = videoJson[16:]
		videoJson = videoJson[:strings.LastIndex(videoJson, ")")]
		videoData := gjson.Parse(videoJson).Get("data")
		videos := videoData.Get("photos").Array()
		if len(videos) < 1 {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d部视频链接未找到，视频名：%s  视频地址：%s", album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl))
			logger.Println(fmt.Sprintf("%v 相册[%s]第%d部视频链接未找到，视频名：%s  视频地址：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl))
			return
		}
		picPosInPage := videoData.Get("picPosInPage").Int()
		videoInfo := (videos[picPosInPage]).Get("video_info").Map()
		status := videoInfo["status"].Int()
		// 状态为2的表示可以正常播放的视频，也就是已经转换并上传在QQ空间服务器上
		if status != 2 {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d个视频文件无效，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s", album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, photo.Get("name").String()))
			logger.Println(fmt.Sprintf("%v 相册[%s]第%d个视频文件无效，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), photo.Get("name").String(), videoUrl, photo.Get("url").String()))
			return
		}
		source = videoInfo["video_url"].String()
		// 目前QQ空间所有视频都是MP4格式，所以暂时固定后缀名都是.mp4
		fileName = fmt.Sprintf("VID_%s_%s_%s.mp4", shootdate[:8], shootdate[8:], helper.Md5(sloc)[8:24])
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
		fileName = fmt.Sprintf("IMG_%s_%s_%s", shootdate[:8], shootdate[8:], helper.Md5(sloc)[8:24])
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
			if err != nil {
				os.RemoveAll(localFiles[tmpName])
			}
			respHeader.Body.Close()

			if respHeader.ContentLength != fsize {
				os.RemoveAll(localFiles[tmpName])
			} else {
				mutex.Lock()
				if photo.Get("is_video").Bool() {
					videoTotal++
				} else {
					imageTotal++
				}
				succTotal++
				albumSucc++
				repeatTotal++
				output := fmt.Sprintf("[%d/%d]相册[%s]第%d个%s文件下载完成_跳过同名文件", albumSucc, len(photos), album.Get("name").String(), (key + 1), resourceType) + "\n" +
					"下载/完成时间：" + time.Now().Format("2006/01/02 15:04:05") + "\n" +
					"相片/视频原名：" + photo.Get("name").String() + "\n" +
					"相片/视频名称：" + tmpName + path.Ext(p) + "\n" +
					"相片/视频大小：" + filer.FormatBytes(fsize) + "\n" +
					"相片/视频地址：" + source + "\n"
				fmt.Println(output)
				mutex.Unlock()
				return
			}
		}
	}

	target := fmt.Sprintf("%s/%s", albumPath, fileName)
	resp, err := myhttp.Download(source, target, 5, 600, false)
	if err != nil {
		// 记录 某个相册 下载失败的相片
		fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("相册[%s]第%d个%s文件下载出错，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s  错误信息：%s\n", album.Get("name").String(), (key + 1), resourceType, photo.Get("name").String(), source, photo.Get("url").String(), err.Error()))
		logger.Println(fmt.Sprintf("%v 相册[%s]第%d个%s文件下载出错，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s  错误信息：%s", time.Now().Format("2006/01/02 15:04:05"), album.Get("name").String(), (key + 1), resourceType, photo.Get("name").String(), source, photo.Get("url").String(), err.Error()))
		return
	} else {
		mutex.Lock()
		succTotal++
		albumSucc++
		addTotal++
		if photo.Get("is_video").Bool() {
			videoTotal++
		} else {
			imageTotal++
		}

		fileInfo, _ := os.Stat(resp["path"].(string))
		output := fmt.Sprintf("[%d/%d]相册[%s]第%d个%s文件下载完成", (albumSucc), len(photos), album.Get("name").String(), (key + 1), resourceType) + "\n" +
			"下载/完成时间：" + time.Now().Format("2006/01/02 15:04:05") + "\n" +
			"相片/视频原名：" + photo.Get("name").String() + "\n" +
			"相片/视频名称：" + resp["filename"].(string) + "\n" +
			"相片/视频大小：" + filer.FormatBytes(fileInfo.Size()) + "\n" +
			"相片/视频地址：" + source + "\n"
		fmt.Println(output)
		mutex.Unlock()
	}
}

// 跟踪panic堆栈信息
func PanicTrace(kb int) []byte {
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

// 定时发送心跳，防止cookie过期
func Heartbeat(url string, ticker *time.Ticker) {
	for t := range ticker.C {
		t.Format("2006/01/02 15:04:05")
		myhttp.Get(url, headers)
	}
}

// 获取相册列表
func GetAlbumList(url string) (string, error) {
	b, err := myhttp.Get(url, headers)
	if err != nil {
		fmt.Println("（。・＿・。）ﾉ获取相册列表出错：" + err.Error())
		MenuSelection()
	}
	str := string(b)
	str = str[15:]
	str = str[:strings.LastIndex(str, ")")]
	albumList := gjson.Get(str, "data.albumListModeSort")
	return albumList.String(), nil
}

// 菜单选项
func MenuSelection() {
	menus := []string{"********** 菜单选项 **********", "1. 再次重试", "2. 结束退出"}
	for _, v := range menus {
		fmt.Println(v)
	}
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("请输入数字再按回车键：")
		scanner.Scan()
		str := scanner.Text()
		input, err := strconv.Atoi(str)
		if err != nil || input < 1 || input > (len(menus)-1) {
			fmt.Println("(T＿T)输入不正确，请输入菜单选项可选数字~~~")
			continue
		}
		switch input {
		case 1:
			BeforeDownload()
		case 2:
			os.Exit(0)
		}
	}
}
