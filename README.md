# LYWP · Go 版网盘

[![License: GPL v3](https://img.shields.io/badge/License-GPL%20v3-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.22%2B-00ADD8.svg)](https://go.dev/)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20Debian%2012-orange.svg)](https://www.debian.org/)
[![Arch](https://img.shields.io/badge/Arch-amd64%20%7C%20arm64-green.svg)](https://github.com/yztcsn/lywp-go/releases)

[English](README_EN.md) · 简体中文

纯 Go 实现的轻量网盘，单文件运行，**不依赖 PHP / Nginx / MySQL**，SQLite 存储分享、会话与离线任务。前端沿用原 PHP 版完整界面（含原 logo），主站与分享页共用同一套皮肤，操作方式与原版一致。

适合个人、家庭或小型团队在一台 Debian 12 机器上快速搭建私有文件中心。

---

## 特性一览

- **零依赖运行**：单个二进制 + 一个配置文件，无需 PHP / Nginx / MySQL
- **安装即用**：交互式 `install.sh` 设置名称、主目录、端口、密码，自动注册 systemd 开机自启
- **完整文件管理**：列表 / 新建文件夹 / 单文件与整文件夹上传 / 下载 / 重命名 / 移动 / 删除 / 搜索 / 右键菜单 / 多选批量操作
- **流媒体在线播放**：视频、音频通过 HTTP Range 边下边播，可拖动进度条任意跳转；主站和分享页都支持，边下边播不占内存
- **分享链接**：可设访问密码、有效期、下载次数上限；二维码、免登录直链、分享管理；文件夹分享逐文件统计下载次数
- **离线下载**：http / https / FTP / 磁力 / BT 种子；服务端流式下载或 aria2 处理，支持自动解压
- **文件夹加密**：右键加密，密码以加盐哈希保存，进入目录需解锁
- **22 款皮肤**：顶栏一键切换，服务端全局保存，登录页 / 网盘 / 分享页 / 所有访客保持一致，重启后仍保留
- **大文件优化**：流式上传内存恒定，前端 4 路并行；>256 MiB 大文件下载交给浏览器流式落盘，不在内存拼 Blob
- **无限制**：文件大小无限制，分享与登录会话默认永久有效
- **自主可控**：网盘主目录安装时可自定义，数据库与网盘文件分离存放

---

## 快速安装（Debian 12）

需要 root 权限。下载 Release 里的二进制包并解压，然后运行：

```bash
sudo bash install.sh
```

脚本依次询问：

1. 网盘名称（显示在页面上）
2. **网盘主目录**（默认 `/var/lib/lywp/files`；可填任意已有目录，如 `/mnt/nas/mydisk`，不存在会自动创建，必须可写）
3. 监听端口（默认 `8080`）
4. 访问密码（隐藏输入，需确认一次）

安装完成后访问 `http://服务器IP:端口/`，记得放行防火墙端口：

```bash
sudo ufw allow 8080/tcp
```

### 静默安装（跳过交互）

用环境变量预填，适合自动化部署：

```bash
sudo env SITE_NAME=我的网盘 \
  LYWP_STORAGE_ROOT=/mnt/disk/mydisk \
  PORT=8080 \
  LYWP_PASSWORD=123456 \
  bash install.sh
```

> ⚠️ 主目录不能是 `/`、`/etc`、`/var` 等系统目录，也不能包含内部数据目录 `/var/lib/lywp`（否则数据库会显示成网盘文件）。

### 从源码编译安装

如果想自己编译，安装 Go 1.22+ 后：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/lywp-server-linux-amd64 .
sudo bash install.sh
```

`install.sh` 会优先使用 `bin/` 下对应架构的预编译二进制，找不到时自动改用本机 Go 编译。

---

## 安装内容

| 路径 | 说明 |
| --- | --- |
| `/opt/lywp` | 源码与 `lywp-server` 可执行文件 |
| 安装时填写的网盘主目录 | 网盘根目录，上传的文件都放在这里（默认 `/var/lib/lywp/files`） |
| `/var/lib/lywp` | 内部数据：`lywp.db` SQLite 数据库、临时文件 |
| `/etc/lywp/config.json` | 配置（仅保存密码哈希，权限 700） |
| `/etc/systemd/system/lywp.service` | systemd 服务，开机自启 |
| `/usr/local/bin/lywp` | 快捷命令 |

---

## 快捷命令

```bash
lywp start                 # 启动
lywp stop                  # 停止
lywp restart               # 重启（也接受 lywp resatrt）
lywp status                # 查看状态
lywp log                   # 跟随查看日志（Ctrl+C 退出）
lywp log --no-follow       # 只看最近 200 行
```

---

## 功能详解

### 分享链接

创建分享时可以填写：

- **访问密码**：分享页需输入密码才能查看
- **有效期**：到期后链接自动失效（`expire_days=0` 表示永久）
- **下载次数限制**：例如填 `20`，第 20 次下载完成后链接立即失效。计数发生在服务端真实下载请求上，浏览器刷新与 Range 续传不会重复计数

分享文件夹后，文件列表新增「下载次数」列，显示每个文件（文件夹显示其下所有文件之和）的累计下载次数。

### 离线下载

工具栏「离线下载」按钮打开任务面板：

- 支持 `http://`、`https://`、`ftp://`、`magnet:`、`.torrent` 直链，也可上传本地 `.torrent` 种子
- 默认保存到当前文件夹，任务面板可查看进度、取消、重试、删除记录
- http/https 由服务端直接流式下载，压缩包可勾选自动解压
- 磁力 / 种子 / FTP 由 `aria2c` 完成：安装脚本自动安装；若被删除，服务会以 root 身份首次使用时自动重装一次；无外网 / 无 apt 权限时可手动 `sudo apt-get install -y aria2`

### 文件夹加密

右键文件夹选择「加密文件夹」，输入两次密码后，服务器在该文件夹内写入隐藏文件 `.encry`（加盐 SHA-256 哈希，非明文）。该文件不会出现在列表、搜索和 ZIP 下载中。进入加密文件夹需输入密码，解锁在当前会话内有效（24 小时）。忘记密码可在服务器上删除对应 `.encry` 文件取消保护。

### 皮肤切换

顶栏「皮肤」按钮提供 22 款皮肤（配色、直角 / 圆角、亮色 / 暗色、毛玻璃等）。选择后写入 `config.json` 的 `theme` 字段，服务端全局生效：登录页、网盘、分享页及所有访客看到的都是同一套皮肤，重启后仍保留。

### 修改时间

文件夹的修改时间按「递归聚合」计算：文件夹下任意层级的文件或子文件夹发生修改、上传、删除、移动，该文件夹的修改时间都会更新为其中最新的时间。

---

## 传输与并发

- **无大小 / 时限限制**：上传按流写入磁盘（4 MiB 缓冲区），分享默认永久
- **并行上传**：前端最多 4 个文件同时上传，文件夹上传也拆进同一任务池
- **并行下载**：服务端支持 Range 断点续传；大文件（>256 MiB）直接交给浏览器下载管理器，避免内存爆掉
- **数据库并发**：SQLite WAL + `synchronous=NORMAL`，最多 8 个连接；下载次数用条件 UPDATE 同步累加，并发下也不会超额
- **ZIP 优化**：已压缩格式（jpg / png / mp4 / mkv / zip 等）直接用 Store 方式打包，不重复 Deflate；打包改为浏览器原生流式保存

---

## 本地开发

```bash
# 生成配置（也可用 go run . hash <password> 单独生成哈希）
LYWP_CONFIG=/tmp/lywp/config.json \
LYWP_NAME=LYWP \
LYWP_PORT=8080 \
LYWP_PASSWORD=123456 \
LYWP_DATA_DIR=/tmp/lywp/data \
LYWP_STORAGE_ROOT=/tmp/lywp/mydisk \
go run . init-config

go run . serve --config /tmp/lywp/config.json
```

交叉编译：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o lywp-server-linux-amd64 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o lywp-server-linux-arm64 .
```

---

## 目录结构

```
.
├── main.go                # 入口、静态资源、优雅退出
├── config.go              # 配置读写、密码哈希
├── store.go               # SQLite：分享、下载计数、会话、离线任务
├── fs.go                  # 路径安全、文件列表、ZIP 流
├── handlers.go            # 原生 /api/* 接口
├── compat.go              # api.php / auth.php 兼容层（供原版前端直接使用）
├── encryption.go          # 文件夹 .encry 加密与统一路径校验
├── offline.go             # 离线下载任务与 aria2 对接
├── storage_linux.go       # Linux 磁盘空间统计
├── storage_other.go       # 非 Linux 开发回退
├── web/                   # 前端：index.html / share.html / theme.css / logo / 静态 JS
├── bin/                   # Linux 预编译二进制（amd64 / arm64）
├── install.sh             # 安装脚本
└── uninstall.sh           # 卸载脚本
```

---

## 卸载

```bash
sudo bash uninstall.sh             # 卸载程序与服务，保留网盘主目录里的文件
sudo bash uninstall.sh --purge -y  # 清空自定义网盘主目录并删除内部数据，不可恢复（主目录本身保留）
```

---

## 许可证

[GPL-3.0](LICENSE) © yztcsn
