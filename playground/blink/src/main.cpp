#include <Arduino.h>

// ESP32-C3 Super Mini: LED onboard ở GPIO 8, active-LOW (LOW = sáng)
constexpr uint8_t LED_PIN = 8;
constexpr uint32_t BLINK_INTERVAL_MS = 1000;

uint32_t lastToggleMs = 0;
bool ledOn = false;
int blinkCount = 0;

void setup() {
  Serial.begin(115200);
  pinMode(LED_PIN, OUTPUT);
  digitalWrite(LED_PIN, HIGH);  // tắt LED lúc khởi động
  Serial.println("Ner0bit firmware khoi dong");
}

void loop() {
  uint32_t now = millis();
  if (now - lastToggleMs >= BLINK_INTERVAL_MS) {
    lastToggleMs = now;
    ledOn = !ledOn;
    digitalWrite(LED_PIN, ledOn ? LOW : HIGH);  // đảo do active-LOW
    blinkCount++;
    Serial.printf("Blink #%d - LED %s\n", blinkCount, ledOn ? "ON" : "OFF");
  }
}
