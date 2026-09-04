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
 * @LastEditTime: 2026-09-04 17:10:00
 * @FileName: qzone.go
 * @Description: [QQ 空间扫码登录流程实现，包含二维码生成、状态轮询及登录凭证提取]
 */

package qzone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	ihttp "github.com/qinjintian/qq-zone/internal/net/http"
	goqrcode "github.com/skip2/go-qrcode"
	"github.com/tuotoo/qrcode"
)

const (
	// QRCodeSavePath 登录二维码的临时落盘路径，登录结束（成功或失败）后会删除。
	QRCodeSavePath = "qrcode.png"
	// UserAgent 登录链路使用的浏览器标识，需与网页端 ptlogin 接口保持一致。
	UserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// LoginHandler 处理 QQ 空间扫码登录相关的全部交互逻辑。
type LoginHandler struct {
	http *ihttp.Client // 请求 ptlogin / xlogin 所用的 HTTP 客户端
}

// NewLoginHandler 初始化一个新的登录处理器
func NewLoginHandler(httpClient *ihttp.Client) *LoginHandler {
	return &LoginHandler{
		http: httpClient,
	}
}

// Login 执行扫码登录流程
// 包含获取登录凭证、下载二维码、轮询扫码状态、提取并验证最终 Cookie 的全过程
func (q *LoginHandler) Login(ctx context.Context) (map[string]string, error) {
	r, err := q.loopUntilLogin(ctx)
	// 无论登录成功还是失败，都清理掉根目录下的二维码图片
	_ = os.Remove(QRCodeSavePath)
	if err != nil {
		return nil, err
	}

	identity, err := q.getCredentials(ctx, r["redirect"], r["cookies"])
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"nickname": r["nickname"],
		"g_tk":     identity["g_tk"],
		"cookie":   identity["cookie"],
	}, nil
}

// loopUntilLogin 轮询二维码直到登录成功或上下文取消。
// 二维码过期（状态 65）会重新拉 login_sig 并换一张码；成功时返回昵称、跳转地址和已捕获的 Cookie。
func (q *LoginHandler) loopUntilLogin(ctx context.Context) (map[string]string, error) {
StartLoop:
	loginSig, err := q.getLoginSig(ctx)
	if err != nil {
		return nil, err
	}

	header, err := q.downloadQRCode(ctx)
	if err != nil {
		return nil, err
	}

	// qrsig 写在 ptqrshow 的 Set-Cookie 里，后续轮询必须原样带上。
	var qrsig string
	for _, cookie := range header.Values("Set-Cookie") {
		if val := extractCookieValue(cookie, "qrsig"); val != "" {
			qrsig = val
			break
		}
	}
	ptqrtoken := q.generatePtqrtoken(qrsig)

	isFirstLoop := true
	capturedCookies := make(map[string]string)
	capturedCookies["qrsig"] = qrsig

	for {
		if isFirstLoop {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}

		str, header, err := q.checkLoginStatus(ctx, ptqrtoken, loginSig, qrsig)
		if err != nil {
			return nil, err
		}

		// 确认登录前后 ptlogin 会下发 ptcz、RK，后续换 p_skey 时要一起带上。
		for _, c := range header.Values("Set-Cookie") {
			for _, key := range []string{"ptcz", "RK"} {
				if val := extractCookieValue(c, key); val != "" {
					capturedCookies[key] = val
				}
			}
		}

		// ptqrlogin 返回 JSONP：ptuiCB('状态','...','跳转URL',...,'昵称')
		content := str[strings.Index(str, "(")+1 : strings.LastIndex(str, ")")]
		s := strings.Split(content, ",")
		for i := range s {
			s[i] = strings.TrimSpace(strings.ReplaceAll(s[i], "'", ""))
		}

		switch s[0] {
		case "65": // 二维码过期
			fmt.Println(time.Now().Format("15:04:05"), "二维码失效，正在重新生成...")
			goto StartLoop
		case "66": // 等待扫码；只在第一次打印终端二维码，避免刷屏
			if isFirstLoop {
				fmt.Println(time.Now().Format("15:04:05"), "二维码已生成，请扫码登录")
				q.printQRCodeToTerminal()
			}
			isFirstLoop = false
		case "67": // 已扫码，等待手机确认
			fmt.Println(time.Now().Format("15:04:05"), "已扫码，请在手机上点击确认")
			isFirstLoop = true
		case "0": // 登录成功，s[2] 是换票跳转地址
			nickname := ""
			if len(s) >= 6 {
				nickname = s[5]
			}

			var cookieParts []string
			for k, v := range capturedCookies {
				cookieParts = append(cookieParts, k+"="+v)
			}

			return map[string]string{
				"nickname": nickname,
				"redirect": s[2],
				"cookies":  strings.Join(cookieParts, "; "),
			}, nil
		default:
			return nil, errors.New("登录过程发生未知错误")
		}
	}
}

// checkLoginStatus 向 ptqrlogin 查询一次当前二维码状态（真正的轮询在 loopUntilLogin）。
// 返回的 JSONP 状态码如：65(失效)、66(等待扫码)、67(已扫码待确认)、0(登录成功)。
func (q *LoginHandler) checkLoginStatus(ctx context.Context, ptqrtoken, loginSig, qrsig string) (string, http.Header, error) {
	headers := map[string]string{
		"user-agent": UserAgent,
		"cookie":     "qrsig=" + qrsig + ";",
	}

	apiURL := fmt.Sprintf("https://ssl.ptlogin2.qq.com/ptqrlogin?u1=%s&ptqrtoken=%v&ptredirect=0&h=1&t=1&g=1&from_ui=1&ptlang=2052&action=0-0-%d&js_ver=21010623&js_type=1&login_sig=%v&pt_uistyle=40&aid=549000912&daid=5&has_onekey=1", url.QueryEscape("https://qzs.qq.com/qzone/v5/loginsucc.html?para=izone"), ptqrtoken, time.Now().Unix()*1000, loginSig)
	header, body, code, err := q.http.Get(ctx, apiURL, headers)
	if err != nil {
		return "", nil, err
	}
	if code != 200 {
		return "", nil, fmt.Errorf("check login status failed with status: %d", code)
	}
	return string(body), header, nil
}

// downloadQRCode 获取最新的 QQ 空间登录二维码图片流，并落盘保存
func (q *LoginHandler) downloadQRCode(ctx context.Context) (http.Header, error) {
	// t 是随机数，防止中间层把过期二维码缓存下来。
	apiURL := fmt.Sprintf("https://ssl.ptlogin2.qq.com/ptqrshow?appid=549000912&e=2&l=M&s=3&d=72&v=4&t=%f&daid=5&pt_3rd_aid=0", rand.Float64())
	header, body, code, err := q.http.Get(ctx, apiURL, map[string]string{"user-agent": UserAgent})
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("download qrcode failed with status: %d", code)
	}

	file, err := os.Create(QRCodeSavePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if _, err = io.Copy(file, strings.NewReader(string(body))); err != nil {
		return nil, err
	}

	return header, nil
}

// getLoginSig 访问 xlogin 页面，从 Set-Cookie 取出 pt_login_sig。
// 该签名要原样传给 ptqrlogin，否则轮询会被服务端拒绝。
func (q *LoginHandler) getLoginSig(ctx context.Context) (string, error) {
	apiURL := "https://xui.ptlogin2.qq.com/cgi-bin/xlogin?proxy_url=https://qzs.qq.com/qzone/v6/portal/proxy.html&daid=5&&hide_title_bar=1&low_login=0&qlogin_auto_login=1&no_verifyimg=1&link_target=blank&appid=549000912&style=22&target=self&s_url=https://qzs.qq.com/qzone/v5/loginsucc.html?para=izone&pt_qr_app=手机QQ空间&pt_qr_link=https://z.qzone.com/download.html&self_regurl=https://qzs.qq.com/qzone/v6/reg/index.html&pt_qr_help_link=https://z.qzone.com/download.html&pt_no_auth=0"
	header, _, code, err := q.http.Get(ctx, apiURL, map[string]string{"user-agent": UserAgent})
	if err != nil {
		return "", err
	}
	if code != 200 {
		return "", fmt.Errorf("get login sig failed with status: %d", code)
	}

	var loginSig string
	for _, cookie := range header.Values("Set-Cookie") {
		if sig := extractCookieValue(cookie, "pt_login_sig"); sig != "" {
			loginSig = sig
			break
		}
	}

	if loginSig == "" {
		return "", errors.New("获取 login_sig 参数错误，请稍后重试")
	}
	return loginSig, nil
}

// getCredentials 访问扫码成功后的跳转地址，收集空间接口所需 Cookie，并用 p_skey 算出 g_tk。
func (q *LoginHandler) getCredentials(ctx context.Context, redirectURL string, initialCookies string) (map[string]string, error) {
	headers := map[string]string{
		"User-Agent": UserAgent,
	}
	if initialCookies != "" {
		headers["Cookie"] = initialCookies
	}

	// 关键凭证写在 302 的 Set-Cookie 上。若自动跟随跳转，这些头会被后续响应盖掉。
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, "GET", redirectURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	cookiesMap := make(map[string]string)
	if initialCookies != "" {
		for _, part := range strings.Split(initialCookies, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) == 2 {
				cookiesMap[kv[0]] = kv[1]
			}
		}
	}

	var pSkey string
	// 空间 CGI 实际会用到的登录 Cookie；其它 Set-Cookie 忽略以免把串撑得过长。
	needs := map[string]bool{
		"uin": true, "skey": true, "p_uin": true, "pt4_token": true, "p_skey": true,
		"ptcz": true, "RK": true, "pt2ggid": true, "pgv_pvid": true,
	}

	for _, c := range resp.Header.Values("Set-Cookie") {
		parts := strings.SplitN(strings.Split(c, ";")[0], "=", 2)
		if len(parts) != 2 {
			continue
		}
		name, val := parts[0], parts[1]
		if val == "" {
			continue
		}
		if needs[name] {
			if name == "p_skey" {
				pSkey = val
			}
			cookiesMap[name] = val
		}
	}

	var cookieParts []string
	for k, v := range cookiesMap {
		cookieParts = append(cookieParts, k+"="+v)
	}

	if pSkey == "" {
		return nil, errors.New("登录失败：未能获取到关键凭证 p_skey，请尝试重新扫码")
	}

	g_tk := calculateGTK(pSkey)

	return map[string]string{
		"g_tk":   g_tk,
		"cookie": strings.Join(cookieParts, "; "),
	}, nil
}

// printQRCodeToTerminal 将下载到本地的二维码图片通过 qrcode 终端库，转换为适合在命令行显示的 ASCII 字符矩阵
func (q *LoginHandler) printQRCodeToTerminal() {
	fi, err := os.Open(QRCodeSavePath)
	if err != nil {
		return
	}
	defer fi.Close()

	// 先从图片解出授权 URL，再用 go-qrcode 画成终端字符块。
	qrmatrix, err := qrcode.Decode(fi)
	if err != nil {
		return
	}

	qr, err := goqrcode.New(qrmatrix.Content, goqrcode.Low)
	if err != nil {
		return
	}
	// Low 纠错让图案更小；ToSmallString(false) 保留白边，手机扫码更稳。
	fmt.Println(strings.TrimRight(qr.ToSmallString(false), "\n"))
}

// generatePtqrtoken 基于腾讯前端的 qrsig 签名算法生成轮询用的 ptqrtoken
// 它是腾讯 API 用于校验二维码状态合法性的核心参数
func (q *LoginHandler) generatePtqrtoken(qrsig string) string {
	e := 0
	for i := 0; i < len(qrsig); i++ {
		e += (e << 5) + int(qrsig[i])
	}
	return strconv.Itoa(2147483647 & e)
}

// calculateGTK 根据用户的 p_skey 算法生成 QQ 空间专用的 CSRF 校验 Token (g_tk)
// 这是腾讯系 API 防跨站请求伪造的核心签名算法
func calculateGTK(pSkey string) string {
	h := 5381
	for i := 0; i < len(pSkey); i++ {
		h += (h << 5) + int(pSkey[i])
	}
	return strconv.Itoa(h & 2147483647)
}

// extractCookieValue 从原始 Cookie 头字符串中解析提取指定 key 的值
func extractCookieValue(header, key string) string {
	parts := strings.Split(header, ";")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, key+"=") {
			return strings.TrimPrefix(p, key+"=")
		}
	}
	return ""
}
