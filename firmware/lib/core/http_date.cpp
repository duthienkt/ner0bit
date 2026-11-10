#include "http_date.h"

#include <string.h>

#include "civil_time.h"

namespace {

// Đọc đúng n chữ số thập phân từ p. false nếu gặp ký tự không phải số.
bool digits(const char* p, int n, unsigned& out) {
    unsigned v = 0;
    for (int i = 0; i < n; i++) {
        if (p[i] < '0' || p[i] > '9') return false;
        v = v * 10 + (unsigned)(p[i] - '0');
    }
    out = v;
    return true;
}

// Vị trí của 3 ký tự p trong bảng tên (mỗi tên 3 ký tự, nối liền), -1 nếu không có.
int indexOf3(const char* table, int count, const char* p) {
    for (int i = 0; i < count; i++) {
        if (memcmp(table + 3 * i, p, 3) == 0) return i;
    }
    return -1;
}

}  // namespace

bool parseHttpDate(const char* s, uint32_t& utc) {
    // "Sun, 06 Nov 1994 08:49:37 GMT"
    //  0    5  8   12   17 20 23 26
    constexpr size_t kLen = 29;
    if (s == nullptr || strnlen(s, kLen + 1) != kLen) return false;
    if (indexOf3("MonTueWedThuFriSatSun", 7, s) < 0) return false;
    if (s[3] != ',' || s[4] != ' ' || s[7] != ' ' || s[11] != ' ' || s[16] != ' ' ||
        s[19] != ':' || s[22] != ':' || s[25] != ' ' || memcmp(s + 26, "GMT", 3) != 0) {
        return false;
    }
    const int mon = indexOf3("JanFebMarAprMayJunJulAugSepOctNovDec", 12, s + 8);
    unsigned day, year, hh, mm, ss;
    if (mon < 0 || !digits(s + 5, 2, day) || !digits(s + 12, 4, year) || !digits(s + 17, 2, hh) ||
        !digits(s + 20, 2, mm) || !digits(s + 23, 2, ss)) {
        return false;
    }
    if (year < 1970 || hh > 23 || mm > 59 || ss > 60 || day < 1 || day > 31) return false;

    // Ngày có thật trong tháng: đi vòng days -> civil phải ra lại đúng (y, m, d).
    const unsigned m = (unsigned)mon + 1;
    const int32_t days = daysFromCivil((int32_t)year, m, day);
    int32_t y2;
    unsigned m2, d2;
    civilFromDays(days, y2, m2, d2);
    if (y2 != (int32_t)year || m2 != m || d2 != day) return false;

    const uint64_t t = (uint64_t)days * 86400u + hh * 3600u + mm * 60u + ss;
    if (t > 0xFFFFFFFFu) return false;
    utc = (uint32_t)t;
    return true;
}
