#!/usr/bin/env bash
# Установка веб-панели WDTT Admin на сервер с wdtt-server.
# Запускать на самом сервере от root:   sudo bash deploy.sh
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DST=/usr/local/bin/wdtt-admin
CFG_DIR=/etc/wdtt-admin
CFG="$CFG_DIR/config.json"
UNIT=/etc/systemd/system/wdtt-admin.service

PORT=8443
HOST=""
MAIN_PASSWORD=""
PANEL_PASSWORD=""
WDTT_DIR=/etc/wdtt
ANSWERS=""
UNINSTALL=0

die() { printf '\n✗ %s\n' "$*" >&2; exit 1; }
say() { printf '• %s\n' "$*"; }

usage() {
  cat <<USAGE
Использование: sudo bash deploy.sh [параметры]
  --port N              порт панели (по умолчанию 8443, HTTPS)
  --host NAME           домен/IP сервера для сертификата (по умолчанию определяется сам)
  --main-password PWD   главный пароль WDTT (если не указан — берётся из $WDTT_DIR/passwords.json или спрашивается)
  --panel-password PWD  пароль входа в панель (если не указан — генерируется)
  --wdtt-dir DIR        каталог конфигурации wdtt-server (по умолчанию $WDTT_DIR)
  --answers FILE        файл с MAIN_PASSWORD=, PANEL_PASSWORD=, PORT=, HOST= (удаляется после чтения)
  --uninstall           удалить панель (данные WDTT не затрагиваются)
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --host) HOST="$2"; shift 2 ;;
    --main-password) MAIN_PASSWORD="$2"; shift 2 ;;
    --panel-password) PANEL_PASSWORD="$2"; shift 2 ;;
    --wdtt-dir) WDTT_DIR="$2"; shift 2 ;;
    --answers) ANSWERS="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; die "неизвестный параметр: $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "Запустите от root: sudo bash deploy.sh"
command -v systemctl >/dev/null || die "Нужен systemd."

if [[ -n "$ANSWERS" ]]; then
  [[ -f "$ANSWERS" ]] || die "Нет файла ответов: $ANSWERS"
  while IFS='=' read -r k v; do
    v="${v%$'\r'}"
    case "$k" in
      MAIN_PASSWORD) MAIN_PASSWORD="$v" ;;
      PANEL_PASSWORD) PANEL_PASSWORD="$v" ;;
      PORT) [[ -n "$v" ]] && PORT="$v" ;;
      HOST) HOST="$v" ;;
    esac
  done < "$ANSWERS"
  shred -u "$ANSWERS" 2>/dev/null || rm -f "$ANSWERS"
fi

if [[ $UNINSTALL -eq 1 ]]; then
  systemctl disable --now wdtt-admin.service 2>/dev/null || true
  rm -f "$UNIT" "$BIN_DST"
  rm -rf "$CFG_DIR"
  systemctl daemon-reload
  say "Панель удалена. wdtt-server не тронут."
  exit 0
fi

[[ "$PORT" =~ ^[0-9]+$ && "$PORT" -ge 1 && "$PORT" -le 65535 ]] || die "Некорректный порт: $PORT"

# Освобождаем порт от прежней копии панели и ищем свободный, если он занят чужим процессом.
systemctl stop wdtt-admin.service 2>/dev/null || true
port_busy() { ss -H -ltn 2>/dev/null | awk '{print $4}' | grep -Eq "[:.]$1\$"; }
if command -v ss >/dev/null && port_busy "$PORT"; then
  ORIG_PORT=$PORT
  while port_busy "$PORT" && [[ $PORT -lt 65535 ]]; do PORT=$((PORT + 1)); done
  say "Порт $ORIG_PORT занят другой программой, использую $PORT (задать свой: --port N)."
fi

# --- бинарник под архитектуру ---
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "Неподдерживаемая архитектура: $(uname -m)" ;;
esac
SRC_BIN="$HERE/bin/wdtt-admin-linux-$ARCH"
[[ -f "$SRC_BIN" ]] || die "Не найден $SRC_BIN (запустите ./build.sh или используйте полный архив)."

# --- wdtt-server должен быть установлен ---
if ! systemctl cat wdtt.service >/dev/null 2>&1; then
  die "wdtt.service не найден. Сначала установите wdtt-server (см. server-installer/README.md)."
fi
systemctl is-active --quiet wdtt.service || say "ВНИМАНИЕ: wdtt.service сейчас не запущен — панель заработает после его старта."

# --- адрес для сертификата ---
if [[ -z "$HOST" ]]; then
  HOST="$(hostname -f 2>/dev/null || hostname)"
  IP="$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || true)"
  [[ -n "$IP" ]] && HOST="$HOST,$IP"
fi

say "Устанавливаю бинарник в $BIN_DST"
install -m 0755 "$SRC_BIN" "$BIN_DST.new"
mv -f "$BIN_DST.new" "$BIN_DST"

# --- главный пароль: аргумент → passwords.json → вопрос ---
if [[ -z "$MAIN_PASSWORD" && -r "$WDTT_DIR/passwords.json" ]]; then
  MAIN_PASSWORD="$(grep -o '"main_password"[[:space:]]*:[[:space:]]*"[^"]*"' "$WDTT_DIR/passwords.json" 2>/dev/null | head -n1 | sed 's/.*:[[:space:]]*"\(.*\)"/\1/' || true)"
  [[ -n "$MAIN_PASSWORD" ]] && say "Главный пароль найден в $WDTT_DIR/passwords.json"
fi

init_panel() {
  local args=(init --config "$CFG" --main-password "$MAIN_PASSWORD" --listen "0.0.0.0:$PORT" --host "$HOST" --wdtt-config-dir "$WDTT_DIR")
  [[ -n "$PANEL_PASSWORD" ]] && args+=(--panel-password "$PANEL_PASSWORD")
  "$BIN_DST" "${args[@]}"
}

OUT=""
for attempt in 1 2 3; do
  if [[ -z "$MAIN_PASSWORD" ]]; then
    [[ -t 0 ]] || die "Главный пароль WDTT не указан (--main-password)."
    read -r -s -p "Главный пароль WDTT-сервера: " MAIN_PASSWORD; echo
  fi
  OUT="$(init_panel)"
  if systemctl is-active --quiet wdtt.service && ! "$BIN_DST" check --config "$CFG" >/dev/null 2>&1; then
    say "Не удалось подтвердить главный пароль через wdtt-server."
    MAIN_PASSWORD=""
    [[ -t 0 ]] || die "Главный пароль неверен."
    continue
  fi
  break
done
[[ -n "$MAIN_PASSWORD" ]] || die "Главный пароль не подтверждён."
chmod 700 "$CFG_DIR"; chmod 600 "$CFG"

# --- systemd ---
cat > "$UNIT" <<UNITEOF
[Unit]
Description=WDTT Admin web panel
After=network-online.target wdtt.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=$BIN_DST serve --config $CFG
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=$CFG_DIR

[Install]
WantedBy=multi-user.target
UNITEOF
systemctl daemon-reload
systemctl enable wdtt-admin.service >/dev/null 2>&1
systemctl restart wdtt-admin.service
sleep 1
systemctl is-active --quiet wdtt-admin.service || { journalctl -u wdtt-admin.service -n 20 --no-pager; die "Служба wdtt-admin не запустилась."; }

# --- файрвол ---
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow "$PORT/tcp" >/dev/null && say "ufw: открыт $PORT/tcp"
elif command -v firewall-cmd >/dev/null && systemctl is-active --quiet firewalld; then
  firewall-cmd --permanent --add-port="$PORT/tcp" >/dev/null && firewall-cmd --reload >/dev/null && say "firewalld: открыт $PORT/tcp"
else
  say "Если у хостера есть внешний файрвол — откройте TCP-порт $PORT."
fi

SHOW_HOST="${HOST%%,*}"
echo
echo "══════════════════════════════════════════════"
echo " WDTT Admin установлена"
echo " Адрес:  https://$SHOW_HOST:$PORT"
if [[ "$OUT" == PANEL_PASSWORD=* ]]; then
  echo " Пароль: ${OUT#PANEL_PASSWORD=}   (сохраните; сменить можно во вкладке «Аккаунт»)"
elif [[ -n "$PANEL_PASSWORD" ]]; then
  echo " Пароль: тот, что вы указали"
else
  echo " Пароль: прежний (панель уже была установлена)"
fi
echo " Сертификат самоподписанный — браузер покажет предупреждение, это нормально."
echo " Управление:  systemctl status wdtt-admin   |   journalctl -u wdtt-admin -f"
echo " Сброс пароля: wdtt-admin passwd"
echo "══════════════════════════════════════════════"
