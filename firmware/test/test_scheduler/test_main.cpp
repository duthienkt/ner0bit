#include <unity.h>
#include "scheduler.h"

void setUp() {}
void tearDown() {}

static const uint32_t D = 20000;  // ngày bất kỳ

static uint32_t T(uint32_t day, uint32_t h, uint32_t m, uint32_t s) {
    return day * 86400 + h * 3600 + m * 60 + s;
}

static Schedule sch(const char* body) {
    Schedule s;
    TEST_ASSERT_TRUE(parseSchedule(body, s));
    return s;
}

static void expectEvent(const TickResult& r, uint32_t start, uint32_t stop, Source src, uint8_t flags) {
    TEST_ASSERT_TRUE(r.hasEvent);
    TEST_ASSERT_EQUAL_UINT32(start, r.event.start);
    TEST_ASSERT_EQUAL_UINT32(stop, r.event.stop);
    TEST_ASSERT_TRUE(src == r.event.source);
    TEST_ASSERT_EQUAL_UINT8(flags, r.event.flags);
}

void test_starts_at_entry_and_stops_after_duration() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 100);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    TEST_ASSERT_FALSE(r.lastStopChanged);
    r = s.tick(T(D, 7, 4, 59), 399);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 400);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
    TEST_ASSERT_TRUE(r.lastStopChanged);
    TEST_ASSERT_EQUAL_UINT32(T(D, 7, 5, 0), r.lastStop);
}

void test_today_line_replaces_line1() {
    Scheduler s;
    s.setSchedule(sch("25200\n28800"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 8, 0, 0), 3600);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 8, 5, 0), 3900);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 8, 0, 0), T(D, 8, 5, 0), Source::Today, 0);
}

void test_empty_line2_no_watering() {
    Scheduler s;
    s.setSchedule(sch("25200\n"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D + 1, 7, 0, 0), 86400);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 1, 7, 5, 0), 86700);
    expectEvent(r, T(D + 1, 7, 0, 0), T(D + 1, 7, 5, 0), Source::Normal, 0);
}

void test_line2_from_yesterday_ignored() {
    Scheduler s;
    s.setSchedule(sch("25200\n28800"), D - 1);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
    r = s.tick(T(D, 8, 0, 0), 3600);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_failsafe_source() {
    Scheduler s;
    s.setSchedule(sch("25200"), D);  // setFetchOk mặc định false
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Failsafe, 0);
}

void test_hard_cap() {
    Scheduler s;
    s.setSchedule(sch("25200:3600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 29, 59), 1799);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 30, 0), 1800);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 30, 0), Source::Normal, kCapped);
    TEST_ASSERT_EQUAL_UINT8(kCapped, r.event.flags);
}

void test_rest_skips_entry() {
    Scheduler s;
    s.setSchedule(sch("25200:300,25500"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    // R1: tick 07:05:00 phát event STOP của phiên 1 (mỗi tick tối đa 1 event)
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
    TEST_ASSERT_TRUE(r.lastStopChanged);
    // tick kế tiếp trong khung mốc 2: skipped
    r = s.tick(T(D, 7, 5, 1), 301);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 5, 0), 0, Source::Normal, kSkippedRest);
    TEST_ASSERT_FALSE(r.lastStopChanged);
}

void test_resume_mid_window_after_boot() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D - 1, 7, 5, 0));
    TickResult r = s.tick(T(D, 7, 3, 0), 10);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 4, 59), 129);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 130);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 3, 0), T(D, 7, 5, 0), Source::Normal, 0);
}

void test_no_catchup_after_window() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 6, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_running_session_survives_new_schedule() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 1, 0), 60);
    TEST_ASSERT_TRUE(r.pumpOn);
    s.setSchedule(sch("3600"), D);
    r = s.tick(T(D, 7, 4, 0), 240);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
}

void test_backward_clock_jump_still_stops_by_monotonic() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 1000);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 6, 0, 0), 1299);  // NTP chỉnh lùi 1 giờ
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 6, 0, 0), 1300);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 6, 0, 0), Source::Normal, 0);
    TEST_ASSERT_TRUE(r.lastStopChanged);
    TEST_ASSERT_EQUAL_UINT32(T(D, 6, 0, 0), r.lastStop);
}

void test_skipped_entry_not_reported_twice() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D, 6, 58, 0));  // nghỉ chưa đủ 600s
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), 0, Source::Normal, kSkippedRest);
    for (uint32_t sec = 1; sec < 300; sec++) {
        r = s.tick(T(D, 7, 0, 0) + sec, sec);
        TEST_ASSERT_FALSE(r.pumpOn);
        TEST_ASSERT_FALSE(r.hasEvent);
    }
}

void test_no_schedule_does_nothing() {
    Scheduler s;
    s.setFetchOk(true);
    TEST_ASSERT_FALSE(s.hasSchedule());
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    TEST_ASSERT_FALSE(r.lastStopChanged);
}

int main() {
    UNITY_BEGIN();
    RUN_TEST(test_starts_at_entry_and_stops_after_duration);
    RUN_TEST(test_today_line_replaces_line1);
    RUN_TEST(test_empty_line2_no_watering);
    RUN_TEST(test_line2_from_yesterday_ignored);
    RUN_TEST(test_failsafe_source);
    RUN_TEST(test_hard_cap);
    RUN_TEST(test_rest_skips_entry);
    RUN_TEST(test_resume_mid_window_after_boot);
    RUN_TEST(test_no_catchup_after_window);
    RUN_TEST(test_running_session_survives_new_schedule);
    RUN_TEST(test_backward_clock_jump_still_stops_by_monotonic);
    RUN_TEST(test_skipped_entry_not_reported_twice);
    RUN_TEST(test_no_schedule_does_nothing);
    return UNITY_END();
}
