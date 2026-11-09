# Firmware Blink LED — Thiết kế

Ngày: 2026-11-09

## Mục tiêu

Thiết lập cấu trúc monorepo cho Ner0bit và viết firmware thử nghiệm đầu tiên (blink LED onboard) để xác nhận toolchain + nạp code lên ESP32-C3 Super Mini hoạt động.

## Quyết định

- **Framework**: Arduino (chạy trên PlatformIO — extension đã cài trong VSCode).
- **Cấu trúc repo** (monorepo, phương án 1):

```
ner0bit/
├── firmware/              # Project PlatformIO (Arduino)
│   ├── platformio.ini
│   ├── src/main.cpp       # Blink LED onboard GPIO 8
│   └── lib/               # Thư viện local sau này
├── docs/                  # Sơ đồ, tài liệu sau này
├── assets/
└── readme.md
```

Thư mục `server/` sẽ tạo khi bắt đầu viết server.

## Chi tiết kỹ thuật

- Board ESP32-C3 Super Mini → board ID `esp32-c3-devkitm-1`.
- LED onboard ở **GPIO 8, active-LOW** (mức LOW = LED sáng).
- USB-native → cần `-DARDUINO_USB_CDC_ON_BOOT=1` để Serial hoạt động.
- Nạp qua `/dev/cu.usbmodem1101`, tốc độ monitor 115200.

## Kiểm tra

1. `pio run` build thành công (lần đầu tải toolchain).
2. Nạp firmware, LED onboard nhấp nháy chu kỳ 1 giây.
3. Serial monitor in `Blink #N` với N tăng dần.
