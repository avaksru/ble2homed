# ble2homed - BLE to MQTT Bridge

Полноценный BLE для интеграции Bluetooth Low Energy устройств в экосистему HOMEd.

Сканирует эфир, парсит рекламные пакеты и публикует все данные в MQTT по стандарту HOMEd.


## ✅ Основные возможности

- Фоновое сканирование BLE устройств (непрерывно или циклами)
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
    "restart_pause": 40,
    "itag_names": ["iTAG"]
  },
  "web": {
    "enabled": false,
    "port": 8081
  },
  "presence_timeout": 60,
  "mqtt_host": "tcp://127.0.0.1:1883",
  "mqtt_prefix": "homed",
  "mqtt_format_json": true,
  "mqtt": {
    "username": "homed",
    "password": "CHANGE_ME",
    "client_id": "homed-ble-CHANGE_ME",
    "qos": 1
  }
}
```

### Авторизация в MQTT-брокере

Если в `mosquitto.conf` включено `allow_anonymous false`, учётные данные нужно задать во вложенном объекте `mqtt`:

- `mqtt.username` — логин;
- `mqtt.password` — пароль;
- `mqtt.client_id` — идентификатор клиента;
- `mqtt.qos` — QoS для публикаций и LWT, по умолчанию `1`.

Поля `mqtt_user` / `mqtt_password` на верхнем уровне не поддерживаются. `mqtt_host` задаёт адрес брокера и имеет приоритет над `mqtt.broker`, поэтому адрес достаточно указать один раз.

Не рекомендуется встраивать логин и пароль прямо в `mqtt_host` вида `tcp://user:pass@host:1883`: Paho понимает такую форму, но пароль хранится в открытом виде в конфиге и требует percent-кодирования спецсимволов. В логах userinfo теперь маскируется, но явные поля `mqtt.username` / `mqtt.password` всё равно предпочтительнее.

Пример `mosquitto.conf`:

```conf
allow_anonymous false
password_file /etc/mosquitto/passwd
acl_file /etc/mosquitto/acl
```

Пример ACL:

```conf
user homed
topic readwrite homed/#
```

Если включён `per_listener_settings true`, `password_file` и `acl_file` должны быть заданы внутри listener'а, к которому подключается `ble2homed`.

В новых сборках MQTT-клиент использует `SetCleanSession(false)`. Поэтому `client_id` должен быть уникальным для каждой системы: при одинаковом ID (по умолчанию `ble2homed`) брокер разрывает предыдущее соединение, публикует LWT `homed/service/ble = offline` и подписка может не восстановиться. При проблеме `Session taken over` очистите persistent-сессию mosquitto или перезапустите брокер.

### Интервалы BLE-сканирования

Параметры задаются в секундах:

- `ble.scan_interval` — длительность одного цикла сканирования. `0` означает непрерывное сканирование без таймаута.
- `ble.restart_pause` — пауза между циклами. Имеет смысл только при `scan_interval > 0`.

Для шлюзов Geniatech GTW360 с модулем Ampak AP6255 (Broadcom BCM43455), где Wi‑Fi и BLE делят 2,4 ГГц и общую антенну, рекомендуется:

```json
"ble": {
  "scan_interval": 10,
  "restart_pause": 10
}
```

| `scan_interval` | `restart_pause` | Поведение |
|---:|---:|---|
| `10` | `10` | Рекомендуется: 10 с сканирования, 10 с паузы для Wi‑Fi |
| `15` | `5` | Больше свежести BLE, меньше запаса для Wi‑Fi |
| `0` | `0` | Непрерывное сканирование; также работает, но без пауз |
| `20` | `40` | Старый пример из документации; слишком долги BLE-пробел |

## 📄 Лицензия

MIT