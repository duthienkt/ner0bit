#include "portal.h"

#include <Arduino.h>
#include <DNSServer.h>
#include <WebServer.h>
#include <WiFi.h>
#include <string.h>

#include "pins.h"
#include "shared.h"

namespace portal {

namespace {

constexpr uint32_t kWindowMs = 5000;       // cửa sổ kiểm tra nút BOOT lúc boot
constexpr uint32_t kHoldMs = 3000;         // giữ liên tục ≥ 3 s ⇒ setup mode
constexpr uint32_t kBlinkMs = 100;         // LED nháy nhanh trong cửa sổ
constexpr uint32_t kIdleTimeoutMs = 10UL * 60 * 1000;  // có cấu hình cũ, 10 phút không lưu
constexpr uint32_t kRestartDelayMs = 1000; // chờ gửi xong trang "đã lưu" rồi mới restart
constexpr const char* kApSsid = "Ner0bit-Setup";
constexpr uint8_t kLedOn = LOW;            // LED active-LOW
constexpr uint8_t kLedOff = HIGH;

const IPAddress kApIp(192, 168, 4, 1);
const IPAddress kApMask(255, 255, 255, 0);

const NetConfig* s_current = nullptr;
WebServer* s_server = nullptr;
DNSServer* s_dns = nullptr;
bool s_restartPending = false;
uint32_t s_restartAt = 0;

// Thoát HTML cho giá trị chèn vào nội dung hoặc thuộc tính (value="...").
void appendEscaped(String& out, const String& s) {
    for (size_t i = 0; i < s.length(); ++i) {
        const char c = s[i];
        switch (c) {
            case '&': out += F("&amp;"); break;
            case '<': out += F("&lt;"); break;
            case '>': out += F("&gt;"); break;
            case '"': out += F("&quot;"); break;
            case '\'': out += F("&#39;"); break;
            default: out += c; break;
        }
    }
}

void sendPage(int code, const String& body) {
    String page;
    page.reserve(body.length() + 600);
    page += F("<!DOCTYPE html><html><head><meta charset=\"utf-8\">"
              "<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">"
              "<title>Ner0bit Setup</title><style>"
              "body{font-family:sans-serif;max-width:28em;margin:1em auto;padding:0 1em}"
              "label{display:block;margin:.8em 0 .2em}"
              "input{width:100%;box-sizing:border-box;padding:.4em;font-size:1em}"
              "button{margin-top:1.2em;padding:.6em 1.2em;font-size:1em}"
              ".err{color:#b00;font-weight:bold}.hint{color:#666;font-size:.9em}"
              "</style></head><body><h1>Ner0bit Setup</h1>");
    page += body;
    page += F("</body></html>");
    s_server->send(code, "text/html; charset=utf-8", page);
}

// Form 4 trường. ssid/url là giá trị điền sẵn (đã nhập hoặc cấu hình cũ). Không bao giờ
// điền lại password/token.
void sendForm(int code, const char* error, const String& ssid, const String& url) {
    const bool hasOld = s_current != nullptr;
    String b;
    b.reserve(1200);
    if (error) {
        b += F("<p class=\"err\">");
        appendEscaped(b, error);
        b += F("</p>");
    }
    b += F("<form method=\"post\" action=\"/save\">"
           "<label for=\"ssid\">WiFi SSID</label>"
           "<input id=\"ssid\" name=\"ssid\" maxlength=\"32\" required value=\"");
    appendEscaped(b, ssid);
    b += F("\"><label for=\"pass\">WiFi password</label>"
           "<input id=\"pass\" name=\"pass\" type=\"password\" maxlength=\"64\"");
    if (hasOld) b += F(" placeholder=\"để trống = giữ mật khẩu cũ\"");
    b += F("><label for=\"url\">Server URL</label>"
           "<input id=\"url\" name=\"url\" maxlength=\"199\" required "
           "placeholder=\"https://...\" value=\"");
    appendEscaped(b, url);
    b += F("\"><label for=\"token\">Token</label>"
           "<input id=\"token\" name=\"token\" type=\"password\" maxlength=\"64\"");
    if (hasOld) b += F(" placeholder=\"để trống = giữ token cũ\"");
    b += F("><button type=\"submit\">Lưu &amp; khởi động lại</button></form>"
           "<p class=\"hint\">Server URL bắt buộc bắt đầu bằng https://</p>");
    sendPage(code, b);
}

void handleRoot() {
    if (s_current) {
        sendForm(200, nullptr, String(s_current->ssid), String(s_current->url));
    } else {
        sendForm(200, nullptr, String(), String());
    }
}

// Chép s (đã kiểm độ dài < N) vào mảng cố định, luôn kết thúc '\0'.
template <size_t N>
void copyField(char (&dst)[N], const String& s) {
    memset(dst, 0, N);
    memcpy(dst, s.c_str(), s.length());
}

void handleSave() {
    const String ssid = s_server->arg("ssid");
    const String pass = s_server->arg("pass");
    String url = s_server->arg("url");
    String token = s_server->arg("token");
    url.trim();
    token.trim();

    NetConfig cfg;
    memset(&cfg, 0, sizeof cfg);
    if (s_current) cfg = *s_current;

    const char* error = nullptr;
    if (ssid.length() == 0) {
        error = "Chưa điền WiFi SSID.";
    } else if (ssid.length() >= sizeof cfg.ssid) {
        error = "WiFi SSID quá dài (tối đa 32 byte).";
    } else if (pass.length() >= sizeof cfg.pass) {
        error = "WiFi password quá dài (tối đa 64 byte).";
    } else if (!url.startsWith("https://") || url.length() <= 8) {
        error = "Server URL phải bắt đầu bằng https:// (không hỗ trợ http://).";
    } else if (url.length() >= sizeof cfg.url) {
        error = "Server URL quá dài (tối đa 199 byte).";
    } else if (token.length() >= sizeof cfg.token) {
        error = "Token quá dài (tối đa 64 byte).";
    }
    if (error) {
        Serial.printf("[portal] form lỗi: %s\n", error);
        sendForm(400, error, ssid, url);
        return;
    }

    copyField(cfg.ssid, ssid);
    copyField(cfg.url, url);
    // Để trống password/token ⇒ giữ giá trị cũ (nếu có); chưa có cấu hình cũ ⇒ rỗng.
    if (pass.length() > 0 || !s_current) copyField(cfg.pass, pass);
    if (token.length() > 0 || !s_current) copyField(cfg.token, token);

    config::save(cfg);
    Serial.printf("[portal] đã lưu cấu hình (SSID \"%s\"), khởi động lại\n", cfg.ssid);
    sendPage(200, F("<p>Đã lưu. Thiết bị đang khởi động lại và nối WiFi nhà.</p>"));
    s_restartPending = true;
    s_restartAt = millis() + kRestartDelayMs;
}

// Captive portal: mọi đường dẫn lạ (generate_204, hotspot-detect.html, ...) ⇒ chuyển về form.
void handleNotFound() {
    s_server->sendHeader("Location", "http://192.168.4.1/", true);
    s_server->send(302, "text/plain", "");
}

}  // namespace

bool bootButtonHeld() {
    pinMode(BOOT_BUTTON_PIN, INPUT_PULLUP);
    pinMode(LED_PIN, OUTPUT);

    const uint32_t t0 = millis();
    bool pressed = false;
    uint32_t pressedSince = 0;
    bool held = false;
    for (uint32_t now = t0; now - t0 < kWindowMs; now = millis()) {
        digitalWrite(LED_PIN, ((now - t0) / kBlinkMs) % 2 == 0 ? kLedOn : kLedOff);
        if (digitalRead(BOOT_BUTTON_PIN) == LOW) {
            if (!pressed) {
                pressed = true;
                pressedSince = now;
            }
            if (now - pressedSince >= kHoldMs) {
                held = true;
                break;
            }
        } else {
            pressed = false;  // nhả nút ⇒ đếm lại từ đầu
        }
        delay(10);
    }
    digitalWrite(LED_PIN, kLedOff);
    return held;
}

void run(const NetConfig* current) {
    s_current = current;
    digitalWrite(LED_PIN, kLedOn);  // LED sáng liên tục = đang ở chế độ cấu hình

    WiFi.persistent(false);
    WiFi.mode(WIFI_AP);
    if (!WiFi.softAPConfig(kApIp, kApIp, kApMask)) Serial.println("[portal] lỗi softAPConfig");
    if (!WiFi.softAP(kApSsid)) Serial.println("[portal] lỗi softAP");

    s_dns = new DNSServer;
    s_dns->setErrorReplyCode(DNSReplyCode::NoError);
    if (!s_dns->start(53, "*", kApIp)) Serial.println("[portal] lỗi DNSServer");

    s_server = new WebServer(80);
    s_server->on("/", HTTP_GET, handleRoot);
    s_server->on("/save", HTTP_POST, handleSave);
    s_server->onNotFound(handleNotFound);
    s_server->begin();

    Serial.printf("[portal] AP \"%s\" (không mật khẩu), mở http://192.168.4.1/%s\n", kApSsid,
                  current ? " — 10 phút không lưu sẽ khởi động lại" : "");

    const uint32_t start = millis();
    for (;;) {
        s_dns->processNextRequest();
        s_server->handleClient();
        schedulerTick();

        if (s_restartPending && (int32_t)(millis() - s_restartAt) >= 0) {
            ESP.restart();
        }
        if (current && !s_restartPending && millis() - start >= kIdleTimeoutMs) {
            Serial.println("[portal] 10 phút không lưu, khởi động lại với cấu hình cũ");
            delay(100);
            ESP.restart();
        }
        delay(2);
    }
}

}  // namespace portal
