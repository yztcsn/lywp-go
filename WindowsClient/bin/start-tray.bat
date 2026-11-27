@echo off
chcp 936 >nul
cd /d "%~dp0"
if not exist "%APPDATA%\LYWP\config.json" (
  echo.
  echo  [提示] 还没有安装老杨网盘。
  echo  请先双击 install.bat 完成安装，再启动。
  echo.
  pause
  exit /b 1
)
if not exist "%~dp0lywp-tray.exe" (
  echo.
  echo  [错误] 找不到托盘程序 lywp-tray.exe。
  echo  请重新下载完整程序包，或运行 build.bat 重新构建。
  echo.
  pause
  exit /b 1
)
start "" "%~dp0lywp-tray.exe"
