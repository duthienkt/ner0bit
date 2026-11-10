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

void test_rest_survives_backward_clock_jump() {
    Scheduler s;
    s.setSchedule(sch("25200:300,25380:1800"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_TRUE(r.lastStopChanged);
    r = s.tick(T(D, 7, 3, 1), 301);  // NTP chỉnh lùi
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 3, 0), 0, Source::Normal, kSkippedRest);
}

void test_persisted_future_lastStop_blocks() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D, 7, 0, 30));
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), 0, Source::Normal, kSkippedRest);
}

void test_rest_boundary_599_600() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D, 6, 50, 1));  // 599s
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), 0, Source::Normal, kSkippedRest);
    Scheduler s2;
    s2.setSchedule(sch("25200:300"), D);
    s2.setFetchOk(true);
    s2.setLastStop(T(D, 6, 50, 0));  // 600s
    r = s2.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
}

void test_resume_across_midnight() {
    Scheduler s;
    s.setSchedule(sch("86100:600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 1, 0, 2, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 1, 0, 4, 59), 179);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 1, 0, 5, 0), 180);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D + 1, 0, 2, 0), T(D + 1, 0, 5, 0), Source::Normal, 0);
}

void test_earliest_open_window_wins() {
    Scheduler s;
    s.setSchedule(sch("25500:300,25200:1800"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 6, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 29, 59), 1439);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 30, 0), 1440);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 6, 0), T(D, 7, 30, 0), Source::Normal, 0);
}

void test_midnight_catchup_uses_yesterday_line2() {
    // Hôm qua (D) là fetchDay ⇒ mốc 23:55 của hôm qua lấy từ dòng 2.
    Scheduler s;
    s.setSchedule(sch("25200\n86100:600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 1, 0, 2, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D + 1, 0, 4, 59), 179);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 1, 0, 5, 0), 180);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D + 1, 0, 2, 0), T(D + 1, 0, 5, 0), Source::Today, 0);
}

void test_midnight_catchup_yesterday_empty_line2() {
    // Dòng 2 rỗng cho hôm qua (fetchDay D) ⇒ hôm qua không tưới, không bù qua 0h.
    Scheduler s;
    s.setSchedule(sch("86100:600\n"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 1, 0, 2, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_midnight_catchup_yesterday_line1_when_fetched_today() {
    // fetchDay = D+1 ⇒ dòng 2 rỗng chỉ áp dụng hôm nay; hôm qua dùng dòng 1.
    Scheduler s;
    s.setSchedule(sch("86100:600\n"), D + 1);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 1, 0, 2, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 1, 0, 5, 0), 180);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D + 1, 0, 2, 0), T(D + 1, 0, 5, 0), Source::Normal, 0);
}

void test_rest_uses_monotonic_on_forward_clock_jump() {
    Scheduler s;
    s.setSchedule(sch("25200:300,26400:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
    r = s.tick(T(D, 7, 20, 30), 301);  // đồng hồ nhảy tới; thực tế mới nghỉ 1s
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 20, 0), 0, Source::Normal, kSkippedRest);
}

void test_entry_due_while_busy_is_skipped() {
    Scheduler s;
    s.setSchedule(sch("25200:600,25500:120"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 4, 59), 299);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_TRUE(r.pumpOn);
    expectEvent(r, T(D, 7, 5, 0), 0, Source::Normal, kSkippedBusy);
    TEST_ASSERT_FALSE(r.lastStopChanged);
    for (uint32_t sec = 301; sec < 600; sec++) {
        r = s.tick(T(D, 7, 0, 0) + sec, sec);
        TEST_ASSERT_TRUE(r.pumpOn);
        TEST_ASSERT_FALSE(r.hasEvent);
    }
    r = s.tick(T(D, 7, 10, 0), 600);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 10, 0), Source::Normal, 0);
    r = s.tick(T(D, 7, 10, 1), 601);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_busy_skips_one_event_per_tick_in_order() {
    // Hai mốc cùng tới giờ khi đang chạy ⇒ hai tick liên tiếp, mốc sớm trước.
    Scheduler s;
    s.setSchedule(sch("25200:900,25560:60,25500:60"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 6, 0), 360);  // mốc 07:05 và 07:06 đều đã tới
    TEST_ASSERT_TRUE(r.pumpOn);
    expectEvent(r, T(D, 7, 5, 0), 0, Source::Normal, kSkippedBusy);
    r = s.tick(T(D, 7, 6, 1), 361);
    TEST_ASSERT_TRUE(r.pumpOn);
    expectEvent(r, T(D, 7, 6, 0), 0, Source::Normal, kSkippedBusy);
    r = s.tick(T(D, 7, 6, 2), 362);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_stop_event_has_priority_over_busy() {
    // Mốc 2 tới giờ đúng tick phiên 1 kết thúc ⇒ tick đó phát STOP; tick sau mốc 2 bị skipped rest.
    Scheduler s;
    s.setSchedule(sch("25200:300,25500:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
    r = s.tick(T(D, 7, 5, 1), 301);
    expectEvent(r, T(D, 7, 5, 0), 0, Source::Normal, kSkippedRest);
}

void test_large_backward_clock_jump_unblocks_entries() {
    // RTC chạy trước 3 ngày, NTP chỉnh về đúng ⇒ mốc hôm nay vẫn chạy.
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 3, 7, 0, 0), 0);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 3, 7, 5, 0), 300);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_TRUE(r.hasEvent);
    r = s.tick(T(D, 6, 59, 0), 1000);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 0, 0), 1060);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 5, 0), 1360);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, T(D, 7, 0, 0), T(D, 7, 5, 0), Source::Normal, 0);
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
    RUN_TEST(test_rest_survives_backward_clock_jump);
    RUN_TEST(test_persisted_future_lastStop_blocks);
    RUN_TEST(test_rest_boundary_599_600);
    RUN_TEST(test_resume_across_midnight);
    RUN_TEST(test_earliest_open_window_wins);
    RUN_TEST(test_midnight_catchup_uses_yesterday_line2);
    RUN_TEST(test_midnight_catchup_yesterday_empty_line2);
    RUN_TEST(test_midnight_catchup_yesterday_line1_when_fetched_today);
    RUN_TEST(test_rest_uses_monotonic_on_forward_clock_jump);
    RUN_TEST(test_entry_due_while_busy_is_skipped);
    RUN_TEST(test_busy_skips_one_event_per_tick_in_order);
    RUN_TEST(test_stop_event_has_priority_over_busy);
    RUN_TEST(test_large_backward_clock_jump_unblocks_entries);
    return UNITY_END();
}
