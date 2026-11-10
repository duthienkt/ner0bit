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

static void expectEvent(const TickResult& r, EventType type, uint32_t at, Source src, Reason reason) {
    TEST_ASSERT_TRUE(r.hasEvent);
    TEST_ASSERT_EQUAL_UINT8((uint8_t)type, (uint8_t)r.event.type);
    TEST_ASSERT_EQUAL_UINT32(at, r.event.at);
    TEST_ASSERT_EQUAL_UINT8((uint8_t)src, (uint8_t)r.event.source);
    TEST_ASSERT_EQUAL_UINT8((uint8_t)reason, (uint8_t)r.event.reason);
}

static void expectStart(const TickResult& r, uint32_t at, Source src) {
    TEST_ASSERT_TRUE(r.pumpOn);
    expectEvent(r, EventType::Start, at, src, Reason::None);
    TEST_ASSERT_FALSE(r.lastStopChanged);
}

static void expectStop(const TickResult& r, uint32_t at, Source src, Reason reason) {
    TEST_ASSERT_FALSE(r.pumpOn);
    expectEvent(r, EventType::Stop, at, src, reason);
    TEST_ASSERT_TRUE(r.lastStopChanged);
    TEST_ASSERT_EQUAL_UINT32(at, r.lastStop);
}

static void expectSkipped(const TickResult& r, uint32_t at, Source src, Reason reason) {
    expectEvent(r, EventType::Skipped, at, src, reason);
    TEST_ASSERT_FALSE(r.lastStopChanged);
}

void test_starts_at_entry_and_stops_after_duration() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 100);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 0, 1), 101);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 4, 59), 399);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 400);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
}

void test_today_line_replaces_line1() {
    Scheduler s;
    s.setSchedule(sch("25200\n28800"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 8, 0, 0), 3600);
    expectStart(r, T(D, 8, 0, 0), Source::Today);
    r = s.tick(T(D, 8, 5, 0), 3900);
    expectStop(r, T(D, 8, 5, 0), Source::Today, Reason::Done);
}

void test_empty_line2_no_watering() {
    Scheduler s;
    s.setSchedule(sch("25200\n"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D + 1, 7, 0, 0), 86400);
    expectStart(r, T(D + 1, 7, 0, 0), Source::Normal);
    r = s.tick(T(D + 1, 7, 5, 0), 86700);
    expectStop(r, T(D + 1, 7, 5, 0), Source::Normal, Reason::Done);
}

void test_line2_from_yesterday_ignored() {
    Scheduler s;
    s.setSchedule(sch("25200\n28800"), D - 1);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
    r = s.tick(T(D, 8, 0, 0), 3600);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_failsafe_source() {
    Scheduler s;
    s.setSchedule(sch("25200"), D);  // setFetchOk mặc định false
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Failsafe);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Failsafe, Reason::Done);
}

// Start và stop của một phiên cùng source (source chốt lúc bật), dù fetch đổi trạng thái giữa phiên.
void test_session_source_fixed_at_start() {
    Scheduler s;
    s.setSchedule(sch("25200"), D);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Failsafe);
    s.setFetchOk(true);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Failsafe, Reason::Done);
}

void test_hard_cap() {
    Scheduler s;
    s.setSchedule(sch("25200:3600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 29, 59), 1799);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 30, 0), 1800);
    expectStop(r, T(D, 7, 30, 0), Source::Normal, Reason::Capped);
}

// Thời lượng đúng bằng hard cap ⇒ không bị cắt ⇒ "done".
void test_duration_equal_to_cap_is_done() {
    Scheduler s;
    s.setSchedule(sch("25200:1800"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 30, 0), 1800);
    expectStop(r, T(D, 7, 30, 0), Source::Normal, Reason::Done);
}

// Phiên capped bị dừng theo monotonic (đồng hồ lùi) vẫn là "capped".
void test_capped_stop_by_monotonic() {
    Scheduler s;
    s.setSchedule(sch("25200:3600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 6, 0, 0), 1800);
    expectStop(r, T(D, 6, 0, 0), Source::Normal, Reason::Capped);
}

void test_rest_skips_entry() {
    Scheduler s;
    s.setSchedule(sch("25200:300,25500"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    // R1: tick 07:05:00 phát event STOP của phiên 1 (mỗi tick tối đa 1 event)
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
    // tick kế tiếp trong khung mốc 2: skipped
    r = s.tick(T(D, 7, 5, 1), 301);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 5, 0), Source::Normal, Reason::Rest);
}

// Tưới bù sau boot giữa khung mốc: event start mang giờ bật thực tế, không phải mốc.
void test_resume_mid_window_after_boot() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D - 1, 7, 5, 0));
    TickResult r = s.tick(T(D, 7, 3, 0), 10);
    expectStart(r, T(D, 7, 3, 0), Source::Normal);
    r = s.tick(T(D, 7, 4, 59), 129);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 130);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
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
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 1, 0), 60);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    s.setSchedule(sch("3600"), D);
    r = s.tick(T(D, 7, 4, 0), 240);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
}

void test_backward_clock_jump_still_stops_by_monotonic() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 1000);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 6, 0, 0), 1299);  // NTP chỉnh lùi 1 giờ
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 6, 0, 0), 1300);
    expectStop(r, T(D, 6, 0, 0), Source::Normal, Reason::Done);
}

void test_skipped_entry_not_reported_twice() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D, 6, 58, 0));  // nghỉ chưa đủ 180s
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 0, 0), Source::Normal, Reason::Rest);
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
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
    r = s.tick(T(D, 7, 3, 1), 301);  // NTP chỉnh lùi
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 3, 0), Source::Normal, Reason::Rest);
}

void test_persisted_future_lastStop_blocks() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D, 7, 0, 30));
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 0, 0), Source::Normal, Reason::Rest);
}

void test_rest_boundary_179_180() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    s.setLastStop(T(D, 6, 57, 1));  // 179s
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 0, 0), Source::Normal, Reason::Rest);
    Scheduler s2;
    s2.setSchedule(sch("25200:300"), D);
    s2.setFetchOk(true);
    s2.setLastStop(T(D, 6, 57, 0));  // 180s
    r = s2.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
}

void test_resume_across_midnight() {
    Scheduler s;
    s.setSchedule(sch("86100:600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 1, 0, 2, 0), 0);
    expectStart(r, T(D + 1, 0, 2, 0), Source::Normal);
    r = s.tick(T(D + 1, 0, 4, 59), 179);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D + 1, 0, 5, 0), 180);
    expectStop(r, T(D + 1, 0, 5, 0), Source::Normal, Reason::Done);
}

void test_earliest_open_window_wins() {
    Scheduler s;
    s.setSchedule(sch("25500:300,25200:1800"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 6, 0), 0);
    expectStart(r, T(D, 7, 6, 0), Source::Normal);
    r = s.tick(T(D, 7, 29, 59), 1439);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D, 7, 30, 0), 1440);
    expectStop(r, T(D, 7, 30, 0), Source::Normal, Reason::Done);
}

void test_midnight_catchup_uses_yesterday_line2() {
    // Hôm qua (D) là fetchDay ⇒ mốc 23:55 của hôm qua lấy từ dòng 2.
    Scheduler s;
    s.setSchedule(sch("25200\n86100:600"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 1, 0, 2, 0), 0);
    expectStart(r, T(D + 1, 0, 2, 0), Source::Today);
    r = s.tick(T(D + 1, 0, 4, 59), 179);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.tick(T(D + 1, 0, 5, 0), 180);
    expectStop(r, T(D + 1, 0, 5, 0), Source::Today, Reason::Done);
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
    expectStart(r, T(D + 1, 0, 2, 0), Source::Normal);
    r = s.tick(T(D + 1, 0, 5, 0), 180);
    expectStop(r, T(D + 1, 0, 5, 0), Source::Normal, Reason::Done);
}

void test_rest_uses_monotonic_on_forward_clock_jump() {
    Scheduler s;
    s.setSchedule(sch("25200:300,26400:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
    r = s.tick(T(D, 7, 20, 30), 301);  // đồng hồ nhảy tới; thực tế mới nghỉ 1s
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 20, 0), Source::Normal, Reason::Rest);
}

void test_entry_due_while_busy_is_skipped() {
    Scheduler s;
    s.setSchedule(sch("25200:600,25500:120"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 4, 59), 299);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 5, 0), 300);
    TEST_ASSERT_TRUE(r.pumpOn);
    expectSkipped(r, T(D, 7, 5, 0), Source::Normal, Reason::Busy);
    for (uint32_t sec = 301; sec < 600; sec++) {
        r = s.tick(T(D, 7, 0, 0) + sec, sec);
        TEST_ASSERT_TRUE(r.pumpOn);
        TEST_ASSERT_FALSE(r.hasEvent);
    }
    r = s.tick(T(D, 7, 10, 0), 600);
    expectStop(r, T(D, 7, 10, 0), Source::Normal, Reason::Done);
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
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 6, 0), 360);  // mốc 07:05 và 07:06 đều đã tới
    TEST_ASSERT_TRUE(r.pumpOn);
    expectSkipped(r, T(D, 7, 5, 0), Source::Normal, Reason::Busy);
    r = s.tick(T(D, 7, 6, 1), 361);
    TEST_ASSERT_TRUE(r.pumpOn);
    expectSkipped(r, T(D, 7, 6, 0), Source::Normal, Reason::Busy);
    r = s.tick(T(D, 7, 6, 2), 362);
    TEST_ASSERT_TRUE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

// Bật bơm ở tick có thêm mốc khác cũng tới giờ ⇒ tick đó chỉ phát START; mốc kia bị busy ở tick sau.
void test_start_tick_emits_only_start() {
    Scheduler s;
    s.setSchedule(sch("25200:600,25260:60"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 1, 0), 0);  // cả 07:00 và 07:01 đều đã tới
    expectStart(r, T(D, 7, 1, 0), Source::Normal);
    r = s.tick(T(D, 7, 1, 1), 1);
    TEST_ASSERT_TRUE(r.pumpOn);
    expectSkipped(r, T(D, 7, 1, 0), Source::Normal, Reason::Busy);
}

void test_stop_event_has_priority_over_busy() {
    // Mốc 2 tới giờ đúng tick phiên 1 kết thúc ⇒ tick đó phát STOP; tick sau mốc 2 bị skipped rest.
    Scheduler s;
    s.setSchedule(sch("25200:300,25500:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 0);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 5, 0), 300);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
    r = s.tick(T(D, 7, 5, 1), 301);
    expectSkipped(r, T(D, 7, 5, 0), Source::Normal, Reason::Rest);
}

void test_large_backward_clock_jump_unblocks_entries() {
    // RTC chạy trước 3 ngày, NTP chỉnh về đúng ⇒ mốc hôm nay vẫn chạy.
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D + 3, 7, 0, 0), 0);
    expectStart(r, T(D + 3, 7, 0, 0), Source::Normal);
    r = s.tick(T(D + 3, 7, 5, 0), 300);
    expectStop(r, T(D + 3, 7, 5, 0), Source::Normal, Reason::Done);
    r = s.tick(T(D, 6, 59, 0), 1000);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 0, 0), 1060);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 5, 0), 1360);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
}

// I1: đọc đồng hồ lỗi giữa phiên ⇒ abort kết thúc phiên (stop tính theo monotonic), không chạy lại.
void test_abort_while_running_ends_session() {
    Scheduler s;
    s.setSchedule(sch("25200:900"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 100);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 1, 0), 160);
    TEST_ASSERT_TRUE(r.pumpOn);
    r = s.abort(220);
    expectStop(r, T(D, 7, 2, 0), Source::Normal, Reason::Abort);
    // Đồng hồ đọc lại được, vẫn trong khung mốc ⇒ bơm không bật lại.
    r = s.tick(T(D, 7, 2, 1), 221);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 14, 59), 999);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    r = s.tick(T(D, 7, 15, 0), 1000);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
}

void test_abort_applies_rest_to_next_entry() {
    Scheduler s;
    s.setSchedule(sch("25200:900,25440,25560"), D);  // 07:00, 07:04, 07:06
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 100);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.abort(220);  // dừng lúc 07:02
    expectStop(r, T(D, 7, 2, 0), Source::Normal, Reason::Abort);
    r = s.tick(T(D, 7, 4, 0), 340);  // mới nghỉ 120 s
    TEST_ASSERT_FALSE(r.pumpOn);
    expectSkipped(r, T(D, 7, 4, 0), Source::Normal, Reason::Rest);
    r = s.tick(T(D, 7, 6, 0), 460);  // nghỉ 240 s
    expectStart(r, T(D, 7, 6, 0), Source::Normal);
}

void test_abort_stop_clamped_to_session_duration() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.tick(T(D, 7, 0, 0), 100);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.abort(5000);  // không có tick nào kết thúc phiên trước đó
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Abort);
}

// Abort phiên capped ⇒ reason "abort" (abort được ưu tiên hơn capped).
void test_abort_capped_session_reason_abort() {
    Scheduler s;
    s.setSchedule(sch("25200:3600"), D);
    s.setFetchOk(false);
    TickResult r = s.tick(T(D, 7, 0, 0), 100);
    expectStart(r, T(D, 7, 0, 0), Source::Failsafe);
    r = s.abort(5000);
    expectStop(r, T(D, 7, 30, 0), Source::Failsafe, Reason::Abort);
}

void test_abort_while_idle_is_noop() {
    Scheduler s;
    s.setSchedule(sch("25200:300"), D);
    s.setFetchOk(true);
    TickResult r = s.abort(50);
    TEST_ASSERT_FALSE(r.pumpOn);
    TEST_ASSERT_FALSE(r.hasEvent);
    TEST_ASSERT_FALSE(r.lastStopChanged);
    // Không đặt trạng thái nghỉ: mốc kế tiếp vẫn chạy.
    r = s.tick(T(D, 7, 0, 0), 100);
    expectStart(r, T(D, 7, 0, 0), Source::Normal);
    r = s.tick(T(D, 7, 5, 0), 400);
    expectStop(r, T(D, 7, 5, 0), Source::Normal, Reason::Done);
    // Phiên đã kết thúc bình thường ⇒ abort cũng không làm gì.
    r = s.abort(401);
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
    RUN_TEST(test_session_source_fixed_at_start);
    RUN_TEST(test_hard_cap);
    RUN_TEST(test_duration_equal_to_cap_is_done);
    RUN_TEST(test_capped_stop_by_monotonic);
    RUN_TEST(test_rest_skips_entry);
    RUN_TEST(test_resume_mid_window_after_boot);
    RUN_TEST(test_no_catchup_after_window);
    RUN_TEST(test_running_session_survives_new_schedule);
    RUN_TEST(test_backward_clock_jump_still_stops_by_monotonic);
    RUN_TEST(test_skipped_entry_not_reported_twice);
    RUN_TEST(test_no_schedule_does_nothing);
    RUN_TEST(test_rest_survives_backward_clock_jump);
    RUN_TEST(test_persisted_future_lastStop_blocks);
    RUN_TEST(test_rest_boundary_179_180);
    RUN_TEST(test_resume_across_midnight);
    RUN_TEST(test_earliest_open_window_wins);
    RUN_TEST(test_midnight_catchup_uses_yesterday_line2);
    RUN_TEST(test_midnight_catchup_yesterday_empty_line2);
    RUN_TEST(test_midnight_catchup_yesterday_line1_when_fetched_today);
    RUN_TEST(test_rest_uses_monotonic_on_forward_clock_jump);
    RUN_TEST(test_entry_due_while_busy_is_skipped);
    RUN_TEST(test_busy_skips_one_event_per_tick_in_order);
    RUN_TEST(test_start_tick_emits_only_start);
    RUN_TEST(test_stop_event_has_priority_over_busy);
    RUN_TEST(test_large_backward_clock_jump_unblocks_entries);
    RUN_TEST(test_abort_while_running_ends_session);
    RUN_TEST(test_abort_applies_rest_to_next_entry);
    RUN_TEST(test_abort_stop_clamped_to_session_duration);
    RUN_TEST(test_abort_capped_session_reason_abort);
    RUN_TEST(test_abort_while_idle_is_noop);
    return UNITY_END();
}
