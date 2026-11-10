#pragma once
#include <stddef.h>
#include <stdint.h>

#include <freertos/FreeRTOS.h>
#include <freertos/semphr.h>

#include "event_queue.h"
#include "scheduler.h"

// Trạng thái dùng chung giữa loop() (scheduler bơm) và network task.
// g_lock bao: bus I2C (Wire: DS3231 + AT24C32), g_queue, g_sched, g_clockValid.
// Chỉ giữ lock trong đoạn ngắn — không bao giờ giữ khi kết nối WiFi hay gửi HTTP.
extern SemaphoreHandle_t g_lock;
extern EventQueue g_queue;  // trên AT24C32; main gọi g_queue.begin() sau Wire.begin()
extern Scheduler g_sched;
extern volatile bool g_clockValid;  // DS3231 không mất nguồn, hoặc đã chỉnh theo NTP

constexpr uint32_t kUtcOffset = 25200;  // UTC+7
// Kích thước buffer (kể cả '\0') cho body lịch: body dài hơn bị coi là lịch lỗi, không cache.
constexpr size_t kScheduleBodyMax = 1024;

// Tạo g_lock. Gọi một lần trong setup(), trước mọi truy cập dùng lock và trước startNetTask().
void sharedInit();

// Local epoch = UTC đọc từ DS3231 + 25200. Gọi trong lock.
// false nếu đọc DS3231 lỗi (ruling R4: coi như đồng hồ không dùng được).
// Không kiểm tra g_clockValid — caller tự kiểm.
bool localNow(uint32_t& out);

// Giây monotonic kể từ boot (esp_timer 64 bit, không wrap như millis()) — ruling R3.
uint32_t monoSec();

// RAII: lấy g_lock trong phạm vi khối.
class SharedLock {
public:
    SharedLock() { xSemaphoreTake(g_lock, portMAX_DELAY); }
    ~SharedLock() { xSemaphoreGive(g_lock); }
    SharedLock(const SharedLock&) = delete;
    SharedLock& operator=(const SharedLock&) = delete;
};
