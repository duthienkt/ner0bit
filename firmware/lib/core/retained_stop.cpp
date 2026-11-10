#include "retained_stop.h"

namespace {

uint32_t checkOf(uint32_t magic, uint32_t lastStop) { return ~(magic ^ lastStop); }

}  // namespace

void retainedStopWrite(RetainedStop& r, uint32_t lastStop) {
    r.magic = kRetainedStopMagic;
    r.lastStop = lastStop;
    r.check = checkOf(kRetainedStopMagic, lastStop);
}

bool retainedStopValid(const RetainedStop& r, bool powerOn) {
    if (powerOn) return false;
    return r.magic == kRetainedStopMagic && r.check == checkOf(r.magic, r.lastStop);
}

uint32_t retainedStopSeed(uint32_t stored, const RetainedStop& r, bool powerOn) {
    if (!retainedStopValid(r, powerOn)) return stored;
    return r.lastStop > stored ? r.lastStop : stored;
}
