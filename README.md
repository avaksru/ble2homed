# ble2homed - BLE to MQTT Bridge

Полноценный BLE to MQTT мост с поддержкой HOMEd и Home Assistant MQTT Discovery.

## Особенности

- **Непрерывное сканирование BLE** устройств
- **Три режима публикации** MQTT топиков:
  - `espruinohub` — совместимость с оригинальным ble2homed
  - `homed` — строгий стиль HOMEd (fd, td, expose, device)
  - `both` — публикация в обоих форматах одновременно
- **Home Assistant MQTT Discovery** — автоматическое обнаружение устройств
- **История значений** — кольцевые буферы с расчётом средних за 1m/10m/1h/24h/7d
- **Парсинг BLE advertising**:
  - Espruino/Puck.js (company 0x0590 → JSON5)
  - Eddystone (URL, TLM)
  - iBeacon
  - Известные GATT сервисы (1809=temp, 180F=battery, 181A=humidity)
- **Обработка команд** через MQTT (write, read, notify, ping)
- **Веб-интерфейс** для мониторинга (опционально)
- **Конфигурация** через YAML или JSON
- **Graceful shutdown**

## Установка

```bash
go build -o ble2homed ./cmd/ble2homed/
```

## Запуск

```bash
# С конфигом по умолчанию (ищет config.yaml в текущей директории)
./ble2homed

# С указанием конфига
./ble2homed -config configs/config.yaml

# Показать версию
./ble2homed -version
```

## Конфигурация

Файлы конфигурации: `config.yaml` или `config.json`

### Пример config.yaml

```yaml
mqtt:
  broker: "tcp://localhost:1883"
  username: ""
  password: ""
  client_id: "ble2homed"
  qos: 1

ble:
  adapter: "hci0"
  scan_timeout: "0s"  # 0 = непрерывное сканирование
  filter_macs: []     # пустой = все устройства
  connect: false      # подключаться для GATT операций

publish:
  mode: "both"        # espruinohub | homed | both
  base_prefix: "/ble"
  retain_presence: true

discovery:
  enabled: true
  prefix: "homeassistant"

history:
  enabled: true
  intervals: ["1m", "10m", "1h", "24h", "7d"]

web:
  enabled: false
  port: 8080

log:
  level: "info"       # debug | info | warn | error
```

## MQTT Топики

### Режим espruinohub

```
{base}/presence/{mac}               → "1" / "0" (retain)
{base}/advertise/{mac}              → полный JSON advertising
{base}/advertise/{mac}/rssi         → -67
{base}/advertise/{mac}/name         → "ThermoX"
{base}/advertise/{mac}/temp         → 22.4 (если распознано)
{base}/advertise/{mac}/battery      → 87
{base}/advertise/{mac}/manufacturer/0590 → hex или parsed JSON
{base}/advertise/{mac}/service/1809 → {"temp":22.4}
{base}/json/{mac}/{uuid}            → например {"temp":22.4}
```

### Режим homed

```
{base}/device/{mac}                 → {"last_seen": unix, "online": true} (retain)
{base}/expose/{mac}                 → JSON со списком сенсоров (retain)
{base}/fd/{mac}                     → плоский JSON: {"temp":22.4, "battery":87, "rssi":-67}
{base}/fd/{mac}/temp                → 22.4 (отдельные топики для recorder)
{base}/fd/{mac}/battery             → 87
{base}/fd/{mac}/rssi                → -67
{base}/td/{mac}/write/{service}/{char}   → запись в характеристику
{base}/td/{mac}/read/{service}/{char}    → чтение характеристики
{base}/td/{mac}/notify/{service}/{char}  → подписка на уведомления
{base}/td/{mac}/ping                     → ping устройства
```

### История (доступна в обоих режимах)

```
{base}/hist/{interval}/{mac}/{field}   → среднее за период
{base}/hist/request                    → запрос на пересчёт (опционально)
```

### Home Assistant Discovery

```
homeassistant/sensor/ble_{device_id}/{sensor}/config
homeassistant/binary_sensor/ble_{device_id}/presence/config
```

## Команды

### Запись в характеристику

```bash
# Отправить hex данные
mosquitto_pub -t "/ble/td/aa:bb:cc:dd:ee:ff/write/1809/2A6E" -m "0x1600"

# Отправить строку
mosquitto_pub -t "/ble/td/aa:bb:cc:dd:ee:ff/write/1809/2A6E" -m "22.4"
```

### Чтение характеристики

```bash
mosquitto_pub -t "/ble/td/aa:bb:cc:dd:ee:ff/read/1809/2A6E" -m ""
```

### Подписка на уведомления

```bash
mosquitto_pub -t "/ble/td/aa:bb:cc:dd:ee:ff/notify/1809/2A6E" -m "1"
mosquitto_pub -t "/ble/td/aa:bb:cc:dd:ee:ff/notify/1809/2A6E" -m "0"
```

### Ping

```bash
mosquitto_pub -t "/ble/td/aa:bb:cc:dd:ee:ff/ping" -m ""
```

## Структура проекта

```
cmd/
  ble2homed/
    main.go              # Точка входа
internal/
  config/               # Загрузка config.yaml/config.json
  ble/                  # BLE сканер (go-ble/ble)
  mqtt/                 # MQTT клиент, publisher, subscriber
  discovery/            # Home Assistant MQTT Discovery
  parser/               # Парсинг BLE advertising данных
  history/              # Кольцевые буферы, расчёт средних
  web/                  # Веб-сервер (опционально)
pkg/
  types/                # Общие типы (Device, Advertisement, ParsedValue)
configs/
  config.yaml           # Пример конфига в YAML
  config.json           # Пример конфига в JSON
```

## Требования

- Go 1.22+
- Linux с BLE адаптером (поддерживается через go-ble/ble/linux)
- MQTT брокер (Mosquitto, EMQX и др.)

## Зависимости

- `github.com/go-ble/ble` — BLE стек для Linux
- `github.com/eclipse/paho.mqtt.golang` — MQTT клиент
- `github.com/rs/zerolog` — структурированное логирование
- `gopkg.in/yaml.v3` — YAML парсер

## Лицензия

MIT