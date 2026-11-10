#include "net_watch.h"

bool netStale(uint32_t nowMono, uint32_t lastAlive, uint32_t limit) {
    const int32_t age = (int32_t)(nowMono - lastAlive);
    return age > (int32_t)limit;
}

bool netRestartDue(uint32_t nowMono, uint32_t lastAlive, uint32_t limit, bool pumpOn) {
    return !pumpOn && netStale(nowMono, lastAlive, limit);
}
