#pragma once
#include <stdint.h>

// Quyết định có tin giờ NTP hay không. NTP không xác thực: ai giả được gói NTP trong LAN là
// xê dịch được đồng hồ (bơm tưới sai giờ). Logic thuần, không Arduino — xem docs/system-design.md
// mục "Đồng bộ thời gian".

constexpr uint32_t kMinValidUtc = 1767225600;  // 2026-01-01T00:00:00Z — nhỏ hơn ⇒ vô lý
constexpr uint32_t kMaxValidUtc = 4102444800;  // 2100-01-01T00:00:00Z — từ đây trở lên ⇒ vô lý
constexpr uint32_t kMaxClockSkewS = 300;       // lệch ≤ 300 s coi là trôi bình thường

enum class NtpVerdict : uint8_t {
    Accept,   // ghi DS3231 ngay
    Pending,  // lệch lớn: giữ làm ứng viên, chờ header Date qua HTTPS xác nhận
    Reject,   // ngoài [kMinValidUtc, kMaxValidUtc): bỏ, không đụng tới ứng viên đang chờ
};

enum class DateVerdict : uint8_t {
    NoCandidate,  // không có ứng viên nào đang chờ
    Confirm,      // header Date khớp ứng viên ⇒ ghi DS3231
    Drop,         // không khớp ⇒ bỏ ứng viên
};

class TimeTrust {
public:
    // ntpUtc: giờ NTP vừa nhận. rtcUtc: giờ DS3231 đọc được (bỏ qua khi !rtcValid).
    // rtcValid: g_clockValid và đọc DS3231 thành công. mono: monoSec() lúc nhận NTP.
    // Accept (đồng hồ chưa hợp lệ, hoặc lệch ≤ kMaxClockSkewS) xoá ứng viên đang chờ;
    // Pending thay ứng viên cũ bằng giá trị mới; Reject giữ nguyên.
    NtpVerdict onNtp(uint32_t ntpUtc, uint32_t rtcUtc, bool rtcValid, uint32_t mono);

    // dateUtc: header Date của response 200 qua HTTPS (đã xác thực bởi TLS).
    // mono: monoSec() lúc nhận response. Confirm ⇒ utcNow = ứng viên cộng thời gian monotonic
    // đã trôi tới mono (giá trị để ghi DS3231). Confirm và Drop đều xoá ứng viên.
    DateVerdict onServerDate(uint32_t dateUtc, uint32_t mono, uint32_t& utcNow);

    bool hasPending() const { return pending_; }

private:
    bool pending_ = false;
    uint32_t candUtc_ = 0;   // giờ NTP ứng viên
    uint32_t candMono_ = 0;  // monoSec() lúc nhận ứng viên
};
