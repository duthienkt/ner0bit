#include "scheduler.h"

void Scheduler::setSchedule(const Schedule& s, uint32_t fetchDay) {
    sched_ = s;
    fetchDay_ = fetchDay;
    hasSchedule_ = true;
}

void Scheduler::setFetchOk(bool ok) { fetchOk_ = ok; }
void Scheduler::setLastStop(uint32_t local) { lastStop_ = local; }

TickResult Scheduler::tick(uint32_t now, uint32_t mono) {
    TickResult r{};
    r.lastStop = lastStop_;

    if (running_) {
        if (now >= stopAt_ || (uint32_t)(mono - monoStart_) >= runDur_) {
            running_ = false;
            lastStop_ = now;
            r.hasEvent = true;
            r.event = Event{startActual_, now, runSource_, runFlags_};
            r.lastStopChanged = true;
            r.lastStop = now;
        } else {
            r.pumpOn = true;
        }
        return r;
    }

    if (!hasSchedule_) return r;

    const uint32_t today = now / 86400;
    const Line* line = &sched_.line1;
    Source src = Source::Normal;
    if (fetchDay_ == today) {
        if (sched_.l2 == Line2State::Present) {
            line = &sched_.line2;
            src = Source::Today;
        } else if (sched_.l2 == Line2State::Empty) {
            return r;
        }
    }
    if (!fetchOk_) src = Source::Failsafe;

    for (uint8_t i = 0; i < line->n; i++) {
        const Entry& e = line->e[i];
        const uint32_t absStart = today * 86400 + e.start;
        const uint32_t capDur = e.duration > kHardCap ? kHardCap : e.duration;
        if (!(absStart <= now && now < absStart + capDur)) continue;
        if (absStart <= handledUpTo_) continue;

        handledUpTo_ = absStart;
        if (lastStop_ != 0 && now - lastStop_ < kMinRest) {
            r.hasEvent = true;
            r.event = Event{absStart, 0, src, kSkippedRest};
        } else {
            running_ = true;
            startActual_ = now;
            stopAt_ = absStart + capDur;
            runDur_ = stopAt_ - now;
            monoStart_ = mono;
            runSource_ = src;
            runFlags_ = e.duration > kHardCap ? kCapped : 0;
            r.pumpOn = true;
        }
        return r;
    }
    return r;
}
