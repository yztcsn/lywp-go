@echo off
chcp 936 >nul
title 停止老杨网盘
set "STOPPED=0"
taskkill /IM lywp-server.exe /F >nul 2>&1
if not errorlevel 1 set "STOPPED=1"
taskkill /IM lywp-tray.exe /F >nul 2>&1
if not errorlevel 1 set "STOPPED=1"
if "%STOPPED%"=="1" (
  echo.
  echo  网盘服务已停止。
) else (
  echo.
  echo  [提示] 没有正在运行的网盘服务。
)
pause
