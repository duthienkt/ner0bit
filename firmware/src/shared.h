#pragma once
#include <stddef.h>
#include <stdint.h>

#include <freertos/FreeRTOS.h>
#include <freertos/semphr.h>

#include "event_queue.h"
#include "heartbeat.h"
#include "scheduler.h"
#include "storage.h"

// Trạng thái dùng chung giữa loop() (scheduler bơm) và network task.
// g_lock bao: bus I2C (Wire: DS3231 + AT24C32), storage (chế độ + g_queue), g_sched,
// g_clockValid, g_ds3231, g_at24c32. Chỉ giữ lock trong đoạn ngắn — không bao giờ giữ khi kết
// nối WiFi hay gửi HTTP.
extern SemaphoreHandle_t g_lock;
extern EventQueue g_queue;  // trên AT24C32 hoặc RAM (storageBoot chọn)
extern Scheduler g_sched;
// Đồng hồ hệ thống (time()) đã được đặt từ nguồn tin cậy: DS3231 lúc boot (có module, OSF
// không bật), NTP được TimeTrust nhận, hoặc header Date của server.
extern volatile bool g_clockValid;
extern ModuleState g_ds3231;   // Absent | Ok | LostPower (OSF bật lúc boot) | Failed (lỗi I/O)
extern ModuleState g_at24c32;  // Absent | Ok | Failed (lỗi lúc boot hoặc ghi lỗi lúc chạy)

constexpr uint32_t kUtcOffset = 25200;  // UTC+7
// Kích thước buffer (kể cả '\0') cho body lịch: body dài hơn bị coi là lịch lỗi, không cache.
constexpr const char* kFwVersion = "0.2.0";  // in ra log lúc boot + trường "fw" của heartbeat
constexpr size_t kScheduleBodyMax = 1024;

// Tạo g_lock. Gọi một lần trong setup(), trước mọi truy cập dùng lock và trước startNetTask().
void sharedInit();

// Log ra Serial, mỗi dòng có tiền tố giờ: "17:38:13 " (giờ VN, khi đồng hồ hợp lệ) hoặc
// "+12s " (uptime, khi chưa có giờ). Cả dòng in một lần dưới khoá riêng ⇒ 2 task không chen nhau.
void nlog(const char* fmt, ...) __attribute__((format(printf, 1, 2)));  // fmt tự kèm '\n'
void nlogln(const char* line);

// Lúc boot, sau Wire.begin(), trong lock. Dò DS3231 (ACK 0x68): có module, OSF tắt và đọc giờ
// hợp lệ ⇒ settimeofday + g_clockValid. Đặt g_ds3231. utc = giờ đọc được (0 nếu không đọc).
void clockBoot(uint32_t& utc);

// Lúc boot, sau Wire.begin(), trong lock. Dò AT24C32 (ACK 0x57) rồi g_queue.begin(): đọc được
// header (và format được nếu cần) ⇒ chế độ AT24C32; không thì chế độ RAM (queue + lastStop
// trong RamStorage, lịch cache trong NVS). Đặt g_at24c32.
void storageBoot();

struct LastStopBoot {
    uint32_t stored;     // lastStop đọc từ queue (AT24C32; chế độ RAM: 0)
    bool retainedValid;  // bản sao giữ qua reset hợp lệ (không phải cấp điện lại, check khớp)
    uint32_t retained;   // giá trị bản sao (0 nếu không hợp lệ)
    uint32_t seed;       // giá trị mới hơn ⇒ nạp cho g_sched
    bool switched;       // ghi AT24C32 lỗi ⇒ vừa chuyển sang RAM (caller log ngoài lock)
};
// Lúc boot, sau storageBoot(), trong lock. lastStop = giá trị mới hơn giữa queue và bản sao
// RTC_NOINIT (giữ qua reset mềm, WDT, panic, brownout; bỏ khi esp_reset_reason() là POWERON —
// lần tắt do mất điện được miễn quy tắc nghỉ). Nạp vào g_sched, chép lại vào queue và bản sao.
LastStopBoot lastStopBoot();

// true: queue, lastStop, lịch cache trên AT24C32. false: RAM + NVS. Gọi trong lock.
bool storageOnEeprom();
// IStorage của chế độ hiện tại (ghi AT24C32 có thử lại; lỗi được ghi nhận cho storageFailover).
// Gọi trong lock.
IStorage& storageDevice();
// Gọi trong lock sau mỗi nhóm thao tác ghi storage. AT24C32 vừa ghi lỗi (đã thử lại) ⇒ chuyển
// hẳn sang RAM tới hết lần chạy: g_at24c32 = Failed, queue RAM mới (rỗng, giữ lastStop), tăng
// storageGeneration(). true nếu vừa chuyển — caller ghi log ngoài lock.
bool storageFailover();
// Tăng mỗi lần đổi storage. Net task so trước/sau khi gửi: khác ⇒ events đã peek thuộc queue
// cũ, không popSent trên queue mới. Gọi trong lock.
uint32_t storageGeneration();
// Log một dòng về việc chuyển sang RAM. Gọi ngoài lock.
void logStorageFailover();

// Local epoch = UTC đồng hồ hệ thống + 25200 (không I2C). false nếu giờ hệ thống vô lý
// (< 2026 hoặc ≥ 2100). Không kiểm tra g_clockValid — caller tự kiểm.
bool localNow(uint32_t& out);
// UTC đồng hồ hệ thống (giây, kẹp vào uint32).
uint32_t systemUtc();

enum class RtcWrite : uint8_t { NoModule, Ok, Failed };
// Đặt đồng hồ hệ thống = utc + usec µs, g_clockValid = true, rồi ghi DS3231 nếu module dùng
// được (Ok/LostPower). Ghi DS3231 lỗi (đã thử lại) ⇒ g_ds3231 = Failed, không dùng DS3231
// nữa; tưới vẫn chạy theo đồng hồ hệ thống. Gọi trong lock.
RtcWrite setClock(uint32_t utc, uint32_t usec);

constexpr uint8_t kDs3231TempFails = 3;
// Nhiệt độ DS3231 (0,25 °C) hoặc kTempUnknown (không module / đọc lỗi). kDs3231TempFails lần lỗi
// liên tiếp ⇒ g_ds3231 = Failed. Gọi trong lock (lúc heartbeat).
int16_t readDs3231Temp();

// Giây monotonic kể từ boot (esp_timer 64 bit, không wrap như millis()) — ruling R3.
uint32_t monoSec();

// Một nhịp scheduler bơm. Gọi thường xuyên từ loop() và từ vòng lặp portal; tự giới hạn
// tối đa 1 tick / 1000 ms. Tự lấy g_lock. Không đọc I2C (giờ lấy từ đồng hồ hệ thống); chỉ ghi
// storage khi có event / lastStop đổi. Có event ⇒ báo net task (heartbeat sớm). Đồng hồ không
// dùng được (!g_clockValid) ⇒ tắt bơm, kết thúc phiên đang chạy (Scheduler::abort) và bỏ tick.
void schedulerTick();

// Gọi ngay trước mọi lần chủ động restart (ESP.restart()): kết thúc phiên đang chạy
// (event + lastStop ghi xuống storage) rồi tắt bơm. Tự lấy g_lock.
void stopPumpForRestart();

// RAII: lấy g_lock trong phạm vi khối.
class SharedLock {
public:
    SharedLock() { xSemaphoreTake(g_lock, portMAX_DELAY); }
    ~SharedLock() { xSemaphoreGive(g_lock); }
    SharedLock(const SharedLock&) = delete;
    SharedLock& operator=(const SharedLock&) = delete;
};
