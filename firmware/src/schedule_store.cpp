#include "schedule_store.h"

#include <Arduino.h>

#include "config_store.h"
#include "schedule_cache.h"
#include "shared.h"

static_assert(kScheduleBodyMax - 1 <= kScheduleCacheMaxLen,
              "body lịch dài nhất phải vừa vùng cache trên AT24C32");

namespace schedule_store {

bool load(char* body, size_t cap, uint32_t& fetchDay) {
    {
        SharedLock lock;
        if (storageOnEeprom()) return scheduleCacheLoad(storageDevice(), body, cap, fetchDay);
    }
    return config::loadSchedule(body, cap, fetchDay);
}

void saveIfChanged(const char* body, uint32_t fetchDay, bool hasLine2) {
    CacheWrite w = CacheWrite::Unchanged;
    bool eeprom;
    bool switched = false;
    {
        SharedLock lock;
        eeprom = storageOnEeprom();
        if (eeprom) {
            w = scheduleCacheSave(storageDevice(), body, fetchDay);
            switched = storageFailover();
        }
    }
    if (switched) logStorageFailover();
    if (eeprom && !switched) {
        if (w == CacheWrite::Written) {
            Serial.printf("[store] lịch cache ghi AT24C32 (day %lu)\n", (unsigned long)fetchDay);
        } else if (w == CacheWrite::Invalid) {
            Serial.println("[store] lịch rỗng hoặc quá dài, không cache");
        }
        return;
    }
    config::saveScheduleIfChanged(body, fetchDay, hasLine2);  // NVS, không cần g_lock
}

}  // namespace schedule_store
