#include <unity.h>

#include "time_trust.h"

void setUp() {}
void tearDown() {}

static const uint32_t kNow = 1794297900;  // 2026-11-10T08:05:00Z
static const uint32_t kDay = 86400;

// Đặt neo: header Date = dateUtc nhận ở mono, RTC khớp ⇒ NoChange.
static void anchorAt(TimeTrust& t, uint32_t dateUtc, uint32_t mono) {
    uint32_t w = 0;
    TEST_ASSERT_TRUE(DateVerdict::NoChange ==
                     t.onServerDate(dateUtc, mono, dateUtc, true, mono, w));
    TEST_ASSERT_TRUE(t.hasAnchor());
}

// ---------- Quy tắc 1: header Date là nguồn tin cậy ----------

// RTC lệch > 5 s ⇒ ghi DS3231 theo Date (cộng thời gian trôi từ lúc nhận response), hai chiều.
void test_date_corrects_skewed_rtc() {
    {
        TimeTrust t;
        uint32_t w = 0;
        // Response nhận ở mono 1000, xử lý xong ở mono 1003 ⇒ dateNow = kNow + 3.
        TEST_ASSERT_TRUE(DateVerdict::Write ==
                         t.onServerDate(kNow, 1000, kNow + 3 + 3600, true, 1003, w));
        TEST_ASSERT_EQUAL_UINT32(kNow + 3, w);
        TEST_ASSERT_TRUE(t.hasAnchor());
    }
    {
        TimeTrust t;
        uint32_t w = 0;
        TEST_ASSERT_TRUE(DateVerdict::Write == t.onServerDate(kNow, 1000, kNow - 6, true, 1000, w));
        TEST_ASSERT_EQUAL_UINT32(kNow, w);
    }
    {
        TimeTrust t;
        uint32_t w = 0;
        TEST_ASSERT_TRUE(DateVerdict::Write == t.onServerDate(kNow, 1000, kNow + 6, true, 1000, w));
        TEST_ASSERT_EQUAL_UINT32(kNow, w);
    }
}

// |RTC − dateNow| ≤ 5 s ⇒ NoChange (không ghi DS3231), nhưng vẫn đặt neo. Biên 5 s, hai chiều.
void test_date_within_5s_no_change() {
    const int32_t offs[] = {0, 5, -5, 3, -1};
    for (int32_t off : offs) {
        TimeTrust t;
        uint32_t w = 777;
        TEST_ASSERT_TRUE(
            DateVerdict::NoChange ==
            t.onServerDate(kNow, 1000, (uint32_t)((int64_t)kNow + 10 + off), true, 1010, w));
        TEST_ASSERT_EQUAL_UINT32(777, w);
        TEST_ASSERT_TRUE(t.hasAnchor());
    }
}

// RTC không hợp lệ hoặc đọc lỗi ⇒ luôn ghi theo Date (dù giá trị rtcUtc tình cờ khớp).
void test_date_invalid_rtc_writes() {
    TimeTrust t;
    uint32_t w = 0;
    TEST_ASSERT_TRUE(DateVerdict::Write == t.onServerDate(kNow, 1000, kNow, false, 1002, w));
    TEST_ASSERT_EQUAL_UINT32(kNow + 2, w);
    TimeTrust t2;
    TEST_ASSERT_TRUE(DateVerdict::Write == t2.onServerDate(kNow, 1000, 0, false, 1000, w));
    TEST_ASSERT_EQUAL_UINT32(kNow, w);
}

// Date ngoài [kMinValidUtc, kMaxValidUtc) ⇒ Reject: không ghi, không đặt neo, neo cũ giữ nguyên.
void test_date_bounds() {
    uint32_t w = 777;
    TimeTrust t;
    TEST_ASSERT_TRUE(DateVerdict::Reject == t.onServerDate(0, 1000, 0, false, 1000, w));
    TEST_ASSERT_TRUE(DateVerdict::Reject ==
                     t.onServerDate(kMinValidUtc - 1, 1000, 0, false, 1000, w));
    TEST_ASSERT_TRUE(DateVerdict::Reject ==
                     t.onServerDate(kMaxValidUtc, 1000, kNow, true, 1000, w));
    TEST_ASSERT_TRUE(DateVerdict::Reject == t.onServerDate(0xFFFFFFFFu, 1000, kNow, true, 1000, w));
    // Date hợp lệ nhưng cộng thời gian trôi thì vượt chặn trên ⇒ cũng Reject.
    TEST_ASSERT_TRUE(DateVerdict::Reject ==
                     t.onServerDate(kMaxValidUtc - 1, 1000, kNow, true, 1010, w));
    TEST_ASSERT_EQUAL_UINT32(777, w);
    TEST_ASSERT_FALSE(t.hasAnchor());
    // Biên trong khoảng: nhận.
    TEST_ASSERT_TRUE(DateVerdict::Write == t.onServerDate(kMinValidUtc, 1000, 0, false, 1000, w));
    TEST_ASSERT_EQUAL_UINT32(kMinValidUtc, w);
    TEST_ASSERT_TRUE(DateVerdict::Write ==
                     t.onServerDate(kMaxValidUtc - 1, 1000, 0, false, 1000, w));
    TEST_ASSERT_EQUAL_UINT32(kMaxValidUtc - 1, w);

    // Reject không xoá neo cũ.
    TimeTrust t2;
    anchorAt(t2, kNow, 1000);
    TEST_ASSERT_TRUE(DateVerdict::Reject == t2.onServerDate(0, 1100, kNow + 100, true, 1100, w));
    TEST_ASSERT_TRUE(t2.hasAnchor());
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t2.onNtp(kNow + 100, kNow + 100, true, 1100));
}

// Date mới thay neo cũ: NTP được so với neo mới nhất.
void test_date_replaces_anchor() {
    TimeTrust t;
    anchorAt(t, kNow, 1000);
    uint32_t w = 0;
    // Server báo giờ khác hẳn (vd. RTC và neo cũ sai) ⇒ ghi, neo mới.
    TEST_ASSERT_TRUE(DateVerdict::Write ==
                     t.onServerDate(kNow + 7200, 1300, kNow + 300, true, 1300, w));
    TEST_ASSERT_EQUAL_UINT32(kNow + 7200, w);
    TEST_ASSERT_TRUE(NtpVerdict::Accept ==
                     t.onNtp(kNow + 7200 + 100, kNow + 7200 + 100, true, 1400));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kNow + 400, kNow + 400, true, 1400));
}

// ---------- Quy tắc 2: có neo (< 24 h) ⇒ NTP phải nằm trong ±5 s của giờ chiếu từ neo ----------

void test_ntp_with_anchor_window() {
    TimeTrust t;
    anchorAt(t, kNow, 1000);
    const uint32_t mono = 1000 + 3600;
    const uint32_t expect = kNow + 3600;
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(expect, expect, true, mono));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(expect + 5, expect, true, mono));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(expect - 5, expect, true, mono));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(expect + 6, expect, true, mono));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(expect - 6, expect, true, mono));
    // Có neo thì neo quyết định, không phải RTC: NTP khớp RTC nhưng lệch neo ⇒ bỏ.
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(expect + 60, expect + 60, true, mono));
    // RTC không hợp lệ cũng không mở cửa cho NTP lệch neo.
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(expect + 60, 0, false, mono));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(expect + 2, 0, false, mono));
}

// Tấn công trôi từng bước: NTP giả mỗi lần +299 s (mỗi 6 h và mỗi lần reboot), có neo ⇒ bỏ hết.
void test_drift_attack_with_anchor_rejected() {
    TimeTrust t;
    uint32_t rtc = kNow;  // RTC chỉ đổi khi TimeTrust cho phép
    uint32_t mono = 1000;
    anchorAt(t, kNow, mono);
    for (uint32_t k = 1; k <= 40; ++k) {
        const uint32_t step = (k % 4 == 0) ? 60 : 6 * 3600;  // xen kẽ reboot (sync sớm)
        mono += step;
        rtc += step;
        const uint32_t truth = kNow + (mono - 1000);
        // Heartbeat 200 vẫn đều đặn (mỗi 300 s): neo luôn mới.
        uint32_t w = 0;
        TEST_ASSERT_TRUE(DateVerdict::NoChange ==
                         t.onServerDate(truth - 1, mono - 1, rtc, true, mono, w));
        // NTP giả: lệch +299 so với RTC hiện tại, và lệch luỹ tiến k·299 so với giờ thật.
        TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(rtc + 299, rtc, true, mono));
        TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(truth + 299 * k, rtc, true, mono));
        TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(rtc - 299, rtc, true, mono));
    }
    // Chỉ một neo, không heartbeat nào sau đó (server bị chặn) — trong 24 h vẫn bỏ hết.
    TimeTrust t2;
    anchorAt(t2, kNow, 1000);
    for (uint32_t k = 1; k <= 3; ++k) {
        const uint32_t m = 1000 + k * 6 * 3600;
        TEST_ASSERT_TRUE(NtpVerdict::Reject ==
                         t2.onNtp(kNow + k * 6 * 3600 + 299 * k, kNow + k * 6 * 3600, true, m));
    }
}

// Tấn công trôi từng bước khi chưa có neo, RTC hợp lệ ⇒ bỏ hết, RTC không xê dịch.
void test_drift_attack_no_anchor_rejected() {
    TimeTrust t;
    uint32_t rtc = kNow;
    uint32_t mono = 10;
    for (uint32_t k = 1; k <= 40; ++k) {
        mono += 6 * 3600;
        rtc += 6 * 3600;
        const uint32_t fake = rtc + 299;
        const NtpVerdict v = t.onNtp(fake, rtc, true, mono);
        TEST_ASSERT_TRUE(NtpVerdict::Reject == v);
        if (v == NtpVerdict::Accept) rtc = fake;
        TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(rtc - 299, rtc, true, mono));
        TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(rtc + 6, rtc, true, mono));
    }
    TEST_ASSERT_EQUAL_UINT32(kNow + 40 * 6 * 3600, rtc);
    TEST_ASSERT_FALSE(t.hasAnchor());
}

// Neo ≥ 24 h tuổi ⇒ bỏ qua neo, quay về quy tắc 3 (so với RTC).
void test_anchor_older_than_24h() {
    TimeTrust t;
    anchorAt(t, kNow, 1000);
    const uint32_t lastFresh = 1000 + kDay - 1;
    const uint32_t expired = 1000 + kDay;
    // Còn hạn: neo quyết định — NTP khớp RTC nhưng lệch neo 60 s ⇒ bỏ.
    TEST_ASSERT_TRUE(NtpVerdict::Reject ==
                     t.onNtp(kNow + kDay - 1 + 60, kNow + kDay - 1 + 60, true, lastFresh));
    TEST_ASSERT_TRUE(NtpVerdict::Accept ==
                     t.onNtp(kNow + kDay - 1, kNow + kDay - 1, true, lastFresh));
    // Hết hạn: so với RTC, ±5 s.
    TEST_ASSERT_TRUE(NtpVerdict::Accept ==
                     t.onNtp(kNow + kDay + 60, kNow + kDay + 60, true, expired));
    TEST_ASSERT_TRUE(NtpVerdict::Accept ==
                     t.onNtp(kNow + kDay + 65, kNow + kDay + 60, true, expired));
    TEST_ASSERT_TRUE(NtpVerdict::Reject ==
                     t.onNtp(kNow + kDay + 66, kNow + kDay + 60, true, expired));
    TEST_ASSERT_TRUE(NtpVerdict::Reject ==
                     t.onNtp(kNow + kDay + 299, kNow + kDay, true, expired + 3600));
    // Hết hạn + RTC không hợp lệ ⇒ bootstrap.
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow + 2 * kDay, 0, false, expired + kDay));
    // Date mới làm neo còn hạn trở lại.
    anchorAt(t, kNow + 2 * kDay, 1000 + 2 * kDay);
    TEST_ASSERT_TRUE(NtpVerdict::Reject ==
                     t.onNtp(kNow + 2 * kDay + 60, kNow + 2 * kDay + 60, true, 1000 + 2 * kDay));
}

// ---------- Quy tắc 3: chưa có neo ----------

// RTC không hợp lệ hoặc đọc lỗi ⇒ bootstrap: nhận NTP dù lệch bao nhiêu.
void test_bootstrap_invalid_rtc() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, kNow - 365 * kDay, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, kNow + 3600, false, 10));
    TEST_ASSERT_FALSE(t.hasAnchor());  // NTP không bao giờ tạo neo
}

// Chưa có neo, RTC hợp lệ: nhận khi |NTP − RTC| ≤ 5 s, bỏ khi lớn hơn, hai chiều.
void test_no_anchor_valid_rtc_window() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow + 5, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow - 5, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kNow + 6, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kNow - 6, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kNow + 7200, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMinValidUtc, kNow, true, 10));
}

// ---------- Quy tắc 4: chặn dưới (2026-01-01) và chặn trên (2100-01-01) cho NTP ----------

void test_ntp_bounds() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(0, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMinValidUtc - 1, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMaxValidUtc, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(0xFFFFFFFFu, 0, false, 10));
    // Cả khi NTP trùng RTC.
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMaxValidUtc, kMaxValidUtc, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMinValidUtc - 1, kMinValidUtc - 1, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kMinValidUtc, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kMaxValidUtc - 1, 0, false, 10));
    // Có neo cũng vậy.
    TimeTrust t2;
    anchorAt(t2, kMinValidUtc + 2, 1000);
    TEST_ASSERT_TRUE(NtpVerdict::Reject ==
                     t2.onNtp(kMinValidUtc - 1, kMinValidUtc + 2, true, 1000));
}

// anchorNow: giờ chiếu từ neo, chỉ khi neo còn hạn.
void test_anchor_now() {
    TimeTrust t;
    uint32_t u = 0;
    TEST_ASSERT_FALSE(t.anchorNow(10, u));
    anchorAt(t, kNow, 1000);
    TEST_ASSERT_TRUE(t.anchorNow(1500, u));
    TEST_ASSERT_EQUAL_UINT32(kNow + 500, u);
    TEST_ASSERT_FALSE(t.anchorNow(1000 + kDay, u));
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_date_corrects_skewed_rtc);
    RUN_TEST(test_date_within_5s_no_change);
    RUN_TEST(test_date_invalid_rtc_writes);
    RUN_TEST(test_date_bounds);
    RUN_TEST(test_date_replaces_anchor);
    RUN_TEST(test_ntp_with_anchor_window);
    RUN_TEST(test_drift_attack_with_anchor_rejected);
    RUN_TEST(test_drift_attack_no_anchor_rejected);
    RUN_TEST(test_anchor_older_than_24h);
    RUN_TEST(test_bootstrap_invalid_rtc);
    RUN_TEST(test_no_anchor_valid_rtc_window);
    RUN_TEST(test_ntp_bounds);
    RUN_TEST(test_anchor_now);
    return UNITY_END();
}
