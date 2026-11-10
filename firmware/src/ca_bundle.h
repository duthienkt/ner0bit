#pragma once

// 7 root CA (PEM nối tiếp, kết thúc '\0') cho WiFiClientSecure::setCACert(). Xem ca_bundle.cpp.
extern const char kCaBundle[];
