@echo off
chcp 936 >nul
title 老杨网盘 - 卸载向导
cd /d "%~dp0"
echo.
echo  ====================================================
echo   老杨网盘 - 卸载向导
echo  ====================================================
echo.
echo   [警告] 此操作会停止网盘服务并删除本机配置。
echo   默认保留网盘文件与数据库；只有明确选择才会彻底清理。
echo.

echo [1/4] 正在停止网盘服务...
set "STOPPED=0"
taskkill /IM lywp-server.exe /F >nul 2>&1
if not errorlevel 1 set "STOPPED=1"
taskkill /IM lywp-tray.exe /F >nul 2>&1
if not errorlevel 1 set "STOPPED=1"
if "%STOPPED%"=="1" (
  echo   服务已停止。
) else (
  echo   服务未在运行。
)

set "LYWP_CFG_DIR=%APPDATA%\LYWP"
set "LYWP_CFG=%LYWP_CFG_DIR%\config.json"

echo.
echo [2/4] 当前配置：%LYWP_CFG%
if exist "%LYWP_CFG%" (
  echo   ----------------------------------------------------
  type "%LYWP_CFG%"
  echo   ----------------------------------------------------
  echo   上面 storage_root 对应的目录就是你的网盘主目录。
) else (
  echo   配置文件不存在，可能已经卸载。
)

echo.
set "LYWP_PURGE="
set /p "LYWP_PURGE=是否同时删除数据库与临时数据（不可恢复）？[y/N]："

echo.
echo [3/4] 清理配置与数据...
if /i "%LYWP_PURGE%"=="y" (
  if exist "%LYWP_CFG_DIR%" rd /s /q "%LYWP_CFG_DIR%"
  echo   已删除整个配置与数据目录：%LYWP_CFG_DIR%
) else (
  if exist "%LYWP_CFG%" del /f /q "%LYWP_CFG%"
  echo   已删除配置文件，数据库与网盘文件已保留。
)

echo.
echo [4/4] 收尾...
echo   程序文件夹：%~dp0
echo   卸载脚本自身位于该目录，无法安全删除，请在关闭窗口后手动删除。
echo   如果选择了彻底清理，请按上面显示的 storage_root 手动删除网盘主目录。
echo.
echo  卸载完成。
pause
