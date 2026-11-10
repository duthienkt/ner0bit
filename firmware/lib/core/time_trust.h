#pragma once
#include <stdint.h>

// Quyết định giờ nào được ghi vào DS3231. NTP không xác thực: ai giả được gói NTP trong LAN là
// xê dịch được đồng hồ (bơm tưới sai giờ). Neo thời gian là header `Date` của response 200 qua
// HTTPS (TLS xác thực server); NTP chỉ được tinh chỉnh trong ±kTrustWindowS quanh giờ tin cậy.
// Logic thuần, không Arduino — xem docs/system-design.md mục "Đồng bộ thời gian".

constexpr uint32_t kMinValidUtc = 1767225600;  // 2026-01-01T00:00:00Z — nhỏ hơn ⇒ vô lý
constexpr uint32_t kMaxValidUtc = 4102444800;  // 2100-01-01T00:00:00Z — từ đây trở lên ⇒ vô lý
constexpr uint32_t kTrustWindowS = 5;          // lệch ≤ 5 s so với giờ tin cậy coi là khớp
constexpr uint32_t kAnchorMaxAgeS = 24 * 3600; // neo Date cũ từ 24 h trở lên ⇒ hết hạn

enum class NtpVerdict : uint8_t {
    Accept,  // ghi DS3231 theo NTP
    Reject,  // vô lý, hoặc lệch quá kTrustWindowS so với neo Date / DS3231: bỏ, ghi log
};

enum class DateVerdict : uint8_t {
    Write,     // DS3231 không hợp lệ hoặc lệch > kTrustWindowS ⇒ ghi writeUtc
    NoChange,  // DS3231 khớp Date ⇒ không ghi
    Reject,    // Date ngoài [kMinValidUtc, kMaxValidUtc): bỏ, neo cũ giữ nguyên
};

class TimeTrust {
public:
    // dateUtc: header Date của response 200 qua HTTPS. monoAtResp: monoSec() lúc nhận response.
    // rtcUtc: giờ DS3231 vừa đọc (bỏ qua khi !rtcOk). rtcOk: g_clockValid và đọc DS3231 thành
    // công. monoNow: monoSec() lúc đọc DS3231. dateNow = dateUtc + (monoNow − monoAtResp).
    // Write ⇒ writeUtc = dateNow. Write và NoChange đều đặt neo (dateUtc, monoAtResp);
    // Reject không đổi writeUtc lẫn neo.
    DateVerdict onServerDate(uint32_t dateUtc, uint32_t monoAtResp, uint32_t rtcUtc, bool rtcOk,
                             uint32_t monoNow, uint32_t& writeUtc);

    // ntpUtc: giờ NTP vừa nhận. rtcUtc/rtcOk như trên. mono: monoSec() lúc nhận NTP.
    // Có neo còn hạn ⇒ Accept khi |ntp − giờ chiếu từ neo| ≤ kTrustWindowS (RTC không xét).
    // Không có neo ⇒ Accept khi !rtcOk (bootstrap) hoặc |ntp − rtc| ≤ kTrustWindowS.
    // Không đổi trạng thái: NTP không bao giờ tạo hay dời neo.
    NtpVerdict onNtp(uint32_t ntpUtc, uint32_t rtcUtc, bool rtcOk, uint32_t mono) const;

    // Giờ chiếu từ neo tới mono. false nếu chưa có neo hoặc neo đã ≥ kAnchorMaxAgeS tuổi.
    bool anchorNow(uint32_t mono, uint32_t& utc) const;

    bool hasAnchor() const { return anchored_; }

private:
    bool anchored_ = false;
    uint32_t anchorUtc_ = 0;   // header Date
    uint32_t anchorMono_ = 0;  // monoSec() lúc nhận response mang Date đó
};
