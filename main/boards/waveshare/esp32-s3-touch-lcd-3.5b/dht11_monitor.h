#pragma once

#include <driver/gpio.h>

#include <cstdint>

struct Dht11Reading {
    float temperature = 0;
    float humidity = 0;
    int64_t last_success_us = 0;
    bool valid = false;
};

// Returns a synchronized copy; the display never waits for sensor I/O.
Dht11Reading GetDht11Reading();

// Starts one board-lifetime task. The selected pin must not be used by an SD card.
void StartDht11Monitor(gpio_num_t pin);
