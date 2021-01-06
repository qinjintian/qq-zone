package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

func Login()  {
	header, err := getQRC()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(0)
	}
	ptqrtoken := ptqrtoken(header)

	loginSig, err := getLoginSig()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(0)
	}

	loginResp, err := ifLogin(ptqrtoken, loginSig)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(0)
	}
	fmt.Println(loginResp)
}

// 检查用户是否扫描成功以及是否登录成功
func ifLogin(ptqrtoken string, loginSig string) (map[string]string, error) {
	url := fmt.Sprintf("https://ssl.ptlogin2.qq.com/ptqrlogin?u1=https://qzs.qq.com/qzone/v5/loginsucc.html?para=izone&ptqrtoken=%s&ptredirect=0&h=1&t=1&g=1&from_ui=1&ptlang=2052&action=%s&js_ver=20121814&js_type=1&login_sig=%s&pt_uistyle=40&aid=549000912&daid=5&", ptqrtoken, action(), loginSig)
	b, err := httpGet(url)
	if err != nil {
		return nil, errors.New(err.Error())
	}
	str := string(b)
	str = strings.ReplaceAll(str[7:strings.LastIndex(str, ")")], "'", "")
	s := strings.Split(str, ",")
	r := make(map[string]string)
	r["nickname"] = s[len(s)-1]
	r["url"] = s[2]
	return r, nil
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

	file, err := os.OpenFile("QRC.png", os.O_RDWR|os.O_CREATE, 0666)
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
func ptqrtoken(header http.Header) string {
	qrsig := strings.Replace(strings.Split(header.Get("Set-Cookie"), ";")[0], "qrsig=", "", 1)
	e := 0
	for i := 0; i < len(qrsig); i++ {
		e += e << 5
		e += int(qrsig[i])
	}
	return strconv.Itoa(2147483647 & e)
}

// 获取action参数
func action() string {
	return fmt.Sprintf("0-0-%d", time.Now().Unix() * 1000)
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