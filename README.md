# qq-zone
QQ空间爬虫，多协程并发下载相册的相片/视频

#### 前言
QQ相册可以说是存放了好大一部分人生活的点点滴滴，近段时间发现QQ空间莫名会删除短视频或者相片，记得20年的时候也类似新闻报道过，为了快速备份写了此程序，网上看到大部分是使用Python实现的，而且操作过程也都**比较繁琐**，需要打开网页然后F12复制cookie等必要参数，对于非专业的用户来说这显得复杂，因此我写了一个通过**手机扫描登陆**即可下载自己或好友的相册相片/视频，极大简化了用户操作流程，人人都会使用，其他功能请关注~~~

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

![image](https://github.com/qinjintian/qq-zone/blob/main/screenshot.png?raw=true)

#### GITHUB代码已开源，传送门 [https://github.com/qinjintian/qq-zone](https://github.com/qinjintian/qq-zone)

### 若项目思路对您有帮助，请不吝点个赞呗~~~ Thanks~