# LYWP Android 客户端

与 LYWP Go 版服务端配套的 Android 客户端，支持 Android 5.0（API 21）及以上，自动适配手机与平板。

## 架构

原生 Java 外壳 + WebView 加载 Go 版网盘页面，因此服务端全部功能 1:1 可用：文件/文件夹浏览与上传下载、右键移动/重命名/删除、分享（密码/有效期/下载次数）、文件夹加密解锁、离线下载、搜索、22 款皮肤、流媒体边下边播与拖动播放。

客户端本身不含服务端逻辑，所有数据来自你填写的服务器地址。

## 功能

- 输入服务器地址 + 访问密码登录，地址带历史记录下拉
- 支持 `http://` 与 `https://`（含局域网明文地址）
- 自动识别手机 / 平板，并写入 User-Agent
- 无原生顶栏，全屏显示网盘网页；返回、皮肤、退出用网页自带按钮
- 登录页默认勾选「记住登录信息 30 天」：30 天内免输入密码自动登录，每次自动登录续期 30 天
- 退出登录（含网页内点“退出”）立即清除本机保存的密码、Cookie 与有效期
- 文件上传多选；文件夹上传打开系统目录树递归展开
- 下载走系统 `DownloadManager`，带会话 Cookie，通知栏显示进度
- 会话过期后重新打开 App 回到登录页

## 安装

1. 下载 `apk/lywp-android-release.apk`（正式版）或 `apk/lywp-android-debug.apk`（调试版）传到手机/平板安装，需允许安装未知来源应用。
2. 打开 App，填写服务器地址，例如 `http://192.168.1.5:8080`。
3. 填写网盘访问密码，点「连接并登录」。
4. 登录成功后进入网盘，功能与浏览器版一致。

## 从源码编译

需要 Gradle 8.4、Android SDK（platform android-34 / build-tools 34.0.0）与 JDK 17。在项目目录执行：

```bash
export JAVA_HOME="<你的 JDK 17 路径>"
export ANDROID_HOME="<你的 Android SDK 路径>"
gradle assembleDebug --no-daemon --console=plain
```

产物在 `app/build/outputs/apk/debug/app-debug.apk`。若用 Android Studio 打开，它会自动生成 `local.properties`（本仓库已剔除该本机路径文件）。

## 目录结构

```
AndroidClient/
├── apk/                     预编译 APK（release / debug）
├── app/src/main/java/com/lywp/android/
│   ├── ConnectActivity.java   登录页
│   ├── MainActivity.java      WebView 主界面 + 上传/下载/换肤
│   ├── ApiClient.java         原生 HTTP 请求
│   ├── Config.java            配置与会话持久化
│   ├── Device.java            手机/平板识别
│   └── Skins.java / SkinAdapter.java  皮肤数据
├── app/src/main/res/         布局、主题、平板资源、图标
└── build.gradle / settings.gradle / gradle/
```
