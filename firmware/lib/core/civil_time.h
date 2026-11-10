#pragma once
#include <stdint.h>

// Chuyển đổi ngày dương lịch <-> số ngày kể từ 1970-01-01.
// Thuật toán của Howard Hinnant (chrono-Compatible Low-Level Date Algorithms),
// không dùng gmtime/mktime. Header-only để driver DS3231 dùng lại.

// Số ngày kể từ 1970-01-01 của (y, m, d); m ∈ [1,12], d ∈ [1,31].
inline int32_t daysFromCivil(int32_t y, unsigned m, unsigned d) {
    y -= m <= 2;
    const int32_t era = (y >= 0 ? y : y - 399) / 400;
    const unsigned yoe = (unsigned)(y - era * 400);                        // [0, 399]
    const unsigned doy = (153 * (m > 2 ? m - 3 : m + 9) + 2) / 5 + d - 1;  // [0, 365]
    const unsigned doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;            // [0, 146096]
    return era * 146097 + (int32_t)doe - 719468;
}

// Ngược lại: số ngày kể từ 1970-01-01 -> (y, m, d).
inline void civilFromDays(int32_t z, int32_t& y, unsigned& m, unsigned& d) {
    z += 719468;
    const int32_t era = (z >= 0 ? z : z - 146096) / 146097;
    const unsigned doe = (unsigned)(z - era * 146097);                           // [0, 146096]
    const unsigned yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;  // [0, 399]
    const unsigned doy = doe - (365 * yoe + yoe / 4 - yoe / 100);                // [0, 365]
    const unsigned mp = (5 * doy + 2) / 153;                                     // [0, 11]
    d = doy - (153 * mp + 2) / 5 + 1;                                            // [1, 31]
    m = mp < 10 ? mp + 3 : mp - 9;                                               // [1, 12]
    y = (int32_t)yoe + era * 400 + (m <= 2);
}
