#include <unity.h>
#include "time_trust.h"

void setUp() {}
void tearDown() {}

static const uint32_t kNow = 1794297900;  // 2026-11-10T08:05:00Z

// Quy tắc 1: chặn dưới (2026-01-01) và chặn trên (2100-01-01).
void test_rule1_bounds() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(0, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMinValidUtc - 1, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMaxValidUtc, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(0xFFFFFFFFu, 0, false, 10));
    // Cả khi đồng hồ đang hợp lệ và giờ NTP trùng RTC.
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMaxValidUtc, kMaxValidUtc, true, 10));
    TEST_ASSERT_FALSE(t.hasPending());
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kMinValidUtc, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kMaxValidUtc - 1, 0, false, 10));
}

// Reject không xoá ứng viên đang chờ.
void test_rule1_reject_keeps_pending() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow + 3600, kNow, true, 100));
    TEST_ASSERT_TRUE(NtpVerdict::Reject == t.onNtp(kMaxValidUtc + 5, kNow, true, 110));
    TEST_ASSERT_TRUE(t.hasPending());
}

// Quy tắc 2: đồng hồ chưa hợp lệ ⇒ nhận NTP, dù lệch bao nhiêu.
void test_rule2_invalid_clock_accepts() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, 0, false, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, kNow - 86400 * 365, false, 10));
    TEST_ASSERT_FALSE(t.hasPending());
}

// Quy tắc 2: nhận NTP khi đồng hồ chưa hợp lệ thì xoá ứng viên cũ.
void test_rule2_accept_clears_pending() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow + 3600, kNow, true, 100));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, 0, false, 200));
    TEST_ASSERT_FALSE(t.hasPending());
    uint32_t u = 0;
    TEST_ASSERT_TRUE(DateVerdict::NoCandidate == t.onServerDate(kNow + 3600, 300, u));
}

// Quy tắc 3: đồng hồ hợp lệ, lệch ≤ 300 s ⇒ nhận (trôi bình thường), hai chiều.
void test_rule3_small_drift_accepts() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow + 300, kNow, true, 10));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow - 300, kNow, true, 10));
    TEST_ASSERT_FALSE(t.hasPending());
}

// Quy tắc 3: lệch nhỏ (NTP khớp RTC) ⇒ ứng viên lệch lớn trước đó bị bỏ.
void test_rule3_accept_clears_pending() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow + 3600, kNow, true, 100));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow + 10, kNow, true, 110));
    TEST_ASSERT_FALSE(t.hasPending());
}

// Quy tắc 4: lệch > 300 s ⇒ chờ xác nhận, hai chiều.
void test_rule4_large_skew_pending() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow + 301, kNow, true, 10));
    TEST_ASSERT_TRUE(t.hasPending());
    TimeTrust t2;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t2.onNtp(kNow - 301, kNow, true, 10));
    TEST_ASSERT_TRUE(t2.hasPending());
    TimeTrust t3;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t3.onNtp(kMinValidUtc, kNow, true, 10));
}

// Quy tắc 4: header Date khớp ứng viên (đã cộng thời gian monotonic trôi) ⇒ Confirm.
void test_rule4_confirm_with_elapsed() {
    TimeTrust t;
    const uint32_t cand = kNow + 7200;  // RTC chậm 2 giờ thật
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(cand, kNow, true, 1000));
    uint32_t u = 0;
    // 400 s sau mới có heartbeat 200; Date = cand + 400 (+ chút trễ mạng).
    TEST_ASSERT_TRUE(DateVerdict::Confirm == t.onServerDate(cand + 402, 1400, u));
    TEST_ASSERT_EQUAL_UINT32(cand + 400, u);
    TEST_ASSERT_FALSE(t.hasPending());
    // Ứng viên đã dùng xong.
    TEST_ASSERT_TRUE(DateVerdict::NoCandidate == t.onServerDate(cand + 500, 1500, u));
}

// Biên ±300 s quanh ứng viên đã chiếu tới thời điểm hiện tại.
void test_rule4_confirm_window_edges() {
    uint32_t u = 0;
    const uint32_t cand = kNow - 3600;
    {
        TimeTrust t;
        t.onNtp(cand, kNow, true, 50);
        TEST_ASSERT_TRUE(DateVerdict::Confirm == t.onServerDate(cand + 1000 + 300, 1050, u));
        TEST_ASSERT_EQUAL_UINT32(cand + 1000, u);
    }
    {
        TimeTrust t;
        t.onNtp(cand, kNow, true, 50);
        TEST_ASSERT_TRUE(DateVerdict::Confirm == t.onServerDate(cand + 1000 - 300, 1050, u));
    }
    {
        TimeTrust t;
        t.onNtp(cand, kNow, true, 50);
        TEST_ASSERT_TRUE(DateVerdict::Drop == t.onServerDate(cand + 1000 + 301, 1050, u));
        TEST_ASSERT_FALSE(t.hasPending());
    }
    {
        TimeTrust t;
        t.onNtp(cand, kNow, true, 50);
        TEST_ASSERT_TRUE(DateVerdict::Drop == t.onServerDate(cand + 1000 - 301, 1050, u));
    }
}

// Quy tắc 4: NTP giả, server (qua TLS) vẫn báo đúng giờ RTC ⇒ Drop, không ghi DS3231.
void test_rule4_spoofed_ntp_dropped() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow + 6 * 3600, kNow, true, 100));
    uint32_t u = 777;
    TEST_ASSERT_TRUE(DateVerdict::Drop == t.onServerDate(kNow + 60, 160, u));
    TEST_ASSERT_FALSE(t.hasPending());
}

// Không có ứng viên ⇒ header Date không làm gì.
void test_no_candidate() {
    TimeTrust t;
    uint32_t u = 0;
    TEST_ASSERT_TRUE(DateVerdict::NoCandidate == t.onServerDate(kNow, 10, u));
    TEST_ASSERT_TRUE(NtpVerdict::Accept == t.onNtp(kNow, kNow, true, 10));
    TEST_ASSERT_TRUE(DateVerdict::NoCandidate == t.onServerDate(kNow, 10, u));
}

// Ứng viên mới thay ứng viên cũ (cả giờ lẫn mốc monotonic).
void test_new_pending_replaces_old() {
    TimeTrust t;
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow + 3600, kNow, true, 100));
    TEST_ASSERT_TRUE(NtpVerdict::Pending == t.onNtp(kNow - 3600, kNow, true, 200));
    uint32_t u = 0;
    TEST_ASSERT_TRUE(DateVerdict::Confirm == t.onServerDate(kNow - 3600 + 50, 250, u));
    TEST_ASSERT_EQUAL_UINT32(kNow - 3600 + 50, u);
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_rule1_bounds);
    RUN_TEST(test_rule1_reject_keeps_pending);
    RUN_TEST(test_rule2_invalid_clock_accepts);
    RUN_TEST(test_rule2_accept_clears_pending);
    RUN_TEST(test_rule3_small_drift_accepts);
    RUN_TEST(test_rule3_accept_clears_pending);
    RUN_TEST(test_rule4_large_skew_pending);
    RUN_TEST(test_rule4_confirm_with_elapsed);
    RUN_TEST(test_rule4_confirm_window_edges);
    RUN_TEST(test_rule4_spoofed_ntp_dropped);
    RUN_TEST(test_no_candidate);
    RUN_TEST(test_new_pending_replaces_old);
    return UNITY_END();
}
