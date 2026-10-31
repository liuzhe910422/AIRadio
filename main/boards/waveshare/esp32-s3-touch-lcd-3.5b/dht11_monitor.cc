#include "dht11_monitor.h"

#include <driver/rmt_rx.h>
#include <esp_log.h>
#include <esp_timer.h>
#include <freertos/FreeRTOS.h>
#include <freertos/semphr.h>
#include <freertos/task.h>

#include <array>
#include <cstdint>
#include <mutex>

namespace {
constexpr char kTag[] = "DHT11";
std::mutex reading_mutex;
Dht11Reading latest_reading;

// Owns the receive buffer and peripheral for the lifetime of the monitoring task.
class Dht11 {
public:
    explicit Dht11(gpio_num_t pin) : pin_(pin) {}
    ~Dht11() {
        if (channel_) {
            if (enabled_) {
                rmt_disable(channel_);
            }
            rmt_del_channel(channel_);
        }
        if (done_) {
            vSemaphoreDelete(done_);
        }
        gpio_reset_pin(pin_);
    }

    esp_err_t Init() {
        done_ = xSemaphoreCreateBinary();
        if (!done_) {
            return ESP_ERR_NO_MEM;
        }
        rmt_rx_channel_config_t config = {};
        config.gpio_num = pin_;
        config.clk_src = RMT_CLK_SRC_DEFAULT;
        config.resolution_hz = 1000000;
        config.mem_block_symbols = 64;
        esp_err_t err = rmt_new_rx_channel(&config, &channel_);
        if (err != ESP_OK) {
            return err;
        }
        // Keep input enabled for RMT while driving only low or releasing the bus.
        gpio_config_t gpio = {};
        gpio.pin_bit_mask = 1ULL << pin_;
        gpio.mode = GPIO_MODE_INPUT_OUTPUT_OD;
        gpio.pull_up_en = GPIO_PULLUP_ENABLE;
        err = gpio_set_level(pin_, 1);
        if (err == ESP_OK) {
            err = gpio_config(&gpio);
        }
        if (err != ESP_OK) {
            return err;
        }
        rmt_rx_event_callbacks_t callbacks = {};
        callbacks.on_recv_done = OnReceive;
        return rmt_rx_register_event_callbacks(channel_, &callbacks, this);
    }

    esp_err_t Read(float& temperature, float& humidity) {
        esp_err_t err = rmt_enable(channel_);
        if (err != ESP_OK) {
            return err;
        }
        enabled_ = true;
        xSemaphoreTake(done_, 0);
        symbol_count_ = 0;
        gpio_set_level(pin_, 0);
        // Round up so the start pulse is always longer than 18 ms.
        vTaskDelay(pdMS_TO_TICKS(20) + 1);
        rmt_receive_config_t config = {};
        config.signal_range_min_ns = 1000;
        config.signal_range_max_ns = 1000000;
        // Arm before releasing DATA: even an immediate sensor response is captured.
        err = rmt_receive(channel_, symbols_.data(), sizeof(symbols_), &config);
        gpio_set_level(pin_, 1);
        if (err == ESP_OK && xSemaphoreTake(done_, pdMS_TO_TICKS(100) + 1) != pdTRUE) {
            err = ESP_ERR_TIMEOUT;
        }
        // Stop reception before touching the buffer, including on timeout.
        esp_err_t stop_err = rmt_disable(channel_);
        if (stop_err == ESP_OK) {
            enabled_ = false;
        }
        if (err != ESP_OK) {
            return err;
        }
        if (stop_err != ESP_OK) {
            return stop_err;
        }
        return Decode(temperature, humidity);
    }

private:
    static bool OnReceive(rmt_channel_handle_t, const rmt_rx_done_event_data_t* event,
                          void* context) {
        auto* self = static_cast<Dht11*>(context);
        self->symbol_count_ = event->num_symbols;
        BaseType_t woken = pdFALSE;
        xSemaphoreGiveFromISR(self->done_, &woken);
        return woken == pdTRUE;
    }

    esp_err_t Decode(float& temperature, float& humidity) const {
        struct Pulse {
            uint16_t duration;
            bool high;
        };
        std::array<Pulse, 128> pulses = {};
        size_t count = 0;
        for (size_t i = 0; i < symbol_count_ && i < symbols_.size(); ++i) {
            const auto& s = symbols_[i];
            if (s.duration0) {
                pulses[count++] = {s.duration0, static_cast<bool>(s.level0)};
            }
            if (s.duration1) {
                pulses[count++] = {s.duration1, static_cast<bool>(s.level1)};
            }
        }
        // Skip the host's release pulse and find the sensor's 80 us low/high response.
        size_t start = 0;
        for (; start + 1 < count; ++start) {
            const auto& low = pulses[start];
            const auto& high = pulses[start + 1];
            if (!low.high && high.high && low.duration >= 60 && low.duration <= 100 &&
                high.duration >= 60 && high.duration <= 100) {
                break;
            }
        }
        if (start + 1 >= count) {
            return ESP_ERR_NOT_FOUND;
        }
        start += 2;
        if (count - start < 80) {
            return ESP_ERR_INVALID_SIZE;
        }
        std::array<uint8_t, 5> bytes = {};
        for (size_t bit = 0; bit < 40; ++bit) {
            const auto& low = pulses[start + bit * 2];
            const auto& high = pulses[start + bit * 2 + 1];
            if (low.high || !high.high || low.duration < 35 || low.duration > 70 ||
                !((high.duration >= 15 && high.duration <= 40) ||
                  (high.duration >= 55 && high.duration <= 90))) {
                ESP_LOGW(kTag, "Invalid bit %u: low=%u us (level=%u), high=%u us (level=%u)",
                         static_cast<unsigned>(bit), low.duration, low.high, high.duration,
                         high.high);
                return ESP_ERR_INVALID_RESPONSE;
            }
            bytes[bit / 8] = (bytes[bit / 8] << 1) | (high.duration >= 55);
        }
        if (static_cast<uint8_t>(bytes[0] + bytes[1] + bytes[2] + bytes[3]) != bytes[4]) {
            return ESP_ERR_INVALID_CRC;
        }
        // Older DHT11s reserve decimal bytes as zero; newer modules can report tenths.
        // Accept both formats only after checksum validation, without discarding decimals.
        const float decoded_humidity = bytes[0] + bytes[1] * 0.1f;
        const float decoded_temperature = bytes[2] + bytes[3] * 0.1f;
        if (bytes[1] > 9 || bytes[3] > 9 || decoded_humidity > 100.0f ||
            decoded_temperature > 50.0f) {
            ESP_LOGW(kTag, "Invalid data: raw=%02x %02x %02x %02x %02x", bytes[0], bytes[1],
                     bytes[2], bytes[3], bytes[4]);
            return ESP_ERR_INVALID_RESPONSE;
        }
        humidity = decoded_humidity;
        temperature = decoded_temperature;
        return ESP_OK;
    }

    gpio_num_t pin_;
    rmt_channel_handle_t channel_ = nullptr;
    SemaphoreHandle_t done_ = nullptr;
    bool enabled_ = false;
    size_t symbol_count_ = 0;
    std::array<rmt_symbol_word_t, 64> symbols_ = {};
};

void MonitorTask(void* context) {
    const auto pin = static_cast<gpio_num_t>(reinterpret_cast<intptr_t>(context));
    {
        Dht11 sensor(pin);
        esp_err_t err = sensor.Init();
        if (err == ESP_OK) {
            ESP_LOGI(kTag, "Monitoring GPIO%d (SD card must remain removed)", pin);
            vTaskDelay(pdMS_TO_TICKS(1000) + 1);
            while (true) {
                float temperature = 0;
                float humidity = 0;
                err = sensor.Read(temperature, humidity);
                if (err == ESP_OK) {
                    {
                        std::lock_guard<std::mutex> lock(reading_mutex);
                        latest_reading = {temperature, humidity, esp_timer_get_time(), true};
                    }
                    ESP_LOGI(kTag, "temperature=%.1f C, humidity=%.1f %%RH", temperature, humidity);
                } else {
                    ESP_LOGW(kTag, "Read failed: %s", esp_err_to_name(err));
                }
                // No immediate retries or catch-up bursts, even after a failed read.
                vTaskDelay(pdMS_TO_TICKS(5000) + 1);
            }
        }
        ESP_LOGE(kTag, "Initialization failed: %s", esp_err_to_name(err));
    }
    vTaskDelete(nullptr);
}
}  // namespace

Dht11Reading GetDht11Reading() {
    std::lock_guard<std::mutex> lock(reading_mutex);
    return latest_reading;
}

void StartDht11Monitor(gpio_num_t pin) {
    if (xTaskCreate(MonitorTask, "dht11", 4096, reinterpret_cast<void*>(static_cast<intptr_t>(pin)),
                    1, nullptr) != pdPASS) {
        ESP_LOGE(kTag, "Could not create monitoring task");
    }
}
