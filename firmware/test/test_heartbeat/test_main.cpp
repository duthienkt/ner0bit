#include <unity.h>
#include <string.h>
#include "civil_time.h"
#include "heartbeat.h"

void setUp() {}
void tearDown() {}

static Event ev(uint32_t start, uint32_t stop, Source src, uint8_t flags) {
    Event e;
    e.start = start;
    e.stop = stop;
    e.source = src;
    e.flags = flags;
    return e;
}

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
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 123456, -58, false, 1794323100};
    char buf[512];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, nullptr, 0);
    const char* expected =
        "{\"id\":\"a4cf12b8e901\",\"fw\":\"0.1.0\",\"uptime_s\":123456,\"rssi\":-58,"
        "\"pump_on\":false,\"rtc\":\"2026-11-10T15:05:00+07:00\",\"events\":[]}";
    TEST_ASSERT_EQUAL_STRING(expected, buf);
    TEST_ASSERT_EQUAL_UINT32(strlen(expected), n);
}

void test_build_with_capped_and_skipped() {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 123456, -58, true, 1794323100};
    Event evs[3] = {
        ev(kDay + 7 * 3600, kDay + 7 * 3600 + 300, Source::Today, 0),
        ev(kDay + 11 * 3600, kDay + 11 * 3600 + 1800, Source::Today, kCapped),
        ev(kDay + 11 * 3600 + 35 * 60, 0, Source::Today, kSkippedRest),
    };
    char buf[1024];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, evs, 3);
    TEST_ASSERT_EQUAL_UINT32(strlen(buf), n);
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"capped\":true"));
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"skipped\":\"rest\""));
    TEST_ASSERT_EQUAL_INT(2, countOccurrences(buf, "\"stop\""));

    // Event skipped (phần tử cuối) không có "stop".
    const char* last = strstr(buf, "{\"start\":\"2026-11-10T11:35:00+07:00\"");
    TEST_ASSERT_NOT_NULL(last);
    TEST_ASSERT_NULL(strstr(last, "\"stop\""));

    const char* expected =
        "{\"id\":\"a4cf12b8e901\",\"fw\":\"0.1.0\",\"uptime_s\":123456,\"rssi\":-58,"
        "\"pump_on\":true,\"rtc\":\"2026-11-10T15:05:00+07:00\",\"events\":["
        "{\"start\":\"2026-11-10T07:00:00+07:00\",\"stop\":\"2026-11-10T07:05:00+07:00\",\"source\":\"today\"},"
        "{\"start\":\"2026-11-10T11:00:00+07:00\",\"stop\":\"2026-11-10T11:30:00+07:00\",\"source\":\"today\",\"capped\":true},"
        "{\"start\":\"2026-11-10T11:35:00+07:00\",\"source\":\"today\",\"skipped\":\"rest\"}]}";
    TEST_ASSERT_EQUAL_STRING(expected, buf);
}

void test_sources() {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 1, 0, false, kDay};
    Event evs[3] = {
        ev(kDay, kDay + 60, Source::Normal, 0),
        ev(kDay, kDay + 60, Source::Today, 0),
        ev(kDay, kDay + 60, Source::Failsafe, 0),
    };
    char buf[1024];
    TEST_ASSERT_TRUE(buildHeartbeat(buf, sizeof(buf), h, evs, 3) > 0);
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"source\":\"normal\""));
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"source\":\"today\""));
    TEST_ASSERT_EQUAL_INT(1, countOccurrences(buf, "\"source\":\"failsafe\""));
}

void test_buffer_too_small_returns_0() {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 123456, -58, false, 1794323100};
    char buf[50];
    TEST_ASSERT_EQUAL_UINT32(0, buildHeartbeat(buf, 50, h, nullptr, 0));
}

void test_buffer_exact_fit_boundary() {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 123456, -58, false, 1794323100};
    Event e = ev(kDay, kDay + 300, Source::Normal, kCapped);
    char big[1024];
    size_t n = buildHeartbeat(big, sizeof(big), h, &e, 1);
    TEST_ASSERT_TRUE(n > 0);

    char buf[1024];
    // Vừa đủ (n + '\0') thì thành công; thiếu 1 byte thì trả 0.
    TEST_ASSERT_EQUAL_UINT32(n, buildHeartbeat(buf, n + 1, h, &e, 1));
    TEST_ASSERT_EQUAL_STRING(big, buf);
    TEST_ASSERT_EQUAL_UINT32(0, buildHeartbeat(buf, n, h, &e, 1));
}

void test_max_events_fits_buf_size() {
    // Trường hợp xấu nhất: uptime/rssi dài nhất, 20 event có stop + capped, epoch max.
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 4294967295u, -100, false, 4294967295u};
    Event evs[kMaxEventsPerHeartbeat];
    for (uint16_t i = 0; i < kMaxEventsPerHeartbeat; i++)
        evs[i] = ev(4294967295u, 4294967295u, Source::Failsafe, kCapped);
    char buf[kHeartbeatBufSize];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, evs, kMaxEventsPerHeartbeat);
    TEST_ASSERT_TRUE(n > 0);
    TEST_ASSERT_EQUAL_INT(kMaxEventsPerHeartbeat, countOccurrences(buf, "\"capped\":true"));
    TEST_ASSERT_EQUAL_INT(kMaxEventsPerHeartbeat, countOccurrences(buf, "\"start\":\"2106-02-07T06:28:15+07:00\""));
}

void test_skipped_busy() {
    HeartbeatInfo h = {"a4cf12b8e901", "0.1.0", 1, -58, true, 1794323100};
    Event e = ev(kDay + 7 * 3600 + 5 * 60, 0, Source::Normal, kSkippedBusy);
    char buf[512];
    size_t n = buildHeartbeat(buf, sizeof(buf), h, &e, 1);
    const char* expected =
        "{\"id\":\"a4cf12b8e901\",\"fw\":\"0.1.0\",\"uptime_s\":1,\"rssi\":-58,"
        "\"pump_on\":true,\"rtc\":\"2026-11-10T15:05:00+07:00\",\"events\":["
        "{\"start\":\"2026-11-10T07:05:00+07:00\",\"source\":\"normal\",\"skipped\":\"busy\"}]}";
    TEST_ASSERT_EQUAL_STRING(expected, buf);
    TEST_ASSERT_EQUAL_UINT32(strlen(expected), n);
}

int main(int, char**) {
    UNITY_BEGIN();
    RUN_TEST(test_format_iso);
    RUN_TEST(test_civil_roundtrip);
    RUN_TEST(test_build_no_events);
    RUN_TEST(test_build_with_capped_and_skipped);
    RUN_TEST(test_sources);
    RUN_TEST(test_buffer_too_small_returns_0);
    RUN_TEST(test_buffer_exact_fit_boundary);
    RUN_TEST(test_max_events_fits_buf_size);
    RUN_TEST(test_skipped_busy);
    return UNITY_END();
}
