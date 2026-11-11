// Ner0bit firmware — ESP32-C3 Super Mini.
// loop(): scheduler bơm 1 lần/giây theo đồng hồ hệ thống. Network task (net_task) chạy song song:
// WiFi, NTP, heartbeat. Module DS3231 + AT24C32 tuỳ chọn (xem shared.h).
// Chế độ cấu hình (portal) thay cho network task khi chưa có cấu hình hoặc giữ BOOT lúc boot.
// Watchdog: loop task trong task watchdog (10 s, panic ⇒ reset); net task giám sát bằng dấu "còn
// sống" (net_watch.h) vì một lần POST HTTPS có thể chặn vài chục giây hợp lệ.
#include <Arduino.h>
#include <WiFi.h>
#include <Wire.h>
#include <string.h>
#include <esp_task_wdt.h>

#include "config_store.h"
#include "led_pattern.h"
#include "net_task.h"
#include "net_watch.h"
#include "pins.h"
#include "portal.h"
#include "pump.h"
#include "schedule.h"
#include "schedule_format.h"
#include "schedule_store.h"
#include "shared.h"

static char s_schedBody[kScheduleBodyMax];  // static: không chiếm stack loopTask
static Schedule s_schedule;
static NetConfig s_cfg;

constexpr uint32_t kLoopWdtMs = 10000;  // loop task (và vòng lặp portal) phải nuôi WDT trong 10 s
// USB CDC: host đã nhận cổng mà không đọc (cắm máy tính lấy điện, chưa mở Serial) ⇒ mỗi
// Serial.write chờ tới 20 × timeout. Mặc định 100 ms ⇒ ~2 s mỗi dòng log, vài dòng trong một nhịp
// loop là chạm watchdog. 20 ms ⇒ ≤ 0,4 s mỗi dòng; host đọc bình thường không bị ảnh hưởng.
constexpr uint32_t kSerialTxTimeoutMs = 20;

// Task watchdog của IDF: timeout 10 s, panic khi hết hạn (⇒ reset, lý do task_wdt), giữ nguyên
// việc giám sát idle task theo sdkconfig; rồi thêm loop task. Từ đây loop task phải nuôi WDT: core
// làm mỗi vòng loop(), portal::run tự làm trong vòng lặp của nó.
static void armLoopWatchdog() {
    esp_task_wdt_config_t cfg = {};
    cfg.timeout_ms = kLoopWdtMs;
#if CONFIG_ESP_TASK_WDT_CHECK_IDLE_TASK_CPU0
    cfg.idle_core_mask = 1u << 0;
#else
    cfg.idle_core_mask = 0;
#endif
    cfg.trigger_panic = true;
    esp_err_t err = esp_task_wdt_reconfigure(&cfg);
    if (err == ESP_ERR_INVALID_STATE) err = esp_task_wdt_init(&cfg);  // TWDT chưa khởi tạo
    if (err != ESP_OK) {
        nlog("[main] lỗi cấu hình watchdog (%s) — chạy không có watchdog\n", esp_err_to_name(err));
        return;
    }
    enableLoopWDT();
    if (esp_task_wdt_status(nullptr) != ESP_OK) {
        nlogln("[main] lỗi thêm loop task vào watchdog — chạy không có watchdog");
        return;
    }
    nlog("[main] watchdog vòng lặp chính: %lu s\n", (unsigned long)(kLoopWdtMs / 1000));
}

// Net task không đóng dấu "còn sống" quá kNetStaleS ⇒ khởi động lại, nhưng chỉ khi bơm tắt (bơm
// đang chạy ⇒ chờ hết phiên). Sau reset, tưới bù và lastStop giữ qua reset lo phần còn lại.
static void checkNetTask() {
    uint32_t alive = 0;
    if (!netTaskAliveAt(alive)) return;  // không có net task (tạo lỗi) ⇒ không giám sát
    const uint32_t now = monoSec();      // đọc sau dấu ⇒ dấu không "mới hơn" now
    if (!netRestartDue(now, alive, kNetStaleS, pump::isOn())) return;
    nlog("[main] luồng mạng không phản hồi %lu s (> %lu s), bơm đang tắt ⇒ khởi động lại\n",
         (unsigned long)(now - alive), (unsigned long)kNetStaleS);
    stopPumpForRestart();
    delay(100);  // cho Serial in xong
    ESP.restart();
}

// Mỗi module một dòng log, rồi một dòng chế độ lưu trữ.
static void logModules(ModuleState ds, ModuleState at, bool clockValid, uint32_t rtcUtc) {
    switch (ds) {
        case ModuleState::Ok:
            if (clockValid) {
                nlog("[main] DS3231: ok — đồng hồ hệ thống lấy từ DS3231 (utc=%lu)\n",
                              (unsigned long)rtcUtc);
            } else {
                nlog("[main] DS3231: ok nhưng giờ vô lý (utc=%lu) — chờ NTP / server\n",
                              (unsigned long)rtcUtc);
            }
            break;
        case ModuleState::LostPower:
            nlogln("[main] DS3231: lost_power — cờ OSF bật (pin CR2032?), giờ không tin "
                           "được — chờ NTP / server");
            break;
        case ModuleState::Failed:
            nlogln("[main] DS3231: failed — có ACK nhưng đọc lỗi, bỏ dùng — chờ NTP / "
                           "server");
            break;
        case ModuleState::Absent:
            nlogln("[main] DS3231: absent — không có module, chờ NTP / server (chưa có giờ "
                           "thì không tưới)");
            break;
    }
    switch (at) {
        case ModuleState::Ok:
            nlogln("[main] AT24C32: ok");
            break;
        case ModuleState::Failed:
            nlogln("[main] AT24C32: failed — có ACK nhưng đọc/ghi header queue lỗi, bỏ "
                           "dùng");
            break;
        case ModuleState::LostPower:  // không dùng cho AT24C32
        case ModuleState::Absent:
            nlogln("[main] AT24C32: absent");
            break;
    }
    nlogln(at == ModuleState::Ok
                       ? "[main] lưu trữ: AT24C32 (queue events, lastStop, lịch cache)"
                       : "[main] lưu trữ: RAM (queue events — mất khi reset; lastStop — giữ qua "
                         "reset mềm, mất khi mất điện) + NVS (lịch cache)");
}

void setup() {
    // 1. Bơm tắt trước mọi thứ, rồi Serial, lock, LED (active-LOW, tắt).
    pump::begin();
    Serial.begin(115200);
#if ARDUINO_USB_CDC_ON_BOOT && ARDUINO_USB_MODE
    Serial.setTxTimeoutMs(kSerialTxTimeoutMs);  // Serial là HWCDC
#endif
    sharedInit();
    pinMode(LED_PIN, OUTPUT);
    digitalWrite(LED_PIN, HIGH);

    // 2. Cửa sổ 5 s: giữ BOOT ≥ 3 s ⇒ chế độ cấu hình.
    // Lệnh "setup" qua Serial ⇒ đã đặt cờ trước khi restart: vào thẳng setup, bỏ cửa sổ nút BOOT.
    const bool serialSetup = consumeSetupRequest();
    const bool button = serialSetup || portal::bootButtonHeld();
    if (serialSetup) nlogln("[main] vào chế độ cấu hình theo lệnh \"setup\" qua Serial");
    nlog("[main] Ner0bit fw %s, nút BOOT: %s\n", kFwVersion, button ? "giữ ⇒ setup" : "không");
    nlog("[main] lý do khởi động: %s\n", resetReasonText());

    // 3. I2C, module DS3231 / AT24C32 (đều tuỳ chọn), đồng hồ hệ thống, storage.
    Wire.begin(I2C_SDA, I2C_SCL, I2C_FREQ);
    uint32_t rtcUtc = 0;
    ModuleState ds;
    ModuleState at;
    bool clockValid;
    uint16_t queued;
    LastStopBoot ls;
    {
        SharedLock lock;
        clockBoot(rtcUtc);
        storageBoot();
        ls = lastStopBoot();
        g_sched.setFetchOk(false);
        ds = g_ds3231;
        at = g_at24c32;
        clockValid = g_clockValid;
        queued = g_queue.count();
    }
    if (ls.switched) logStorageFailover();
    logModules(ds, at, clockValid, rtcUtc);
    if (ls.retainedValid) {
        nlog("[main] queue %u events, lastStop %lu (storage %lu, bản sao giữ qua reset "
                      "%lu)\n",
                      (unsigned)queued, (unsigned long)ls.seed, (unsigned long)ls.stored,
                      (unsigned long)ls.retained);
    } else {
        nlog("[main] queue %u events, lastStop %lu (bản sao giữ qua reset: không có)\n",
                      (unsigned)queued, (unsigned long)ls.seed);
    }

    // 4. Lịch cache (AT24C32 hoặc NVS theo chế độ storage).
    uint32_t fetchDay = 0;
    const bool hasCache = schedule_store::load(s_schedBody, sizeof s_schedBody, fetchDay);
    const bool cacheOk = hasCache && parseSchedule(s_schedBody, s_schedule);
    if (cacheOk) {
        SharedLock lock;
        g_sched.setSchedule(s_schedule, fetchDay);
    }
    nlog("[main] lịch cache: %s\n",
                  cacheOk ? "đã nạp" : (hasCache ? "lỗi, bỏ qua" : "chưa có"));
    if (cacheOk) {
        static char line[kScheduleLogMax];  // static: không chiếm stack loop task
        formatSchedule(s_schedule, line, sizeof line);
        nlog("[main] lịch: %s (nhận ngày %lu)\n", line, (unsigned long)fetchDay);
    }

    // 5. Watchdog loop task — sau cửa sổ 5 s và khởi tạo module, trước portal (không return) /
    // net task.
    armLoopWatchdog();

    // 6. Chế độ cấu hình hoặc chạy thường.
    const bool hasCfg = config::load(s_cfg);
    if (!hasCfg || button) {
        nlogln(hasCfg ? "[main] vào chế độ cấu hình (nút BOOT)"
                              : "[main] chưa có cấu hình ⇒ chế độ cấu hình");
        portal::run(hasCfg ? &s_cfg : nullptr);  // không return
    }
    startNetTask(s_cfg);
}

// Lệnh qua Serial (pio device monitor): gõ "setup" + Enter ⇒ khởi động lại vào chế độ cấu hình.
static void pollSerialCommand() {
    static char line[16];
    static uint8_t len = 0;
    while (Serial.available() > 0) {
        const int c = Serial.read();
        if (c == '\r' || c == '\n') {
            line[len] = '\0';
            if (len > 0 && strcmp(line, "setup") == 0) {
                nlogln("[main] lệnh \"setup\": tắt bơm, khởi động lại vào chế độ cấu hình");
                stopPumpForRestart();
                requestSetupOnNextBoot();
                delay(100);  // cho log kịp ra
                ESP.restart();
            } else if (len > 0) {
                nlog("[main] lệnh không rõ \"%s\" (có: setup)\n", line);
            }
            len = 0;
        } else if (len < sizeof line - 1) {
            line[len++] = (char)c;
        }
    }
}

void loop() {  // core nuôi watchdog trước mỗi lần gọi loop()
    schedulerTick();  // tự giới hạn 1 lần / 1000 ms
    pollSerialCommand();
    checkNetTask();   // sau tick: pump::isOn() là trạng thái mới nhất
    // Đèn báo trạng thái (active-LOW): bơm chạy chớp nhanh, chưa có giờ chớp đôi, mất WiFi chớp
    // dài, bình thường nháy ngắn mỗi 5 s. Đọc trạng thái không cần g_lock (chỉ để hiển thị).
    const LedState led = pickLed(pump::isOn(), g_clockValid, WiFi.isConnected());
    digitalWrite(LED_PIN, ledOn(led, millis()) ? LOW : HIGH);
    delay(20);
}
