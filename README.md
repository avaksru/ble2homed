# ble2homed - BLE to MQTT Bridge

Полноценный BLE мост для интеграции Bluetooth Low Energy устройств в экосистему HOMEd.

Сканирует эфир, парсит рекламные пакеты, поддерживает чтение/запись характеристик, хранит историю значений и публикует все данные в MQTT по стандарту HOMEd.


## ✅ Основные возможности

- Непрерывное фоновое сканирование BLE устройств
- Полная совместимость с протоколом HOMEd
- Автоматическое обнаружение устройств в веб интерфейсе HOMEd
- Парсинг рекламных данных без подключения
- Поддержка чтения, записи и подписки на уведомления GATT характеристик
- Встроенная история значений с расчётом средних за 1м/10м/1ч/24ч/7д
- Отдельные топики для каждого сенсора (совместимо с recorder)
- Опциональный веб интерфейс для мониторинга
- Поддержка конфигурации в форматах YAML и JSON
- Низкое потребление ресурсов

## 🚀 Быстрый старт

### Установка

Скомпилировать из исходников:
```bash
git clone https://github.com/avaksru/ble2homed.git
cd ble2homed
go build -o ble2homed ./cmd/ble2homed/
```

Или скачать готовый бинарник со страницы [релизов](https://github.com/avaksru/ble2homed/releases).

### Настройка прав в Linux

Для доступа к BLE адаптеру без прав root:
```bash
sudo setcap 'cap_net_raw,cap_net_admin+eip' ble2homed
```

### Запуск

```bash
# С конфигом по умолчанию
./ble2homed

# С указанием пути к конфигу
./ble2homed -config /etc/ble2homed/config.yaml

# Показать версию
./ble2homed -version
```

### Установка как системный сервис systemd

Создайте файл `/etc/systemd/system/ble2homed.service`:
```ini
[Unit]
Description=BLE to HOMEd Bridge
After=network.target

[Service]
WorkingDirectory=/opt/ble2homed
ExecStart=/opt/ble2homed/ble2homed -config /etc/ble2homed/config.json
Restart=always
RestartSec=10
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW

[Install]
WantedBy=multi-user.target
```

Включите и запустите сервис:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now ble2homed
```

## ⚙️ Конфигурация

Файл конфигурации может быть в формате YAML или JSON.

### Пример конфигурации config.yaml

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
  filter_macs: []     # пустой список = принимать все устройства
  connect: false      # автоматически подключаться для GATT операций

publish:
  base_prefix: "/ble"
  retain_presence: true

history:
  enabled: true
  intervals: ["1m", "10m", "1h", "24h", "7d"]

web:
  enabled: false
  port: 8080

log:
  level: "info"       # debug | info | warn | error
```

## 📡 MQTT Топики (стандарт HOMEd)

Все топики начинаются с префикса указанного в `base_prefix` (по умолчанию `/ble`).

### Основные топики

| Топик                            | Описание                                      | Retain |
|----------------------------------|-----------------------------------------------|--------|
| `{base}/device/{mac}`            | Статус устройства: last_seen, online, name    | ✅      |
| `{base}/expose/{mac}`            | Описание сенсоров для автоматического обнаружения | ✅ |
| `{base}/fd/{mac}`                | Все значения одним JSON объектом              | ✅      |



### История значений

| Топик                            | Описание                                      |
|----------------------------------|-----------------------------------------------|
| `{base}/hist/{interval}/{mac}/{field}` | Среднее значение за период |


## 🛠️ Поддерживаемые устройства и протоколы

✅ Автоматически парсит:
- Espruino / Puck.js (Company ID 0x0590, JSON5 формат)
- Eddystone (URL, TLM телеметрия)
- iBeacon
- Стандартные GATT сервисы:
  - `0x1809` Температура
  - `0x180F` Уровень батареи
  - `0x181A` Влажность
  - `0x2A6E` Температура Цельсия
  - `0x2A6F` Влажность
  - `0x2A19` Уровень заряда батареи

## ❗ Устранение неполадок

| Проблема | Решение |
|----------|---------|
| Ошибка `no BLE adapters found` | Убедитесь что bluetooth включён: `sudo hciconfig hci0 up` |
| Ошибка доступа к адаптеру | Установите права capabilities: `sudo setcap 'cap_net_raw,cap_net_admin+eip' ble2homed` |
| Устройства не найдены | Проверьте что адаптер работает: `sudo hcitool lescan` |
| Не подключается к MQTT | Проверьте адрес брокера, логин и пароль, доступность порта 1883 |
| Низкая скорость сканирования | Установите `scan_timeout: "3s"` в конфигурации |


## 📋 Требования

- Go 1.22+ (для компиляции)
- Linux с Bluetooth адаптером версии 4.0+
- MQTT брокер (Mosquitto, EMQX, Vernemq и др.)
- HOMEd 3.0+ (для полной интеграции)

⚠️ **Важно:** Мост работает только на Linux. Поддержка Windows и macOS не реализована.

## 📦 Зависимости

- `github.com/go-ble/ble` — BLE стек для Linux
- `github.com/eclipse/paho.mqtt.golang` — MQTT клиент
- `github.com/rs/zerolog` — Структурированное логирование
- `gopkg.in/yaml.v3` — Парсер YAML

## 📄 Лицензия

MIT