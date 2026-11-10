#pragma once
#include <stdint.h>

// Bộ nhớ bền trừu tượng (thực tế: AT24C32 4 KB; test: RAM).
// read/write trả false nếu lỗi bus hoặc vượt dung lượng.
struct IStorage {
    virtual bool read(uint16_t addr, uint8_t* buf, uint16_t len) = 0;
    virtual bool write(uint16_t addr, const uint8_t* buf, uint16_t len) = 0;
    virtual ~IStorage() = default;
};
