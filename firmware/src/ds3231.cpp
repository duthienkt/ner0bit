#include "ds3231.h"

#include <Arduino.h>
#include <Wire.h>

#include "civil_time.h"

namespace ds3231 {

static constexpr uint8_t kAddr = 0x68;
static constexpr uint8_t kRegTime = 0x00;    // 0x00..0x06: giây, phút, giờ, thứ, ngày, tháng, năm
static constexpr uint8_t kRegStatus = 0x0F;
static constexpr uint8_t kRegTemp = 0x11;    // 0x11: °C có dấu, 0x12 bit 7:6: phần tư độ
static constexpr uint8_t kOsf = 0x80;

static uint8_t bcd2bin(uint8_t v) { return (uint8_t)((v >> 4) * 10 + (v & 0x0F)); }
static uint8_t bin2bcd(uint8_t v) { return (uint8_t)(((v / 10) << 4) | (v % 10)); }

static bool readRegs(uint8_t reg, uint8_t* buf, uint8_t len) {
    Wire.beginTransmission(kAddr);
    Wire.write(reg);
    if (Wire.endTransmission(false) != 0) return false;
    if (Wire.requestFrom(kAddr, (size_t)len) != len) return false;
    for (uint8_t i = 0; i < len; i++) buf[i] = (uint8_t)Wire.read();
    return true;
}

static bool writeRegs(uint8_t reg, const uint8_t* buf, uint8_t len) {
    Wire.beginTransmission(kAddr);
    Wire.write(reg);
    Wire.write(buf, len);
    return Wire.endTransmission() == 0;
}

bool begin() {
    Wire.beginTransmission(kAddr);
    return Wire.endTransmission() == 0;
}

bool readUtc(uint32_t& utc) {
    uint8_t r[7];
    if (!readRegs(kRegTime, r, sizeof r)) return false;

    const unsigned sec = bcd2bin(r[0] & 0x7F);
    const unsigned min = bcd2bin(r[1] & 0x7F);
    unsigned hour;
    if (r[2] & 0x40) {  // chế độ 12 h (writeUtc luôn ghi 24 h, nhưng chip có thể bị chỉnh nơi khác)
        hour = bcd2bin(r[2] & 0x1F) % 12 + ((r[2] & 0x20) ? 12 : 0);
    } else {
        hour = bcd2bin(r[2] & 0x3F);
    }
    const unsigned day = bcd2bin(r[4] & 0x3F);
    const unsigned month = bcd2bin(r[5] & 0x1F);
    const int32_t year = 2000 + bcd2bin(r[6]) + ((r[5] & 0x80) ? 100 : 0);

    if (sec > 59 || min > 59 || hour > 23 || day < 1 || day > 31 || month < 1 || month > 12) {
        return false;
    }
    const int32_t days = daysFromCivil(year, month, day);
    utc = (uint32_t)days * 86400u + hour * 3600u + min * 60u + sec;
    return true;
}

bool writeUtc(uint32_t utc) {
    const uint32_t days = utc / 86400u;
    const uint32_t sod = utc % 86400u;
    int32_t y;
    unsigned m, d;
    civilFromDays((int32_t)days, y, m, d);
    if (y < 2000 || y > 2199) return false;  // ngoài dải DS3231 (năm 2 chữ số + bit thế kỷ)

    const uint8_t century = (y >= 2100) ? 0x80 : 0x00;
    uint8_t r[7];
    r[0] = bin2bcd((uint8_t)(sod % 60));
    r[1] = bin2bcd((uint8_t)(sod / 60 % 60));
    r[2] = bin2bcd((uint8_t)(sod / 3600));                 // bit 6 = 0 ⇒ 24 h
    r[3] = (uint8_t)((days + 4) % 7 + 1);                  // 1970-01-01 là thứ Năm; 1 = Chủ nhật
    r[4] = bin2bcd((uint8_t)d);
    r[5] = (uint8_t)(bin2bcd((uint8_t)m) | century);
    r[6] = bin2bcd((uint8_t)(y % 100));
    if (!writeRegs(kRegTime, r, sizeof r)) return false;

    uint8_t status;
    if (!readRegs(kRegStatus, &status, 1)) return false;
    status &= (uint8_t)~kOsf;
    return writeRegs(kRegStatus, &status, 1);
}

bool oscStopped(bool& stopped) {
    uint8_t status;
    if (!readRegs(kRegStatus, &status, 1)) return false;
    stopped = (status & kOsf) != 0;
    return true;
}

bool readTempQuarters(int16_t& quarters) {
    uint8_t r[2];
    if (!readRegs(kRegTemp, r, sizeof r)) return false;
    // Số bù 2 mười bit: phần nguyên có dấu × 4 + phần tư (luôn dương) — vd. 0xFF,0xC0 = −0,25 °C.
    quarters = (int16_t)((int8_t)r[0] * 4 + (r[1] >> 6));
    return true;
}

}  // namespace ds3231
