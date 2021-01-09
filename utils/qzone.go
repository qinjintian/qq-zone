package utils

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	pkgurl "net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

var headers map[string]string

func init() {
	headers = make(map[string]string)
	headers["user-agent"] = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36"
}

// 登录获取g_tk和cookie参数才能进入相册
func Login() (map[string]string, error) {
	r, err := loopIfLogin()
	if err != nil {
		return nil, err
	}

	res := make(map[string]string)
	res["nickname"] = r["nickname"]
	credential, err := getCredential(r["redirect"])
	if err != nil {
		return nil, err
	}
	res["g_tk"] = credential["g_tk"]
	res["cookie"] = credential["cookie"]
	return res, nil
}

// 循环检查用户是否扫描成功以及是否登录成功
func loopIfLogin() (map[string]string, error) {
StartLoop:
	loginSig, err := getLoginSig()
	if err != nil {
		return nil, err
	}

	header, err := getQRC()
	if err != nil {
		return nil, err
	}

	qrsig := strings.Replace(strings.Split(header.Get("Set-Cookie"), ";")[0], "qrsig=", "", 1)
	ptqrtoken := ptqrtoken(qrsig)
	res := make(map[string]string)

OuterLoop:
	for {
		str, err := ifLogin(ptqrtoken, loginSig, qrsig)
		if err != nil {
			return nil, err
		}

		if !strings.Contains(str, "") {
			return nil, errors.New("未知错误001，请刷新重试！")
		}

		s := strings.Split(strings.ReplaceAll(str[strings.Index(str, "(")+1:len(str)-1], "'", ""), ",")
		// 间隔3秒循环一次
		time.Sleep(time.Second * 3)
		// 65 二维码已失效 66 二维码未失效 67 已扫描,但还未点击确认 0  已经点击确认,并登录成功
		switch s[0] {
		case "65":
			goto StartLoop
		case "66":
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), "二维码已生成在根目录，请双击打开[qrcode.png]并使用手机QQ扫码登录")
			continue OuterLoop
		case "67":
			fmt.Println(time.Now().Format("2006/01/02 15:04:05"), "已扫描,请点击允许登录")
			continue OuterLoop
		case "0":
			// 已经点击确认,并登录成功
			res["nickname"] = s[len(s)-1]
			res["redirect"] = s[2]
			break OuterLoop
		default:
			return nil, errors.New("未知错误002，请刷新重试！")
		}
	}
	return res, nil
}

// 检查用户是否扫描成功以及是否登录成功
func ifLogin(ptqrtoken string, loginSig string, qrsig string) (string, error) {
	header := make(map[string]string)
	header["user-agent"] = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36"
	header["cookie"] = fmt.Sprintf("qrsig=%s;", qrsig)
	url := fmt.Sprintf("https://ssl.ptlogin2.qq.com/ptqrlogin?u1=%s&ptqrtoken=%v&ptredirect=0&h=1&t=1&g=1&from_ui=1&ptlang=2052&action=%v&js_ver=21010623&js_type=1&login_sig=%v&pt_uistyle=40&aid=549000912&daid=5&has_onekey=1", pkgurl.QueryEscape("https://qzs.qq.com/qzone/v5/loginsucc.html?para=izone"), ptqrtoken, action(), loginSig)
	b, err := httpGet(url, header)
	if err != nil {
		return "", errors.New(err.Error())
	}
	return string(b), nil
}

// 随机数
func t() string {
	return strconv.FormatFloat(rand.Float64(), 'g', -1, 64)
}

// 获取二维码
func getQRC() (http.Header, error) {
	url := "https://ssl.ptlogin2.qq.com/ptqrshow?appid=549000912&e=2&l=M&s=3&d=72&v=4&t=" + t() + "&daid=5&pt_3rd_aid=0"
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	file, err := os.OpenFile("qrcode.png", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return nil, err
	}
	return resp.Header, nil
}

// 获取login_sig参数
func getLoginSig() (string, error) {
	url := "https://xui.ptlogin2.qq.com/cgi-bin/xlogin?proxy_url=https://qzs.qq.com/qzone/v6/portal/proxy.html&daid=5&&hide_title_bar=1&low_login=0&qlogin_auto_login=1&no_verifyimg=1&link_target=blank&appid=549000912&style=22&target=self&s_url=https://qzs.qq.com/qzone/v5/loginsucc.html?para=izone&pt_qr_app=手机QQ空间&pt_qr_link=https://z.qzone.com/download.html&self_regurl=https://qzs.qq.com/qzone/v6/reg/index.html&pt_qr_help_link=https://z.qzone.com/download.html&pt_no_auth=0"
	resp, err := http.Get(url)
	if err != nil {
		return "", errors.New(err.Error())
	}
	resp.Body.Close()

	setCookies := resp.Header.Values("Set-Cookie")
	if len(setCookies) < 1 {
		return "", errors.New("获取login_sig参数时错误，请稍后重试")
	}

	var loginSig string
	for _, val := range setCookies {
		if strings.Contains(val, "pt_login_sig=") {
			s := strings.Split(val, ";")
			for _, v := range s {
				if strings.Contains(v, "pt_login_sig=") {
					loginSig = strings.Replace(v, "pt_login_sig=", "", 1)
				}
			}
		}
	}

	if loginSig == "" {
		return "", errors.New("获取login_sig参数时错误，请稍后重试")
	}
	return loginSig, nil
}

/**
 * 获获取ptqrttoken参数
 * header http.Header 将获取二维码接口的headers传进来
 */
func ptqrtoken(qrsig string) string {
	e := 0
	for i := 0; i < len(qrsig); i++ {
		e += (e << 5) + int(qrsig[i])
	}
	return strconv.Itoa(2147483647 & e)
}

// 获取action参数
func action() string {
	return fmt.Sprintf("0-0-%d", time.Now().Unix()*1000)
}

// 登录成功，验证进入空间的签名
func getCredential(url string) (map[string]string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var (
		p_skey string
		needs = []string{"uin", "skey", "p_uin", "pt4_token", "p_skey"} // 需要从set-cookie取的参数
		cookie = make([]string, 0)
	)

	setCookies := resp.Header.Values("Set-Cookie")
	for _, val := range setCookies {
		c := strings.Split(strings.Split(val, ";")[0], "=")
		name := c[0]
		value := c[1]
		for _, ckey := range needs {
			if name == ckey && value != "" {
				if ckey == "p_skey" {
					p_skey = value
				}
				cookie = append(cookie, fmt.Sprintf("%s=%s", name, value))
			}
		}
	}

	res := make(map[string]string)
	res["g_tk"] = gtk(p_skey)
	res["cookie"] = strings.Join(cookie, "; ")
	return res, nil
}

// 获取登录成功之后的g_tk参数
func gtk(skey string) string {
	h := 5381
	for i := 0; i < len(skey); i++ {
		h += (h << 5) + int(skey[i])
	}
	return strconv.Itoa(h & 2147483647)
}

// 获取登录成功进入空间活动的cookie
func getCookie() {

}

func httpGet(url string, msgs ...map[string]string) ([]byte, error) {
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
		return nil, fmt.Errorf("HTTP request failed, status code：%s", resp.Status)
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