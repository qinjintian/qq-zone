package controllers

import (
	"bufio"
	"fmt"
	"github.com/tidwall/gjson"
	"net/http"
	"os"
	"path/filepath"
	"qq-zone/utils/filer"
	"qq-zone/utils/helper"
	"qq-zone/utils/logger"
	myhttp "qq-zone/utils/net/http"
	"qq-zone/utils/qzone"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type QzoneController struct {
	BaseController
	ticker     *time.Ticker      // 定时器
	whitelist  map[string]bool   // 要下载的相册
	localFiles map[string]string // 当前本地相册已经存在的文件
}

var (
	chans               chan int       // 缓冲信道控制并行下载的任务数
	mutex               sync.Mutex     // 互斥锁，下载数累加解决竞态
	waiterIn            sync.WaitGroup // 等待当前相册下载完才能继续下一个相册
	waiterOut           sync.WaitGroup // 等待所有相片下载完才能继续往下执行
	total               uint64 = 0     // 相片/视频总数
	addTotal            uint64 = 0     // 新增数
	succTotal           uint64 = 0     // 下载成功数
	repeatTotal         uint64 = 0     // 重复数
	videoTotal          uint64 = 0     // 视频数
	imageTotal          uint64 = 0     // 相片数
	albumPhotoSuccTotal uint64 = 0     // 正在下载的相册相片成功数
)

const (
	QRCODE = "qrcode.png"
	USER_AGENT = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/69.0.3497.100 Safari/537.36"
)

func  (q *QzoneController) initResult() {
	total, addTotal, succTotal, repeatTotal, videoTotal, imageTotal, albumPhotoSuccTotal = 0, 0, 0, 0, 0, 0, 0
}

func Spider() {
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

※※※※※※※※ QQ空间相册相片/ 视频下载器 ※※※※※※※

说明：本程序基于GO语言多协程开发，绿色无毒，不存在收录用户数据等情况，请放心使用 ^_^
使用：双击运行.exe可执行文件，然后根据终端提示操作即可，相片和日志文件默认存放在根目录storage文件夹
技巧：为了能占满带宽满速下载，100兆宽带最佳并行下载数为8~15，200兆16~30，以此类推，实际使用可根据自身情况调整`
	fmt.Println(dotted)

	(&QzoneController{}).menu()
}

// 爬取相册
func (q *QzoneController) spiderAlbum(option int) {
Start:
	scanner := bufio.NewScanner(os.Stdin)
	qq := ""
	for {
		fmt.Printf("请输入您的QQ号：")
		scanner.Scan()
		qq = scanner.Text()
		_, err := strconv.ParseInt(qq, 10, 64)
		if qq == "" || err != nil {
			fmt.Println("您的QQ号不正确，请重新输入~")
			continue
		}
		break
	}

	friendQQ := ""
	if option == 2 {
		for {
			fmt.Printf("请输入您好友的QQ号，留空代表全部：")
			scanner.Scan()
			friendQQ = scanner.Text()
			if friendQQ == "" {
				break
			}
			_, err := strconv.ParseInt(friendQQ, 10, 64)
			if err != nil {
				fmt.Println("（。・＿・。）ﾉ您的好友QQ号不正确，请重新输入~")
				continue
			}
			break
		}
	}

	task := 1
	for {
		fmt.Printf("请输入1~100之间的下载并行任务数，默认为1：")
		scanner.Scan()
		str := scanner.Text()
		if str != "" {
			task, _ = strconv.Atoi(str)
			if task < 1 || task > 100 {
				fmt.Println("并行下载任务数不正确，输入范围为1~100之间的整数，请重新输入~")
				continue
			}
		} else {
			task = 1
		}
		break
	}

	exclude := false
	for {
		fmt.Printf("是否开启防重复下载，可选[y/n]，默认是y：")
		scanner.Scan()
		str := strings.ToLower(scanner.Text())
		if str == "" || str == "y" {
			exclude = true
		} else {
			if str != "y" && str != "n" {
				fmt.Println("防重复下载输入不正确，可选[y/n]，请重新输入~")
				continue
			}
		}
		break
	}

	fmt.Printf("请输入要下载的相册名，多个相册用空格键隔开，格式[相册1 相册2]，不输入默认下载全部相册：")
	scanner.Scan()
	str := scanner.Text()
	var albums = []string{}
	if str != "" {
		albums = strings.Split(str, " ")
	}

	// 指定要下载的相册
	q.whitelist = make(map[string]bool)
	for _, name := range albums {
		q.whitelist[name] = true
	}

	res, err := qzone.Login()
	if err != nil {
		fmt.Println("（。・＿・。）ﾉ登录QQ空间异常，正在根据提示重新输入，退出请按Ctrl+Z")
		goto Start
	}

	// 登陆成功之后删掉二维码
	if filer.IsFile(QRCODE) {
		os.Remove(QRCODE)
	}

	gtk := res["g_tk"]
	cookie := res["cookie"]
	chans = make(chan int, task)

	fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("登录成功，^_^欢迎%s，%s", res["nickname"], "程序即将开始下载~~~~"))
	fmt.Println()

	time.Sleep(time.Second * 2)

	// 为空时自动爬取开放qq相册权限的好友
	if option == 2 && friendQQ == "" {
		// 有时候QQ空间明明有相册但是获取到的却是空的，这应该是tx的问题，因为我多次尝试使用自己的小号在浏览器登陆进入相册，我的账号是有相册的，但是不管怎么刷新都没显示出相册来
		qqs, err := q.openAccess(qq, cookie, gtk)
		if err != nil {
			fmt.Println("（。・＿・。）ﾉ获取对你开放空间的好友出现异常，正在根据提示重新输入，退出请按Ctrl+Z")
			q.menu()
		}

	OuterLoop:
		for _, friendQQ := range qqs {
			q.initResult() // 初始化结果
			err := q.readyDownload(qq, friendQQ, cookie, gtk, exclude)
			if err != nil {
				fmt.Println(err)
				menus := []string{"※※※※※※※※※※※※ 菜 单 选 项 ※※※※※※※※※※※", "⒈ 跳过错误继续进行下一个任务", "⒉ 结束退出"}
				fmt.Println()
				for _, v := range menus {
					fmt.Println(v)
				}
				fmt.Println()
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
						continue OuterLoop
					case 2:
						os.Exit(0)
					}
				}
			}

			if exclude {
				if repeatTotal > 0 {
					fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d, 已存在%d", time.Now().Format("2006/01/02 15:04:05"), friendQQ, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal), repeatTotal))
				} else {
					fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d，已存在%d", time.Now().Format("2006/01/02 15:04:05"), friendQQ, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal), repeatTotal))
				}
			} else {
				fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d，失败%d", time.Now().Format("2006/01/02 15:04:05"), friendQQ, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal)))
			}
		}
	} else {
		err := q.readyDownload(qq, friendQQ, cookie, gtk, exclude)
		if err != nil {
			fmt.Println(err)
			q.menu()
		}

		hostUin := qq
		if friendQQ != "" {
			hostUin = friendQQ
		}

		if exclude {
			if repeatTotal > 0 {
				fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d, 已存在%d", time.Now().Format("2006/01/02 15:04:05"), hostUin, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal), repeatTotal))
			} else {
				fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d, 失败%d，已存在%d", time.Now().Format("2006/01/02 15:04:05"), hostUin, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal), repeatTotal))
			}
		} else {
			fmt.Println(fmt.Sprintf("%v QQ空间[%v]相片/视频下载完成，共有%d张相片/视频，已保存%d张相片/视频，其中%d张相片, %d部视频, 包含新增%d，失败%d", time.Now().Format("2006/01/02 15:04:05"), hostUin, total, succTotal, imageTotal, videoTotal, addTotal, (total - succTotal)))
		}
	}
}

func (q *QzoneController) readyDownload(qq, friendQQ, cookie, gtk string, exclude bool) error {
	header := make(map[string]string)
	header["cookie"] = cookie
	header["user-agent"] = USER_AGENT

	var (
		uin = qq
		hostUin = friendQQ
	)

	if friendQQ == "" {
		hostUin = qq
	}
	url := qzone.GetAlbumListUrl(hostUin, uin, gtk)
	list, err := qzone.GetAlbumList(url, header)
	if err != nil {
		return err

	}

	go q.heartbeat(url, header)

	var albums = gjson.Parse(list).Array()
	if len(albums) < 1 {
		return fmt.Errorf("（。・＿・。）ﾉ 该账号( %v )没有可访问的相册~~~", hostUin)
	}

	for _, album := range albums {
		// 跳过没有无权访问的相册
		if album.Get("allowAccess").Int() == 0 {
			continue
		}

		// 跳过不在白名单中的相册
		name := album.Get("name").String()
		if len(q.whitelist) > 0 {
			if _, ok := q.whitelist[name]; !ok {
				continue
			}
		}

		apath := fmt.Sprintf("./storage/qzone/%v/album/%s", hostUin, name)
		if !filer.IsDir(apath) {
			os.MkdirAll(apath, os.ModePerm)
		}

		photos, err := qzone.GetPhotoList(hostUin, uin, cookie, gtk, album)
		if err != nil {
			return err
		}
		photoTotal := len(photos)
		total += uint64(photoTotal) // 累加相片/视频总数

		if exclude {
			q.localFiles = make(map[string]string, 0)
			files, _ := filer.GetAllFiles(apath)
			for _, path := range files {
				filename := filepath.Base(path)
				filename = filename[:strings.LastIndex(filename, ".")]
				q.localFiles[filename] = path
			}
		} else {
			os.RemoveAll(apath) // 把当前本地相册删掉重新创建空相册然后下载文件，相当于清空目录资源
			os.MkdirAll(apath, os.ModePerm)
		}

		albumPhotoSuccTotal = 0 // 重新初始化为0
		// 正在下载处理
		for key, photo := range photos {
			waiterIn.Add(1)
			waiterOut.Add(1)
			chans <- 1
			go q.StartDownload(hostUin, uin, gtk, cookie, key, photo, album, apath, photoTotal, exclude)
		}
		waiterIn.Wait() // 等待当前相册相片下载完之后才能继续下载下一个相册
	}

	waiterOut.Wait()
	close(chans)
	q.ticker.Stop()
	return nil
}

func (q *QzoneController) StartDownload(hostUin, uin, gtk, cookie string, key int, photo, album gjson.Result, apath string, photoTotal int, exclude bool) {
	defer func() {
		<-chans
		waiterIn.Done()
		waiterOut.Done()

		if err := recover(); err != nil {
			// 打印栈信息
			fmt.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d个相片/视频下载过程异常，相片/视频名：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), string(logger.PanicTrace())))
			logger.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d个相片/视频下载过程异常，相片/视频名：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), string(logger.PanicTrace())))
		}
	}()

	header := make(map[string]string)
	header["cookie"] = cookie
	header["user-agent"] = USER_AGENT

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
	source, cate, filename := "", "", ""
	if photo.Get("is_video").Bool() {
		cate = "视频"
		url := fmt.Sprintf("https://h5.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/cgi_floatview_photo_list_v2?g_tk=%v&callback=viewer_Callback&topicId=%v&picKey=%v&cmtOrder=1&fupdate=1&plat=qzone&source=qzone&cmtNum=0&inCharset=utf-8&outCharset=utf-8&callbackFun=viewer&uin=%v&hostUin=%v&appid=4&isFirst=1", gtk, album.Get("id").String(), sloc, uin, hostUin)
		b, err := myhttp.Get(url, header)
		if err != nil {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("QQ( %v )的相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：%s", hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url, err.Error()))
			logger.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：%s", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url, err.Error()))
			return
		}
		str := string(b)
		str = str[16:strings.LastIndex(str, ")")]
		if !gjson.Valid(str) {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("QQ( %v )的相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：invalid json", hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url))
			logger.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d部视频获取下载链接出错，视频名：%s  视频地址：%s  错误信息：invalid json", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url))
			return
		}
		data := gjson.Parse(str).Get("data")
		videos := data.Get("photos").Array()
		if len(videos) < 1 {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("QQ( %v )的相册[%s]第%d部视频链接未找到，视频名：%s  视频地址：%s", hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url))
			logger.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d部视频链接未找到，视频名：%s  视频地址：%s", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url))
			return
		}
		picPosInPage := data.Get("picPosInPage").Int()
		videoInfo := (videos[picPosInPage]).Get("video_info").Map()
		status := videoInfo["status"].Int()
		// 状态为2的表示可以正常播放的视频，也就是已经转换并上传在QQ空间服务器上
		if status != 2 {
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("QQ( %v )的相册[%s]第%d个视频文件无效，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s", hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url, photo.Get("name").String()))
			logger.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d个视频文件无效，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), photo.Get("name").String(), url, photo.Get("url").String()))
			return
		}
		source = videoInfo["video_url"].String()
		// 目前QQ空间所有视频都是MP4格式，所以暂时固定后缀名都是.mp4
		filename = fmt.Sprintf("VID_%s_%s_%s.mp4", shootdate[:8], shootdate[8:], helper.Md5(sloc)[8:24])
	} else {
		cate = "相片"
		if raw := photo.Get("raw").String(); raw != "" {
			source = raw
		} else if originUrl := photo.Get("origin_url").String(); originUrl != "" {
			source = originUrl
		} else {
			source = photo.Get("url").String()
		}
		// QQ空间相片有不同的文件后缀名，那么不传后缀名的文件名下载的时候会自动获取到对应的文件扩展名
		filename = fmt.Sprintf("IMG_%s_%s_%s", shootdate[:8], shootdate[8:], helper.Md5(sloc)[8:24])
	}

	// 检查是否启用了防重复下载开关,如果开启就忽略下载已经存在的
	if exclude && len(q.localFiles) > 0 {
		pos := strings.LastIndex(filename, ".")
		tmpName := filename
		if pos != -1 {
			tmpName = filename[:pos]
		}

		if p, ok := q.localFiles[tmpName]; ok {
			// 假如本地已经存在改文件名，那就匹配文件大小是否一致
			fileInfo, _ := os.Stat(p)
			fsize := fileInfo.Size()
			respHeader, err := http.Get(source)
			if err != nil {
				os.RemoveAll(q.localFiles[tmpName])
			}
			respHeader.Body.Close()

			if respHeader.ContentLength != fsize {
				os.RemoveAll(q.localFiles[tmpName])
			} else {
				mutex.Lock()
				if photo.Get("is_video").Bool() {
					videoTotal++
				} else {
					imageTotal++
				}
				succTotal++
				albumPhotoSuccTotal++
				repeatTotal++
				output := fmt.Sprintf("[%d/%d]相册[%s]第%d个%s文件下载完成_跳过同名文件", albumPhotoSuccTotal, photoTotal, album.Get("name").String(), (key + 1), cate) + "\n" +
					"下载/完成时间：" + time.Now().Format("2006/01/02 15:04:05") + "\n" +
					"相片/视频原名：" + photo.Get("name").String() + "\n" +
					"相片/视频名称：" + tmpName + filepath.Ext(p) + "\n" +
					"相片/视频大小：" + filer.FormatBytes(fsize) + "\n" +
					"相片/视频地址：" + source + "\n"
				fmt.Println(output)
				mutex.Unlock()
				return
			}
		}
	}

	target := fmt.Sprintf("%s/%s", apath, filename)
	resp, err := myhttp.Download(source, target, 5, 600, false)
	if err != nil {
		// 记录 某个相册 下载失败的相片
		fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("QQ( %v )的相册[%s]第%d个%s文件下载出错，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s  错误信息：%s\n", hostUin, album.Get("name").String(), (key + 1), cate, photo.Get("name").String(), source, photo.Get("url").String(), err.Error()))
		logger.Println(fmt.Sprintf("%v QQ( %v )的相册[%s]第%d个%s文件下载出错，相片/视频名：%s  相片/视频地址：%s  相册列表页地址：%s  错误信息：%s", time.Now().Format("2006/01/02 15:04:05"), hostUin, album.Get("name").String(), (key + 1), cate, photo.Get("name").String(), source, photo.Get("url").String(), err.Error()))
		return
	} else {
		mutex.Lock()
		succTotal++
		albumPhotoSuccTotal++
		addTotal++
		if photo.Get("is_video").Bool() {
			videoTotal++
		} else {
			imageTotal++
		}

		fileInfo, _ := os.Stat(resp["path"].(string))
		output := fmt.Sprintf("[%d/%d]相册[%s]第%d个%s文件下载完成", albumPhotoSuccTotal, photoTotal, album.Get("name").String(), (key + 1), cate) + "\n" +
			"下载/完成时间：" + time.Now().Format("2006/01/02 15:04:05") + "\n" +
			"相片/视频原名：" + photo.Get("name").String() + "\n" +
			"相片/视频名称：" + resp["filename"].(string) + "\n" +
			"相片/视频大小：" + filer.FormatBytes(fileInfo.Size()) + "\n" +
			"相片/视频地址：" + source + "\n"
		fmt.Println(output)
		mutex.Unlock()
	}
}

// 获取对我开放空间权限或开放相册权限的好友
func (q *QzoneController) getAccess(option int) {
Start:
	scanner := bufio.NewScanner(os.Stdin)
	qq := ""
	for {
		fmt.Printf("请输入您的QQ号：")
		scanner.Scan()
		qq = scanner.Text()
		_, err := strconv.ParseInt(qq, 10, 64)
		if qq == "" || err != nil {
			fmt.Println("您的QQ号不正确，请重新输入~")
			continue
		}
		break
	}

	res, err := qzone.Login()
	if err != nil {
		fmt.Println("（。・＿・。）ﾉ登录QQ空间异常，正在根据提示重新输入，退出请按Ctrl+Z")
		goto Start
	}

	fmt.Println(time.Now().Format("2006/01/02 15:04:05"), fmt.Sprintf("登录成功，^_^欢迎%s，%s", res["nickname"], "程序将马上为您查询~~~~"))
	fmt.Println()

	time.Sleep(time.Second * 2)

	// 登陆成功之后删掉二维码
	if filer.IsFile(QRCODE) {
		os.Remove(QRCODE)
	}

	gtk := res["g_tk"]
	cookie := res["cookie"]

	url := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/r.qzone.qq.com/cgi-bin/tfriend/friend_ship_manager.cgi?uin=%v&do=1&fupdate=1&clean=1&g_tk=%v", qq, gtk)
	header := make(map[string]string)
	header["cookie"] = cookie
	header["user-agent"] = USER_AGENT
	str, err := qzone.GetMyFriends(url, header)
	if err != nil {
		fmt.Println(err)
		q.menu()
	}

	friends := gjson.Parse(str).Array()
	ch := make(chan int, 10)
	swg := &sync.WaitGroup{}
	// 遍历检查好友账号是否对自己开放权限
	if option == 1 {
		fmt.Println("以下好友对你开放空间权限↓")
	} else {
		fmt.Println("以下好友对你公开相册权限↓")
	}

	for _, val := range friends {
		swg.Add(1)
		ch <- 1
		go func(val gjson.Result) {
			hostUin := val.Get("uin").String()
			nickname := val.Get("name").String()
			defer func() {
				<-ch
				swg.Done()

				if err := recover(); err != nil {
					// 打印栈信息
					fmt.Println(fmt.Sprintf("%v QQ号：%v  昵称：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), hostUin, nickname, string(logger.PanicTrace())))
					logger.Println(fmt.Sprintf("%v QQ号：%v  昵称：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), hostUin, nickname, string(logger.PanicTrace())))
				}
			}()

			url := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine0_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&callbackFun=shine0", gtk, hostUin, qq)
			body, err := qzone.GetAlbumList(url, header)
			if err != nil {
				return
			}

			if option == 1 {
				fmt.Println(fmt.Sprintf("账号：%v  昵称：%v", hostUin, nickname))
			} else {
				albums := gjson.Parse(body).Array()
				totalInPageModeSort := len(albums) // totalInPageModeSort 包含了需要密码才能访问的相册
				if totalInPageModeSort > 0 {
					// 排除掉需要密码才能访问的相册
					allowAccess := make([]string, 0)
					for _, album := range albums {
						if album.Get("allowAccess").Int() == 0 {
							continue
						}
						allowAccess = append(allowAccess, album.Get("name").String())
					}

					if len(allowAccess) > 0 {
						displays := ""
						for key, val := range allowAccess {
							if key >= 6 {
								break
							}
							displays += val + " "
						}
						fmt.Println(fmt.Sprintf("账号：%v  昵称：%v 相册数：%v 相册名[大于6个时仅显示前面6个]：%v", hostUin, nickname, totalInPageModeSort, displays))
					}
				}
			}
		}(val)
	}
	swg.Wait()
	close(ch)
	q.menu()
}

func (q *QzoneController) openAccess(qq, cookie, gtk string) ([]string, error) {
	url := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/r.qzone.qq.com/cgi-bin/tfriend/friend_ship_manager.cgi?uin=%v&do=1&fupdate=1&clean=1&g_tk=%v", qq, gtk)
	header := make(map[string]string)
	header["cookie"] = cookie
	header["user-agent"] = USER_AGENT
	str, err := qzone.GetMyFriends(url, header)
	if err != nil {
		return nil, err
	}

	ch := make(chan int, 1)
	wg := &sync.WaitGroup{}
	qqs := make([]string, 10)
	friends := gjson.Parse(str).Array()
	for _, val := range friends {
		wg.Add(1)
		ch <- 1
		go func(val gjson.Result) {
			hostUin := val.Get("uin").String()
			nickname := val.Get("name").String()
			defer func() {
				<-ch
				wg.Done()

				if err := recover(); err != nil {
					// 打印栈信息
					fmt.Println(fmt.Sprintf("%v QQ号：%v  昵称：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), hostUin, nickname, string(logger.PanicTrace())))
					logger.Println(fmt.Sprintf("%v QQ号：%v  昵称：%v  Panic信息：%v", time.Now().Format("2006/01/02 15:04:05"), hostUin, nickname, string(logger.PanicTrace())))
				}
			}()

			url := fmt.Sprintf("https://user.qzone.qq.com/proxy/domain/photo.qzone.qq.com/fcgi-bin/fcg_list_album_v3?g_tk=%v&callback=shine0_Callback&hostUin=%v&uin=%v&appid=4&inCharset=utf-8&outCharset=utf-8&source=qzone&plat=qzone&format=jsonp&notice=0&filter=1&handset=4&pageNumModeSort=40&pageNumModeClass=15&needUserInfo=1&idcNum=4&callbackFun=shine0", gtk, hostUin, qq)
			body, err := qzone.GetAlbumList(url, header)
			if err != nil {
				return
			}
			albums := gjson.Parse(body).Array() // 包含了需要密码才能访问的相册
			totalInPageModeSort := len(albums)
			if totalInPageModeSort > 0 {
				// 排除掉需要密码才能访问的相册
				for _, album := range albums {
					if album.Get("allowAccess").Int() == 1 {
						mutex.Lock()
						qqs = append(qqs, hostUin)
						mutex.Unlock()
						return
					}
				}
			}
		}(val)
	}
	wg.Wait()
	return qqs, nil
}

// 定时发送心跳，防止cookie过期
func (q *QzoneController) heartbeat(url string, header map[string]string) {
	q.ticker = time.NewTicker(time.Minute * 10)
	for t := range q.ticker.C {
		t.Format("2006/01/02 15:04:05")
		myhttp.Get(url, header)
	}
}

// 菜单选项
func (q *QzoneController) menu() {
	menus := []string{"※※※※※※※※※※※※ 菜 单 选 项 ※※※※※※※※※※※", "⒈ 下载自己的相册相片/视频", "⒉ 下载好友的相册相片/视频", "⒊ 列出对我开放空间权限的好友", "⒋ 列出对我公开相册权限的好友", "⒌ 结束退出"}
	fmt.Println()
	for _, v := range menus {
		fmt.Println(v)
	}
	fmt.Println()
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
			q.spiderAlbum(1)
		case 2:
			q.spiderAlbum(2)
		case 3:
			q.getAccess(1)
		case 4:
			q.getAccess(2)
		case 5:
			os.Exit(0)
		}
		q.menu()
	}
}
