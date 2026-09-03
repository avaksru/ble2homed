# ble2homed - BLE to MQTT Bridge

Полноценный BLE для интеграции Bluetooth Low Energy устройств в экосистему HOMEd.

Сканирует эфир, парсит рекламные пакеты и публикует все данные в MQTT по стандарту HOMEd.


## ✅ Основные возможности

- Непрерывное фоновое сканирование BLE устройств
- Полная совместимость с протоколом HOMEd
- Автоматическое обнаружение устройств в веб интерфейсе HOMEd
- Парсинг рекламных данных без подключения
- Отдельные топики для каждого сенсора (совместимо с recorder)
- Опциональный веб интерфейс для мониторинга
- Поддержка конфигурации в форматах YAML и JSON
- Низкое потребление ресурсов

## 🚀 Быстрый старт

### Установка

✅ Автоматическая установка одной командой:

**Для обычного Linux (Debian, Ubuntu и т.д.):**
```bash
curl -s https://raw.githubusercontent.com/avaksru/ble2homed/master/install.sh | sudo sh
```

**Для OpenWrt:**
```bash
curl -s https://raw.githubusercontent.com/avaksru/ble2homed/master/install.sh | sh
```

Скомпилировать из исходников:
```bash
git clone https://github.com/avaksru/ble2homed.git
cd ble2homed
go build -o ble2homed ./cmd/ble2homed/
```

Или скачать готовый бинарник со страницы [релизов](https://github.com/avaksru/ble2homed/releases).

### Запуск

```bash
# С конфигом по умолчанию
./ble2homed

# С указанием пути к конфигу
./ble2homed -config /etc/ble2homed/config.yaml

# Показать версию
./ble2homed -version
```

## ⚙️ Конфигурация

Файл конфигурации может быть в формате YAML или JSON.

### Пример конфигурации homed-ble.conf

```json
{
  "log": {
    "level": "warn"
  },
  "retain": false,
  "history": {
    "enabled": false
  },
  "ble": {
    "scan_interval": 20,
    "restart_pause": 40
  },
  "web": {
    "enabled": false,
    "port": 8081
  },
  "presence_timeout": 60,
  "mqtt_host": "tcp://localhost:1883",
  "mqtt_prefix": "homed",
  "mqtt_format_json": true
}
```
## 📄 Лицензия

MIT