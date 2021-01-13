# qq-zone
QQ空间爬虫，多协程并发下载相册的相片/视频

#### 介绍
使用GO语言多协程并发式开发的QQ空间爬虫，通过手机QQ扫码登陆后即可全自动下载相册的相片和视频

#### 环境要求
- go 1.14.4 (我的环境是这个版本，其他版本应该也没问题)
- go mod

#### 使用说明一

1. 把项目下载下来 git clone https://github.com/qinjintian/qq-zone.git

1. 进入到qq-zone目录

1. 构建项目 go build 后会在当前目录下生成一个 qq-zone.exe 可执行文件(windows 10 环境，其他环境自行百度构建)

1. 双击运行 qq-zone.exe，根据终端提示输入QQ账号，然后当前目录下会生成扫码登陆的二维码，扫码即可登陆进行自动下载

#### 使用说明二
#### _**对于不想看代码实现过程，只想直接可以运行使用的情况可以直接下载 qq-zone.exe 可执行文件到本地电脑直接双击运行即可**_

#### 截图
![截图.png](https://static.studygolang.com/210113/6b7d4bd1925a15d773f849e0b103ffc4.png)

#### GITHUB代码已开源，传送门 [https://github.com/qinjintian/qq-zone](https://github.com/qinjintian/qq-zone)

### 若项目思路对您有帮助，请不吝点个赞呗~~~ 在此谢过!!!