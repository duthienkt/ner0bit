#pragma once
#include <stdint.h>
#include "event.h"
#include "schedule.h"

constexpr uint32_t kHardCap = 1800;
constexpr uint32_t kMinRest = 600;

struct TickResult {
    bool pumpOn;
    bool hasEvent;
    Event event;
    bool lastStopChanged;
    uint32_t lastStop;
};

class Scheduler {
public:
    void setSchedule(const Schedule& s, uint32_t fetchDay);  // fetchDay = local day lúc nhận lịch
    void setFetchOk(bool ok);
    void setLastStop(uint32_t local);  // nạp từ EEPROM lúc boot, 0 = chưa từng
    bool hasSchedule() const { return hasSchedule_; }
    TickResult tick(uint32_t nowLocal, uint32_t monoSec);  // gọi ~1 lần/giây

private:
    Schedule sched_{};
    bool hasSchedule_ = false;
    uint32_t fetchDay_ = 0;
    bool fetchOk_ = false;
    uint32_t lastStop_ = 0;
    uint32_t handledUpTo_ = 0;
    bool haveRunStop_ = false;  // đã có lần tắt trong lần chạy này (RAM)
    uint32_t monoLastStop_ = 0;

    // Chọn dòng áp dụng cho ngày `day`; nullptr nếu ngày đó không có mốc nào.
    const Line* lineForDay(uint32_t day, Source& src) const;
    bool restViolated(uint32_t now, uint32_t mono) const;

    bool running_ = false;
    uint32_t startActual_ = 0;
    uint32_t stopAt_ = 0;
    uint32_t monoStart_ = 0;
    uint32_t runDur_ = 0;
    Source runSource_ = Source::Normal;
    uint8_t runFlags_ = 0;
};
