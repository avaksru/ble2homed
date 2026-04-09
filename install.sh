#!/bin/sh
set -e

# Скрипт установки homed-ble для Linux и OpenWrt
# Версия: 1.0

APP_NAME="homed-ble"
REPO="avaksru/ble2homed"
BIN_DEST_LINUX="/usr/bin/$APP_NAME"
BIN_DEST_OPENWRT="/usr/bin/$APP_NAME"
CONF_DEST="/etc/homed/$APP_NAME.conf"
SYSTEMD_SERVICE="/etc/systemd/system/$APP_NAME.service"
OPENWRT_INIT="/etc/init.d/$APP_NAME"

echo "🚀 Начинаем установку $APP_NAME"

# 1. Останавливаем службу если существует
echo "⏹️  Останавливаем существующую службу..."
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet $APP_NAME; then
        systemctl stop $APP_NAME
        echo "✅ Служба systemd остановлена"
    fi
elif [ -f $OPENWRT_INIT ]; then
    if $OPENWRT_INIT running; then
        $OPENWRT_INIT stop
        echo "✅ Служба OpenWrt остановлена"
    fi
fi

# 2. Останавливаем все запущенные процессы
echo "⏹️  Завершаем запущенные процессы $APP_NAME..."
if pgrep -x "$APP_NAME" >/dev/null; then
    killall $APP_NAME 2>/dev/null || true
    sleep 1
    if pgrep -x "$APP_NAME" >/dev/null; then
        killall -9 $APP_NAME 2>/dev/null || true
    fi
    echo "✅ Все процессы завершены"
fi

# 3. Определяем архитектуру системы
echo "🔍 Определяем архитектуру системы..."
ARCH=$(uname -m)
case $ARCH in
    x86_64|amd64)
        BIN_ARCH="amd64"
        ;;
    aarch64|arm64)
        BIN_ARCH="arm64"
        ;;
    armv7l|armv7)
        BIN_ARCH="armv7"
        ;;
    armv6l|armv6)
        BIN_ARCH="armv6"
        ;;
    i386|i686)
        BIN_ARCH="386"
        ;;
    *)
        echo "❌ Неизвестная архитектура: $ARCH"
        exit 1
        ;;
esac

echo "✅ Обнаружена архитектура: $BIN_ARCH"

# 4. Получаем последнюю версию релиза
echo "🔍 Получаем последнюю версию..."
LATEST_TAG=$(curl -s https://api.github.com/repos/$REPO/releases/latest | grep -o '"tag_name": ".*"' | cut -d'"' -f4)
if [ -z "$LATEST_TAG" ]; then
    echo "❌ Не удалось получить последнюю версию"
    exit 1
fi
echo "✅ Последняя версия: $LATEST_TAG"

# 5. Скачиваем архив
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_TAG/ble2homed-linux-$BIN_ARCH.tar.gz"
echo "📥 Скачиваем $DOWNLOAD_URL"
TEMP_DIR=$(mktemp -d)
curl -L -o "$TEMP_DIR/archive.tar.gz" "$DOWNLOAD_URL"

# 6. Распаковываем
echo "📦 Распаковываем архив..."
tar -xzf "$TEMP_DIR/archive.tar.gz" -C "$TEMP_DIR"

# 7. Копируем бинарный файл
echo "📋 Копируем исполняемый файл..."
if [ -f "$TEMP_DIR/ble2homed" ]; then
    cp -f "$TEMP_DIR/ble2homed" "$BIN_DEST_LINUX"
    chmod +x "$BIN_DEST_LINUX"
    echo "✅ Бинарный файл установлен в $BIN_DEST_LINUX"
else
    echo "❌ Бинарный файл не найден в архиве"
    exit 1
fi

# 8. Копируем конфиг если он не существует
echo "📋 Копируем конфигурацию..."
mkdir -p /etc/homed
if [ ! -f "$CONF_DEST" ]; then
    if [ -f "$TEMP_DIR/config.yaml" ]; then
        cp "$TEMP_DIR/config.yaml" "$CONF_DEST"
        echo "✅ Конфигурация установлена в $CONF_DEST"
    else
        echo "⚠️  Файл конфигурации не найден в архиве"
    fi
else
    echo "ℹ️  Конфигурация уже существует, не перезаписываем"
fi

# 9. Устанавливаем службу
if command -v systemctl >/dev/null 2>&1; then
    echo "⚙️  Устанавливаем systemd службу..."
    cat > $SYSTEMD_SERVICE << EOF
[Unit]
Description=Homed BLE Bridge Service
After=network.target

[Service]
Type=simple
User=root
ExecStart=$BIN_DEST_LINUX --config $CONF_DEST
Restart=always
RestartSec=5
SyslogIdentifier=$APP_NAME

[Install]
WantedBy=multi-user.target
EOF

    chmod 644 $SYSTEMD_SERVICE
    systemctl daemon-reload
    systemctl enable $APP_NAME
    echo "✅ Systemd служба установлена и добавлена в автозапуск"

    echo "▶️  Запускаем службу..."
    systemctl start $APP_NAME

else
    # OpenWrt init.d
    echo "⚙️  Устанавливаем OpenWrt init скрипт..."
    cat > $OPENWRT_INIT << EOF
#!/bin/sh /etc/rc.common

START=99
STOP=10

USE_PROCD=1
PROG=$BIN_DEST_OPENWRT
CONFIG=$CONF_DEST

start_service() {
    procd_open_instance
    procd_set_param command \$PROG --config \$CONFIG
    procd_set_param respawn 3600 5 10
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_close_instance
}
EOF

    chmod +x $OPENWRT_INIT
    $OPENWRT_INIT enable
    echo "✅ OpenWrt служба установлена и добавлена в автозапуск"

    echo "▶️  Запускаем службу..."
    $OPENWRT_INIT start
fi

# 10. Очищаем временные файлы
rm -rf "$TEMP_DIR"

echo ""
echo "✅✅✅ Установка успешно завершена!"
echo ""
echo "📝 Конфигурация: $CONF_DEST"
echo "🔍 Проверить статус службы:"
if command -v systemctl >/dev/null 2>&1; then
    echo "   systemctl status $APP_NAME"
    echo "📜 Посмотреть логи:"
    echo "   journalctl -u $APP_NAME -f"
else
    echo "   $OPENWRT_INIT status"
    echo "📜 Посмотреть логи:"
    echo "   logread -f | grep $APP_NAME"
fi
echo ""
echo "⚙️  Не забудьте настроить параметры в конфигурационном файле и перезапустить службу"