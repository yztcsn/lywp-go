@echo off
chcp 936 >nul
title 老杨网盘（命令行模式）
cd /d "%~dp0"
if not exist "%APPDATA%\LYWP\config.json" (
  echo.
  echo  [提示] 还没有安装老杨网盘。
  echo  请先双击 install.bat 完成安装，再启动。
  echo.
  pause
  exit /b 1
)
"%~dp0lywp-server.exe" serve
echo.
echo  服务已停止。
pause
