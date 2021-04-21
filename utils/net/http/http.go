package http

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	pbar "github.com/cheggaaa/pb/v3"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	pkgurl "net/url"
	"os"
	"path/filepath"
	"qq-zone/utils/filer"
	"strings"
	"time"
)

/**
 * GET请求
 * @param string url
 * @param map[string]string headers
 */
func Get(url string, headers map[string]string) (http.Header, []byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}

	if headers != nil {
		for key, val := range headers {
			req.Header.Add(key, val)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("The http request failed, the status code is: %s", resp.Status)
	}

	var buffer [512]byte
	result := bytes.NewBuffer(nil)
	for {
		n, err := resp.Body.Read(buffer[0:])
		result.Write(buffer[0:n])
		if err != nil && err == io.EOF {
			break
		} else if err != nil {
			return nil, nil, err
		}
	}
	return resp.Header, result.Bytes(), nil
}

/**
 * GET请求，获取响应头
 * @param string url
 * @param map[string]string headers
 */
func Head(url string, headers map[string]string) (http.Header, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if headers != nil {
		for key, val := range headers {
			req.Header.Add(key, val)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300  {
		return nil, fmt.Errorf("The http request failed, the status code is: %s", resp.Status)
	}
	return resp.Header, nil
}

/**
 * 表单POST请求
 * @param string url 接口地址
 * @param map[string]string params 表单数据，以JSON形式提交
 */
func PostJson(url string, params map[string]interface{}, headers map[string]string) ([]byte, error) {
	var bodyJson []byte
	if params != nil {
		var err error
		bodyJson, err = json.Marshal(params)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyJson))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if headers != nil {
		for key, val := range headers {
			req.Header.Add(key, val)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("The http request failed, the status code is: %s", resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

/**
 * 表单POST请求
 * @param string url 接口地址
 * @param map[string]string params 表单数据，以FormData形式提交
 */
func PostForm(url string, params map[string]string, headers map[string]string) ([]byte, error) {
	var data = pkgurl.Values{}
	if params != nil {
		for key, val := range params {
			data.Set(key, val)
		}
	}

	req, err := http.NewRequest("POST", url, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}

	if headers != nil {
		for key, val := range headers {
			req.Header.Add(key, val)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("The http request failed, the status code is: %s", resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

/**
* 远程文件下载，支持断点续传，支持实时进度显示
* @param string uri 远程资源地址
* @param map[string]string headers 远程资源地址
* @param string target 调用时传入文件名，如果支持断点续传时当程序超时程序会自动调用该方法重新下载，此时传入的是文件句柄
* @param interface{} msgs 可变参数，参数顺序 0: retry int（下载失败后重试次数） 1：timeout int 超时，默认300s 2：progressbar bool 是否开启进度条，默认false
 */
func Download(uri string, target string, headers map[string]string, msgs ...interface{}) (map[string]interface{}, error) {
	filename := filepath.Base(target)
	entension := filepath.Ext(target)
	var targetDir string
	if entension != "" {
		filename = strings.Replace(filename, entension, "", 1)
		targetDir = filepath.Dir(target)
	} else {
		lasti := strings.LastIndex(target, "/")
		if lasti == -1 {
			return nil, fmt.Errorf("Not the correct filer address")
		}
		targetDir = target[:lasti]
	}

	if !filer.IsDir(targetDir) {
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

	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
		} else {
			return nil, err
		}
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.108 Safari/537.36")
	if headers != nil {
		for key, val := range headers {
			req.Header.Add(key, val)
		}
	}

	client := &http.Client{
		Timeout: time.Second * time.Duration(timeout),
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	hresp, err := client.Do(req)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
		} else {
			return nil, fmt.Errorf("Failed to get response header, Error message → ", err.Error())
		}
	}
	defer hresp.Body.Close()

	if hresp.StatusCode < 200 || hresp.StatusCode >= 300 {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
		} else {
			return nil, fmt.Errorf("Http request was not successfully received and processed, status code is %v, status is %v", hresp.StatusCode, hresp.Status)
		}
	}

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
		contentLength = hresp.ContentLength
	)

	if filer.IsFile(target) {
		if ranges {
			fileInfo, _ := os.Stat(target)
			if fileInfo != nil {
				size = fileInfo.Size()
			}
		} else {
			if err := os.Remove(target); err != nil {
				if retry > 0 {
					return Download(uri, target, headers, retry-1, timeout, progressbar)
				} else {
					return nil, err
				}
			}
		}
	}

	if contentLength == 0 {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
		} else {
			return nil, fmt.Errorf("The remote resource pointed to by the URL is invalid")
		}
	}

	res := make(map[string]interface{})
	if size == contentLength {
		res["filename"] = filename
		res["dir"] = targetDir
		res["path"] = target
		return res, nil
	}

	if ranges {
		req.Header.Set("Accept-Ranges", "bytes")
		req.Header.Set("Range", fmt.Sprintf("bytes=%v-", size))
	}

	resp, err := client.Do(req)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
		} else {
			return nil, err
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
		} else {
			return nil, fmt.Errorf("Http request was not successfully received and processed, status code is %v, status is %v", resp.StatusCode, resp.Status)
		}
	}

	file, err := os.OpenFile(target, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0666)
	if err != nil {
		if retry > 0 {
			return Download(uri, target, headers, retry-1, timeout, progressbar)
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
				return Download(uri, target, headers, retry-1, timeout, progressbar)
			} else {
				return nil, err
			}
		}
	} else {
		_, err = io.Copy(file, resp.Body)
		if err != nil {
			if retry > 0 {
				return Download(uri, target, headers, retry-1, timeout, progressbar)
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
		return nil, fmt.Errorf("The source filer and the target filer size are inconsistent")
	}

	res["filename"] = filename
	res["dir"] = targetDir
	res["path"] = target
	return res, nil
}