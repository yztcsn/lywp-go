#!/usr/bin/env bash
# LYWP uninstaller. By default the website data in /var/lib/lywp is kept.
set -euo pipefail

APP_DIR="/opt/lywp"
DATA_DIR="/var/lib/lywp"
CONF_DIR="/etc/lywp"
CONF_FILE="$CONF_DIR/config.json"
UNIT_FILE="/etc/systemd/system/lywp.service"
CMD_FILE="/usr/local/bin/lywp"

# Read the configured netdisk root before the config file is removed.
STORAGE_DIR=""
if [[ -f "$CONF_FILE" ]]; then
  STORAGE_DIR="$(sed -n 's/.*"storage_root"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$CONF_FILE" | head -1)"
fi
[[ -n "$STORAGE_DIR" ]] || STORAGE_DIR="$DATA_DIR/files"

green() { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
red() { printf '\033[31m%s\033[0m\n' "$*" >&2; }
die() { red "$*"; exit 1; }

[[ $EUID -eq 0 ]] || die "请使用 root 运行：sudo bash uninstall.sh"

YES=0
PURGE=0
for arg in "$@"; do
  case "$arg" in
    -y|--yes) YES=1 ;;
    --purge) PURGE=1 ;;
    *) die "未知参数：$arg（支持 -y / --purge）" ;;
  esac
done

echo
red "⚠️  此操作将停止并删除 LYWP 网盘服务、程序与配置。"
echo "  默认保留网盘主目录 $STORAGE_DIR 中的文件与内部数据 $DATA_DIR（SQLite 数据库）。"
echo "  使用 --purge 才会一并删除数据，且不可恢复（自定义主目录本身会保留，只清空其中内容）。"
echo

if [[ $YES -ne 1 ]]; then
  read -r -p "确认卸载？[y/N] " ans
  [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "已取消"; exit 0; }
fi

if [[ $PURGE -ne 1 && $YES -ne 1 ]]; then
  read -r -p "是否同时删除网盘数据（不可恢复）？[y/N] " ans2
  [[ "$ans2" == "y" || "$ans2" == "Y" ]] && PURGE=1
fi

green "[1/4] 停止并禁用服务..."
systemctl stop lywp.service 2>/dev/null || true
systemctl disable lywp.service 2>/dev/null || true

green "[2/4] 删除 systemd 单元与快捷命令..."
rm -f "$UNIT_FILE" "$CMD_FILE"
systemctl daemon-reload

green "[3/4] 删除程序与配置..."
rm -rf "$APP_DIR" "$CONF_DIR"

green "[4/4] 处理数据目录..."
if [[ $PURGE -eq 1 ]]; then
  # A user-chosen root may live outside $DATA_DIR; remove its contents but
  # keep the directory itself, since it may be shared with other data.
  case "$STORAGE_DIR/" in
    "$DATA_DIR"/*) ;; # already covered by the removal below
    *)
      case "$STORAGE_DIR" in
        /|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/var)
          red "跳过清空系统目录 $STORAGE_DIR，请手动检查其中的网盘文件。"
          ;;
        *)
          if [[ -d "$STORAGE_DIR" ]]; then
            find "$STORAGE_DIR" -mindepth 1 -depth -delete
            echo "网盘主目录已清空（目录本身保留）：$STORAGE_DIR"
          fi
          ;;
      esac
      ;;
  esac
  rm -rf "$DATA_DIR"
  echo "内部数据目录已删除：$DATA_DIR"
else
  echo "网盘文件已保留：$STORAGE_DIR"
  echo "内部数据已保留：$DATA_DIR"
fi

echo
green "LYWP 已卸载。"
