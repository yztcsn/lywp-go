@echo off
chcp 936 >nul
title 老杨网盘 - 安装向导
cd /d "%~dp0"
echo.
echo  ====================================================
echo   老杨网盘 - 安装向导
echo  ====================================================
echo.
set "LYWP_NAME=老杨网盘"
set /p "LYWP_NAME=请输入网盘名称（直接回车用默认：老杨网盘）："
set "LYWP_PASS="
set /p "LYWP_PASS=请设置访问密码（必填）："
if not defined LYWP_PASS (
  echo.
  echo [错误] 密码不能为空，安装已取消。
  pause
  exit /b 1
)
set "LYWP_STORAGE=%USERPROFILE%\LYWP"
echo.
echo  存储路径：直接回车使用  %LYWP_STORAGE%
set /p "LYWP_STORAGE=请输入新的存储路径："
set "LYWP_PORT=8080"
set /p "LYWP_PORT=请输入端口（直接回车用 8080）："
echo.
echo  正在安装，请稍候...
"%~dp0lywp-server.exe" install -name "%LYWP_NAME%" -password "%LYWP_PASS%" -storage "%LYWP_STORAGE%" -port %LYWP_PORT%
if errorlevel 1 (
  echo.
  echo [错误] 安装失败，请检查上面填写的信息后重试。
  pause
  exit /b 1
)
echo.
echo  ====================================================
echo   安装完成！
echo   双击 start-tray.bat 以托盘模式启动网盘
echo   双击 start.bat     以命令行模式启动网盘
echo   访问地址： http://localhost:%LYWP_PORT%
echo  ====================================================
echo.
pause
