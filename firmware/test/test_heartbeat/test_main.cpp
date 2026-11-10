#include <unity.h>
#include <stdio.h>
#include <string.h>
#include "civil_time.h"
#include "heartbeat.h"

void setUp() {}
void tearDown() {}

static Event ev(EventType type, uint32_t at, Source src, Reason reason) {
    Event e;
    e.at = at;
    e.type = type;
    e.source = src;
    e.reason = reason;
    return e;
}

// Info mặc định cho test: module đều ok, 31.25 °C, queue 3.
static HeartbeatInfo info(bool pumpOn, uint32_t uptime = 123456) {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", uptime, -58, pumpOn, 182344, 150212,
                       ModuleState::Ok, ModuleState::Ok, 125, 3, 1794323100};
    return h;
}

static const char* kPrefixOff =
    "{\"id\":\"a4cf12b8e901\",\"fw\":\"0.1.0\",\"uptime_s\":123456,\"rssi\":-58,"
    "\"pump_on\":false,\"heap_free\":182344,\"heap_min\":150212,\"ds3231\":\"ok\",\"at24c32\":\"ok\","
    "\"temp_c\":31.25,\"queue\":3,\"rtc\":\"2026-11-10T15:05:00+07:00\",\"events\":[";

static int countOccurrences(const char* hay, const char* needle) {
    int n = 0;
    size_t len = strlen(needle);
    for (const char* p = strstr(hay, needle); p; p = strstr(p + len, needle)) n++;
    return n;
}

// 2026-11-10 00:00:00 local
static const uint32_t kDay = 1794323100 - (15 * 3600 + 5 * 60);

void test_format_iso() {
    char buf[26];
    formatIsoLocal(1794323100, buf);
    TEST_ASSERT_EQUAL_STRING("2026-11-10T15:05:00+07:00", buf);

    // 2024-02-29 00:00:00 (ngày nhuận): days_from_civil = 19782
    formatIsoLocal(19782u * 86400u, buf);
    TEST_ASSERT_EQUAL_STRING("2024-02-29T00:00:00+07:00", buf);

    // 2026-12-31 23:59:59: days_from_civil(2026-12-31) = 20818
    formatIsoLocal(20818u * 86400u + 86399u, buf);
    TEST_ASSERT_EQUAL_STRING("2026-12-31T23:59:59+07:00", buf);

    formatIsoLocal(0, buf);
    TEST_ASSERT_EQUAL_STRING("1970-01-01T00:00:00+07:00", buf);
}

void test_civil_roundtrip() {
    TEST_ASSERT_EQUAL_INT32(0, daysFromCivil(1970, 1, 1));
    TEST_ASSERT_EQUAL_INT32(19782, daysFromCivil(2024, 2, 29));
    TEST_ASSERT_EQUAL_INT32(20818, daysFromCivil(2026, 12, 31));
    TEST_ASSERT_EQUAL_INT32(kDay / 86400, daysFromCivil(2026, 11, 10));
    // Mỗi ngày từ 1970 tới 2106 đi vòng civil -> days khớp nhau.
    for (int32_t d = 0; d < 49710; d++) {
        int32_t y;
        unsigned m, dd;
        civilFromDays(d, y, m, dd);
        TEST_ASSERT_EQUAL_INT32(d, daysFromCivil(y, m, dd));
    }
}

void test_build_no_events() {
    HeartbeatInfo h = info(false);
    char buf[512];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, nullptr, 0);
    const char* expected =
        "{\"id\":\"a4cf12b8e901\",\"fw\":\"0.1.0\",\"uptime_s\":123456,\"rssi\":-58,"
        "\"pump_on\":false,\"heap_free\":182344,\"heap_min\":150212,\"ds3231\":\"ok\",\"at24c32\":\"ok\","
        "\"temp_c\":31.25,\"queue\":3,\"rtc\":\"2026-11-10T15:05:00+07:00\",\"events\":[]}";
    TEST_ASSERT_EQUAL_STRING(expected, buf);
    TEST_ASSERT_EQUAL_UINT32(strlen(expected), n);
}

void test_temp_unknown_omitted() {
    HeartbeatInfo h = info(true, 7);
    h.ds3231 = ModuleState::Absent;
    h.at24c32 = ModuleState::Absent;
    h.tempQuarterC = kTempUnknown;
    h.queueLen = 0;
    char buf[512];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, nullptr, 0);
    const char* expected =
        "{\"id\":\"a4cf12b8e901\",\"fw\":\"0.1.0\",\"uptime_s\":7,\"rssi\":-58,"
        "\"pump_on\":true,\"heap_free\":182344,\"heap_min\":150212,\"ds3231\":\"absent\",\"at24c32\":\"absent\","
        "\"queue\":0,\"rtc\":\"2026-11-10T15:05:00+07:00\",\"events\":[]}";
    TEST_ASSERT_EQUAL_STRING(expected, buf);
    TEST_ASSERT_EQUAL_UINT32(strlen(expected), n);
}

void test_module_states() {
    HeartbeatInfo h = info(false);
    h.ds3231 = ModuleState::LostPower;
    h.at24c32 = ModuleState::Failed;
    char buf[512];
    TEST_ASSERT_TRUE(buildHeartbeat(buf, sizeof(buf), h, nullptr, 0) > 0);
    TEST_ASSERT_NOT_NULL(strstr(buf, "\"ds3231\":\"lost_power\",\"at24c32\":\"failed\","));
}

static void expectTemp(int16_t q, const char* expected) {
    HeartbeatInfo h = info(false);
    h.tempQuarterC = q;
    char buf[512];
    TEST_ASSERT_TRUE(buildHeartbeat(buf, sizeof(buf), h, nullptr, 0) > 0);
    char key[48];
    snprintf(key, sizeof key, "\"temp_c\":%s,\"queue\":", expected);
    TEST_ASSERT_NOT_NULL_MESSAGE(strstr(buf, key), expected);
}

void test_temp_format() {
    expectTemp(0, "0.00");
    expectTemp(1, "0.25");
    expectTemp(2, "0.50");
    expectTemp(3, "0.75");
    expectTemp(125, "31.25");
    expectTemp(-1, "-0.25");
    expectTemp(-15, "-3.75");
    expectTemp(-16, "-4.00");
    expectTemp(INT16_MAX, "8191.75");
    expectTemp(INT16_MIN + 1, "-8191.75");
}

void test_events_exact() {
    HeartbeatInfo h = info(false);
    Event evs[6] = {
        ev(EventType::Start, kDay + 7 * 3600, Source::Today, Reason::None),
        ev(EventType::Skipped, kDay + 7 * 3600 + 120, Source::Today, Reason::Busy),
        ev(EventType::Stop, kDay + 7 * 3600 + 300, Source::Today, Reason::Done),
        ev(EventType::Skipped, kDay + 7 * 3600 + 360, Source::Normal, Reason::Rest),
        ev(EventType::Stop, kDay + 11 * 3600 + 1800, Source::Failsafe, Reason::Capped),
        ev(EventType::Stop, kDay + 12 * 3600 + 61, Source::Normal, Reason::Abort),
    };
    char buf[1024];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, evs, 6);
    char expected[1024];
    snprintf(expected, sizeof expected, "%s%s", kPrefixOff,
             "{\"type\":\"start\",\"at\":\"2026-11-10T07:00:00+07:00\",\"source\":\"today\"},"
             "{\"type\":\"skipped\",\"at\":\"2026-11-10T07:02:00+07:00\",\"source\":\"today\",\"reason\":\"busy\"},"
             "{\"type\":\"stop\",\"at\":\"2026-11-10T07:05:00+07:00\",\"source\":\"today\",\"reason\":\"done\"},"
             "{\"type\":\"skipped\",\"at\":\"2026-11-10T07:06:00+07:00\",\"source\":\"normal\",\"reason\":\"rest\"},"
             "{\"type\":\"stop\",\"at\":\"2026-11-10T11:30:00+07:00\",\"source\":\"failsafe\",\"reason\":\"capped\"},"
             "{\"type\":\"stop\",\"at\":\"2026-11-10T12:01:01+07:00\",\"source\":\"normal\",\"reason\":\"abort\"}]}");
    TEST_ASSERT_EQUAL_STRING(expected, buf);
    TEST_ASSERT_EQUAL_UINT32(strlen(expected), n);
}

// Start không bao giờ có "reason" (kể cả khi record mang reason khác None).
void test_start_never_has_reason() {
    HeartbeatInfo h = info(false);
    Event e = ev(EventType::Start, kDay, Source::Normal, Reason::Capped);
    char buf[512];
    TEST_ASSERT_TRUE(buildHeartbeat(buf, sizeof(buf), h, &e, 1) > 0);
    TEST_ASSERT_NULL(strstr(buf, "\"reason\""));
}

void test_sources() {
    HeartbeatInfo h = info(false, 1);
    Event evs[3] = {
        ev(EventType::Stop, kDay + 60, Source::Normal, Reason::Done),
        ev(EventType::Stop, kDay + 60, Source::Today, Reason::Done),
        ev(EventType::Stop, kDay + 60, Source::Failsafe, Reason::Done),
    };
    char buf[1024];
    TEST_ASSERT_TRUE(buildHeartbeat(buf, sizeof(buf), h, evs, 3) > 0);
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"source\":\"normal\""));
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"source\":\"today\""));
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"source\":\"failsafe\""));
}

void test_buffer_too_small_returns_0() {
    HeartbeatInfo h = info(false);
    char buf[50];
    TEST_ASSERT_EQUAL_UINT32(0, buildHeartbeat(buf, 50, h, nullptr, 0));
}

void test_buffer_exact_fit_boundary() {
    HeartbeatInfo h = info(false);
    Event e = ev(EventType::Stop, kDay + 300, Source::Normal, Reason::Capped);
    char big[1024];
    size_t n = buildHeartbeat(big, sizeof(big), h, &e, 1);
    TEST_ASSERT_TRUE(n > 0);

    char buf[1024];
    // Vừa đủ (n + '\0') thì thành công; thiếu 1 byte thì trả 0.
    TEST_ASSERT_EQUAL_UINT32(n, buildHeartbeat(buf, n + 1, h, &e, 1));
    TEST_ASSERT_EQUAL_STRING(big, buf);
    TEST_ASSERT_EQUAL_UINT32(0, buildHeartbeat(buf, n, h, &e, 1));
}

// Trường hợp xấu nhất: mọi số dài nhất, chuỗi trạng thái dài nhất ("lost_power"), nhiệt độ âm dài
// nhất, 20 event với tổ hợp tên dài nhất mà formatter có thể in (skipped + failsafe + capped — kể cả
// record hỏng), epoch max.
void test_max_events_fits_buf_size() {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 4294967295u, INT32_MIN, false, 4294967295u, 4294967295u,
                       ModuleState::LostPower, ModuleState::LostPower, INT16_MIN + 1, 65535, 4294967295u};
    Event evs[kMaxEventsPerHeartbeat];
    for (uint16_t i = 0; i < kMaxEventsPerHeartbeat; i++)
        evs[i] = ev(EventType::Skipped, 4294967295u, Source::Failsafe, Reason::Capped);
    static char buf[4096];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, evs, kMaxEventsPerHeartbeat);
    TEST_ASSERT_TRUE(n > 0);
    TEST_ASSERT_EQUAL_INT(kMaxEventsPerHeartbeat,
                          countOccurrences(buf, "{\"type\":\"skipped\",\"at\":\"2106-02-07T06:28:15+07:00\","
                                                "\"source\":\"failsafe\",\"reason\":\"capped\"}"));
    char msg[64];
    snprintf(msg, sizeof msg, "worst case = %u byte", (unsigned)n);
    TEST_MESSAGE(msg);
    TEST_ASSERT_TRUE_MESSAGE(n + 1 <= kHeartbeatBufSize, msg);
    // Buffer đúng cỡ khai báo cũng build được.
    static char exact[kHeartbeatBufSize];
    TEST_ASSERT_EQUAL_UINT32(n, buildHeartbeat(exact, sizeof(exact), h, evs, kMaxEventsPerHeartbeat));
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_format_iso);
    RUN_TEST(test_civil_roundtrip);
    RUN_TEST(test_build_no_events);
    RUN_TEST(test_temp_unknown_omitted);
    RUN_TEST(test_module_states);
    RUN_TEST(test_temp_format);
    RUN_TEST(test_events_exact);
    RUN_TEST(test_start_never_has_reason);
    RUN_TEST(test_sources);
    RUN_TEST(test_buffer_too_small_returns_0);
    RUN_TEST(test_buffer_exact_fit_boundary);
    RUN_TEST(test_max_events_fits_buf_size);
    return UNITY_END();
}
