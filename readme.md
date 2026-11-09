![image](./assets/Ner0bit_Logo.png)

Ner0bit là dự án hệ thống tưới nước tự động dùng ESP32. Thiết bị kết nối Internet để đồng bộ lịch tưới từ máy chủ và điều khiển tưới theo lịch. Mục tiêu là giảm công tưới thủ công và cho phép cập nhật lịch tưới từ xa.

## Tính năng dự kiến

- Kết nối Wi-Fi để nhận và đồng bộ lịch tưới từ máy chủ.
- Tự động bật và tắt bơm hoặc van điện theo lịch đã đồng bộ.

## Phần cứng dự kiến
- Bo mạch ESP32-C3 supper mini.
- Mạch đồng hồ DS3231 tích hợp AT24C32.
- MOSFET
- Mạch hạ áp (5V).
- Bơm nước (24V).
- Nguồn 19-24V.
- Dây điện đi mạch, board đồng đục lỗ, dây header các loại.
- Nguồn cấp và ống dẫn nước, vòi tưới....

## Cấu trúc repo

```
ner0bit/
├── firmware/    # Firmware ESP32-C3 chính (sẽ tạo khi phát triển thật)
├── playground/  # Code thử nghiệm
│   └── blink/   # Blink LED onboard GPIO 8 (bài kiểm tra toolchain đầu tiên)
├── docs/        # Tài liệu, sơ đồ mạch
└── assets/      # Logo, hình ảnh
```

Thư mục `server/` (API đồng bộ lịch tưới) sẽ được thêm sau.

## Chạy code thử nghiệm

Yêu cầu: [PlatformIO](https://platformio.org/) (extension VSCode hoặc CLI `pio`).

```bash
cd playground/blink
pio run              # build
pio run -t upload    # nạp vào board
pio device monitor   # xem Serial log (Ctrl+C để thoát)
```

Board: ESP32-C3 Super Mini, LED onboard GPIO 8 (active-LOW).

## Trạng thái triển khai

Đang chờ linh kiện

