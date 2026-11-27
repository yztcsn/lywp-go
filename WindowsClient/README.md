# LYWP Windows 客户端

纯 Go 实现的轻量网盘 Windows 版，支持 **Windows 10 / 11 64 位**。不需要 PHP、Nginx、MySQL，也不需要管理员权限。前端沿用原版界面与 logo，22 款皮肤由服务端统一保存。

## 快速开始（双击即可，不会闪退）

1. 双击 `bin\install.bat`，按提示输入网盘名称、访问密码、存储路径和端口。
2. 安装成功后双击 `bin\start-tray.bat`，程序以托盘方式在后台运行。
3. 浏览器打开 `http://localhost:8080`（端口以安装时填写的为准）。

> 请不要直接双击 `lywp-server.exe`：它必须带命令参数运行。若未完成安装就双击启动脚本，脚本会提示先运行 `install.bat` 并停留等待按键，不会闪退。

## bin/ 目录内容

| 文件 | 说明 |
| --- | --- |
| `install.bat` | 交互式安装（名称 / 密码 / 存储路径 / 端口） |
| `lywp-server.exe` | 控制台服务端，用于 install / serve / hash 等命令 |
| `lywp-tray.exe` | GUI 子系统托盘程序，启动无控制台黑窗口，logo 托盘图标 |
| `start.bat` | 命令行方式启动（保留控制台） |
| `start-tray.bat` | 托盘方式启动（无控制台窗口） |
| `stop.bat` | 停止服务 |
| `uninstall.bat` | 卸载向导，可选彻底删除数据 |

## 命令行安装

```bat
lywp-server.exe install ^
  -name "老杨网盘" ^
  -password "你的访问密码" ^
  -storage "D:\LYWP" ^
  -port 8080
```

- `-name`：网盘名称，显示在登录页、顶栏和标签页
- `-password`：访问密码，只保存加盐哈希
- `-storage`：网盘主目录，默认 `<用户目录>\LYWP`
- `-port`：端口，默认 `8080`
- `-config`：配置路径，默认 `%APPDATA%\LYWP\config.json`
- `-theme`：初始皮肤，默认 `ocean`

配置写入 `%APPDATA%\LYWP\config.json`，数据（SQLite 数据库、会话、临时文件）在 `%APPDATA%\LYWP\data`，不会混进网盘文件里。

## 托盘模式

双击 `start-tray.bat` 或 `bin\lywp-tray.exe`。`lywp-tray.exe` 使用 Windows GUI 子系统编译，启动不会创建控制台窗口；系统托盘显示 logo 图标：

- 左键双击图标：打开 `http://localhost:端口`
- 右键菜单：「打开网盘」「退出」
- 选择「退出」会先正常关闭 HTTP 服务再退出

若托盘启动失败（端口占用、配置损坏），会弹出错误提示框显示原因。

## 功能

- 登录密码、分享链接（密码 / 过期时间 / 下载次数上限）
- 文件上传、文件夹上传、拖拽上传、在线预览
- 流媒体在线播放：HTTP Range 边下边播、拖动跳转；主站与分享页均支持
- 下载、批量队列下载、批量 ZIP、搜索、重命名、移动、删除
- 文件夹加密：密码保存在目录内 `.encry`，未解锁时隐藏文件、搜索与 ZIP
- 离线下载：http/https 直接流式下载；magnet / FTP / BT 种子交给 aria2
- 响应式界面，22 款皮肤（含亮色、直角、毛玻璃）；手机窄屏头像不变形

## 关于 aria2

http/https 离线下载不需要额外工具。magnet、FTP 和 `.torrent` 需要 aria2：下载 Windows 64 位 aria2，把 `aria2c.exe` 放到 `lywp-server.exe` 同目录或加入 PATH，下次离线任务会自动找到它。没有 aria2 时其他功能不受影响。

## 重新构建

安装 Go 1.22+ 后，在 Windows 命令行执行 `build.bat`。构建使用 `CGO_ENABLED=0` 与 `-ldflags="-s -w"`，不依赖 GCC。

## 卸载

双击 `bin\uninstall.bat`：先停止服务，再删除配置。默认保留数据库与网盘文件；彻底清理会删除 `%APPDATA%\LYWP`（网盘主目录需按脚本提示的 `storage_root` 手动删除），最后手动删除程序文件夹。
