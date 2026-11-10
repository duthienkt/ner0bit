#pragma once
#include <stdint.h>

#include "storage.h"

// EEPROM AT24C32 (4 KB) trên module DS3231, địa chỉ I2C 0x57, địa chỉ ô nhớ 16 bit.
// Wire.begin(...) phải được gọi trước. Không tự khoá: caller giữ mutex bus I2C.
class At24c32 : public IStorage {
public:
    static constexpr uint16_t kSize = 4096;
    static constexpr uint16_t kPageSize = 32;

    // Đọc theo chunk ≤ 30 byte (vừa buffer Wire 32 byte trên mọi core).
    bool read(uint16_t addr, uint8_t* buf, uint16_t len) override;
    // Ghi tách theo biên page 32 byte; sau mỗi page chờ 5 ms rồi ACK-poll tới khi chip sẵn sàng.
    bool write(uint16_t addr, const uint8_t* buf, uint16_t len) override;
};
