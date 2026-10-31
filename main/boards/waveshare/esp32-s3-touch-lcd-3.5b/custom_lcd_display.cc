
#include "custom_lcd_display.h"
#include "dht11_monitor.h"
#include "application.h"
#include "assets/lang_config.h"
#include "board.h"
#include "config.h"
#include "lcd_display.h"
#include "settings.h"
#include "lvgl_image.h"
#include "jpg/jpeg_to_image.h"

#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

#include <esp_err.h>
#include <esp_lcd_panel_io.h>
#include <esp_log.h>
#include <esp_lvgl_port.h>
#include <freertos/FreeRTOS.h>
#include <freertos/semphr.h>
#include <freertos/task.h>
#include <esp_heap_caps.h>
#include <esp_timer.h>

#define TAG "CustomLcdDisplay"

static SemaphoreHandle_t trans_done_sem = NULL;
static uint16_t *trans_act;
static uint16_t *trans_buf_1;
static uint16_t *trans_buf_2;

struct IntercomImageLink {
  CustomLcdDisplay *display;
  std::string url;
};

static bool English(const Intercom &intercom) { return intercom.Language() == "en-US"; }
static const char *StatusText(const Intercom &intercom) {
  if (!English(intercom)) return intercom.Status();
  const auto status = std::string(intercom.Status());
  if (status == "托管模式") return "Hosted mode";
  if (status == "对讲未配置") return "Intercom not configured";
  if (status == "对讲未连接") return "Intercom disconnected";
  if (status == "对讲连接中") return "Connecting intercom";
  if (status == "开始对讲") return "Start intercom";
  if (status == "说话中") return "Speaking";
  if (status == "语音回放中") return "Playing audio";
  return "Intercom unavailable";
}

void CustomLcdDisplay::SetupUI() {
  LcdDisplay::SetupUI();
  if (!Lock(1000))
    return;
  // Own a separate transcript panel: the base display's content may be a
  // subtitle label rather than a scrollable message container.
  auto screen = lv_screen_active();
  auto panel = lv_obj_create(screen);
  lv_obj_set_size(panel, 300, 320);
  lv_obj_align(panel, LV_ALIGN_TOP_RIGHT, 0, 0);
  lv_obj_set_style_pad_all(panel, 8, 0);
  lv_obj_set_style_border_width(panel, 1, 0);
  lv_obj_set_style_bg_color(panel, lv_color_hex(0xF4F7F6), 0);
  lv_obj_set_style_text_color(panel, lv_color_hex(0x17332D), 0);
  lv_obj_remove_flag(panel, LV_OBJ_FLAG_SCROLLABLE);
  auto title = lv_label_create(panel);
  chat_title_ = title;
  lv_label_set_text(title, English(Application::GetInstance().GetIntercom()) ? "History" : "对讲记录");
  lv_obj_set_width(title, 80);
  lv_label_set_long_mode(title, LV_LABEL_LONG_CLIP);
  lv_obj_align(title, LV_ALIGN_TOP_LEFT, 0, 0);
  environment_label_ = lv_label_create(panel);
  lv_obj_set_width(environment_label_, 198);
  // Inherit the same text font and Chinese fallback as the history heading.
  lv_obj_set_style_text_align(environment_label_, LV_TEXT_ALIGN_RIGHT, 0);
  lv_label_set_long_mode(environment_label_, LV_LABEL_LONG_CLIP);
  lv_obj_align(environment_label_, LV_ALIGN_TOP_RIGHT, 0, 0);
  environment_start_us_ = esp_timer_get_time();
  UpdateEnvironment();
  chat_list_ = lv_obj_create(panel);
  lv_obj_set_size(chat_list_, 282, 270);
  lv_obj_align(chat_list_, LV_ALIGN_BOTTOM_MID, 0, 0);
  lv_obj_set_style_pad_all(chat_list_, 4, 0);
  lv_obj_set_style_border_width(chat_list_, 0, 0);
  lv_obj_set_flex_flow(chat_list_, LV_FLEX_FLOW_COLUMN);
  lv_obj_set_scroll_dir(chat_list_, LV_DIR_VER);
  lv_obj_set_scrollbar_mode(chat_list_, LV_SCROLLBAR_MODE_AUTO);
  intercom_bar_ = lv_obj_create(lv_screen_active());
  lv_obj_set_size(intercom_bar_, 180, 320);
  lv_obj_align(intercom_bar_, LV_ALIGN_LEFT_MID, 0, 0);
  lv_obj_set_style_radius(intercom_bar_, 0, 0);
  lv_obj_set_style_border_width(intercom_bar_, 0, 0);
  lv_obj_set_style_bg_color(intercom_bar_, lv_color_hex(0xF4F7F6), 0);
  lv_obj_set_style_pad_all(intercom_bar_, 8, 0);
  device_status_label_ = lv_label_create(intercom_bar_);
  lv_label_set_text(device_status_label_, English(Application::GetInstance().GetIntercom()) ? "Device: not configured" : "设备：未配置");
  lv_obj_set_width(device_status_label_, 164);
  lv_label_set_long_mode(device_status_label_, LV_LABEL_LONG_WRAP);
  lv_obj_set_height(device_status_label_, 54);
  lv_obj_align(device_status_label_, LV_ALIGN_TOP_LEFT, 0, 4);
  intercom_status_label_ = lv_label_create(intercom_bar_);
  lv_label_set_text(intercom_status_label_, English(Application::GetInstance().GetIntercom()) ? "Intercom status\nRoom: default" : "对讲状态\n房间：默认房间");
  lv_obj_set_width(intercom_status_label_, 164);
  lv_obj_align(intercom_status_label_, LV_ALIGN_TOP_LEFT, 0, 68);
  intercom_button_ = lv_button_create(intercom_bar_);
  lv_obj_set_size(intercom_button_, 145, 82);
  lv_obj_align(intercom_button_, LV_ALIGN_CENTER, 0, 32);
  intercom_label_ = lv_label_create(intercom_button_);
  lv_obj_center(intercom_label_);
  lv_obj_add_event_cb(
      intercom_button_,
      [](lv_event_t *) { ESP_LOGI(TAG, "Intercom button pressed"); },
      LV_EVENT_PRESSED, nullptr);
  lv_obj_add_event_cb(
      intercom_button_,
      [](lv_event_t *) {
        ESP_LOGI(TAG, "Intercom button clicked");
        auto &app = Application::GetInstance();
        app.Schedule([&app]() { app.GetIntercom().Press(); });
      },
      LV_EVENT_CLICKED, nullptr);
  // Ending is done by pressing the main talk button again. Keep the
  // cancellation control available to the application but hide it from
  // this simplified touch UI.
  intercom_cancel_ = nullptr;
  Unlock();
  UpdateIntercom();
}

void CustomLcdDisplay::UpdateIntercom() {
  if (!intercom_label_ || !Lock(100))
    return;
  auto &intercom = Application::GetInstance().GetIntercom();
  if (chat_title_) lv_label_set_text(chat_title_, English(intercom) ? "History" : "对讲记录");
  UpdateEnvironment();
  lv_label_set_text(intercom_label_, StatusText(intercom));
  lv_obj_set_style_bg_color(
      intercom_button_, lv_color_hex(intercom.IsHosted() ? 0xE6B83D : 0x3187C8),
      0);
  lv_obj_set_style_text_color(
      intercom_button_,
      intercom.IsHosted() ? lv_color_hex(0x302600) : lv_color_white(), 0);
  if (intercom_status_label_) {
    const auto speaker = intercom.SpeakingDeviceId();
    const std::string status =
        intercom.AuthError()
            ? (English(intercom) ? "API key error" : "API Key错误")
            : (speaker.empty() ? StatusText(intercom) : (English(intercom) ? "Speaking: " + speaker : "说话中：" + speaker));
    std::string text =
        (English(intercom) ? "Intercom status\nRoom: " : "对讲状态\n房间：") + std::string(intercom.RoomName()) + "\n" + status;
    lv_label_set_text(intercom_status_label_, text.c_str());
  }
  if (device_status_label_) {
    const char *device_id = intercom.DeviceId();
    lv_label_set_text_fmt(device_status_label_, English(intercom) ? "Device: %s\nPending events: %d" : "设备：%s\n待处理事件：%d条",
                          device_id[0] ? device_id : "未配置",
                          intercom.PendingEventCount());
  }
  if (intercom.IsHosted() || intercom.CanRecord() || intercom.IsRecording())
    lv_obj_remove_state(intercom_button_, LV_STATE_DISABLED);
  else
    lv_obj_add_state(intercom_button_, LV_STATE_DISABLED);
  UpdateChatRecords();
  UpdateRoomPicker();
  UpdateEventPicker();
  Unlock();
}

// Called only while the LVGL lock is held, including during initial UI setup.
void CustomLcdDisplay::UpdateEnvironment() {
  if (!environment_label_) return;
  const bool english = English(Application::GetInstance().GetIntercom());
  const auto reading = GetDht11Reading();
  const int64_t reference = reading.valid ? reading.last_success_us : environment_start_us_;
  const bool stale = esp_timer_get_time() - reference >= 15000000;
  if (stale) {
    lv_label_set_text(environment_label_, english ? "Temp/RH: read error" : "温湿度：读取异常");
  } else if (!reading.valid) {
    lv_label_set_text(environment_label_, english ? "Temp/RH: --°C / --%" : "温湿度：--°C / --%");
  } else {
    // LVGL's built-in formatter has float support disabled in this firmware.
    char text[96];
    snprintf(text, sizeof(text), english ? "Temp/RH: %.1f°C / %.1f%%" : "温湿度：%.1f°C / %.1f%%",
             static_cast<double>(reading.temperature), static_cast<double>(reading.humidity));
    lv_label_set_text(environment_label_, text);
  }
  lv_obj_set_style_text_color(environment_label_,
                              lv_color_hex(stale ? 0xB54708 : 0x17332D), 0);
}

void CustomLcdDisplay::UpdateEventPicker() {
  auto &intercom = Application::GetInstance().GetIntercom();
  const bool english = English(intercom);
  if (!intercom.EventPickerVisible()) {
    if (event_picker_)
      lv_obj_delete(event_picker_);
    event_picker_ = event_picker_title_ = event_picker_list_ = nullptr;
    return;
  }
  if (event_picker_ && event_picker_revision_ == intercom.EventPickerRevision()) {
    if (event_picker_title_)
      lv_label_set_text_fmt(event_picker_title_, english ? "Pending events · %ds" : "待处理事件 · %d秒", intercom.EventPickerSeconds());
    return;
  }
  if (event_picker_)
    lv_obj_delete(event_picker_);
  event_picker_revision_ = intercom.EventPickerRevision();
  event_picker_ = lv_obj_create(lv_screen_active());
  lv_obj_set_size(event_picker_, LV_PCT(100), LV_PCT(100));
  lv_obj_set_style_bg_color(event_picker_, lv_color_black(), 0);
  lv_obj_set_style_bg_opa(event_picker_, LV_OPA_50, 0);
  auto panel = lv_obj_create(event_picker_);
  lv_obj_set_size(panel, 420, 280);
  lv_obj_center(panel);
  lv_obj_set_style_pad_all(panel, 12, 0);
  lv_obj_set_style_bg_color(panel, lv_color_hex(0xF4F7F6), 0);
  auto title = lv_label_create(panel);
  event_picker_title_ = title;
  lv_label_set_text_fmt(title, english ? "Pending events · %ds" : "待处理事件 · %d秒", intercom.EventPickerSeconds());
  lv_obj_align(title, LV_ALIGN_TOP_LEFT, 0, 8);
  auto close = lv_button_create(panel);
  lv_obj_set_size(close, 48, 40);
  lv_obj_align(close, LV_ALIGN_TOP_RIGHT, 0, 0);
  auto cl = lv_label_create(close);
  lv_label_set_text(cl, "X");
  lv_obj_center(cl);
  lv_obj_add_event_cb(close, [](lv_event_t *) {
    auto &app = Application::GetInstance();
    app.Schedule([&app]() { app.GetIntercom().CloseEventPicker(); });
  }, LV_EVENT_CLICKED, nullptr);
  event_picker_list_ = lv_obj_create(panel);
  lv_obj_set_size(event_picker_list_, 388, 200);
  lv_obj_align(event_picker_list_, LV_ALIGN_BOTTOM_MID, 0, 0);
  lv_obj_set_flex_flow(event_picker_list_, LV_FLEX_FLOW_COLUMN);
  lv_obj_set_scroll_dir(event_picker_list_, LV_DIR_VER);
  int index = 1;
  for (const auto &event : intercom.EventOptions()) {
    auto button = lv_button_create(event_picker_list_);
    lv_obj_set_width(button, LV_PCT(100));
    auto label = lv_label_create(button);
    lv_label_set_text_fmt(label, "%d. %s\n%s", index++, event.content.c_str(),
                          event.scheduled_at.c_str());
    lv_obj_set_width(label, LV_PCT(100));
    lv_label_set_long_mode(label, LV_LABEL_LONG_WRAP);
  }
  lv_label_set_text_fmt(title, english ? "Pending events · %ds" : "待处理事件 · %d秒", intercom.EventPickerSeconds());
}

void CustomLcdDisplay::UpdateRoomPicker() {
  auto &intercom = Application::GetInstance().GetIntercom();
  const bool english = English(intercom);
  if (!intercom.RoomPickerVisible()) {
    if (room_picker_)
      lv_obj_delete(room_picker_);
    room_picker_ = room_picker_title_ = room_picker_list_ = nullptr;
    return;
  }
  if (!room_picker_ || room_picker_revision_ != intercom.RoomPickerRevision()) {
    if (room_picker_)
      lv_obj_delete(room_picker_);
    room_picker_revision_ = intercom.RoomPickerRevision();
    room_picker_ = lv_obj_create(lv_screen_active());
    lv_obj_set_size(room_picker_, LV_PCT(100), LV_PCT(100));
    lv_obj_set_style_bg_color(room_picker_, lv_color_black(), 0);
    lv_obj_set_style_bg_opa(room_picker_, LV_OPA_50, 0);
    lv_obj_set_style_border_width(room_picker_, 0, 0);
    lv_obj_set_style_pad_all(room_picker_, 0, 0);
    lv_obj_set_style_radius(room_picker_, 0, 0);
    lv_obj_remove_flag(room_picker_, LV_OBJ_FLAG_SCROLLABLE);
    auto panel = lv_obj_create(room_picker_);
    lv_obj_set_size(panel, 420, 280);
    lv_obj_set_style_pad_all(panel, 12, 0);
    lv_obj_center(panel);
    lv_obj_set_style_bg_color(panel, lv_color_hex(0xF4F7F6), 0);
    lv_obj_set_style_text_color(panel, lv_color_hex(0x17332D), 0);
    lv_obj_remove_flag(panel, LV_OBJ_FLAG_SCROLLABLE);
    room_picker_title_ = lv_label_create(panel);
    lv_obj_set_width(room_picker_title_, 300);
    lv_label_set_long_mode(room_picker_title_, LV_LABEL_LONG_DOT);
    lv_obj_align(room_picker_title_, LV_ALIGN_TOP_LEFT, 0, 8);
    auto close = lv_button_create(panel);
    lv_obj_set_size(close, 48, 40);
    lv_obj_align(close, LV_ALIGN_TOP_RIGHT, 0, 0);
    auto close_label = lv_label_create(close);
    lv_label_set_text(close_label, "X");
    lv_obj_center(close_label);
    lv_obj_add_event_cb(
        close,
        [](lv_event_t *) {
          auto &app = Application::GetInstance();
          app.Schedule([&app]() { app.GetIntercom().CloseRoomPicker(); });
        },
        LV_EVENT_CLICKED, nullptr);
    room_picker_list_ = lv_obj_create(panel);
    lv_obj_set_size(room_picker_list_, 388, 200);
    lv_obj_align(room_picker_list_, LV_ALIGN_BOTTOM_MID, 0, 0);
    lv_obj_set_flex_flow(room_picker_list_, LV_FLEX_FLOW_COLUMN);
    lv_obj_set_scroll_dir(room_picker_list_, LV_DIR_VER);
    for (const auto &room : intercom.RoomOptions()) {
      auto button = lv_button_create(room_picker_list_);
      lv_obj_set_size(button, LV_PCT(100), LV_SIZE_CONTENT);
      lv_obj_set_style_min_height(button, 44, 0);
      lv_obj_set_style_bg_color(
          button, lv_color_hex(room.current ? 0xCDEAD9 : 0xDFEAF5), 0);
      lv_obj_set_style_text_color(button, lv_color_hex(0x17332D), 0);
      auto label = lv_label_create(button);
      lv_obj_set_width(label, LV_PCT(100));
      lv_label_set_long_mode(label, LV_LABEL_LONG_WRAP);
      const std::string text = room.code.empty() ? room.name : room.name + (english ? " (code: " : "（加入码：") + room.code + (english ? ")" : "）");
      lv_label_set_text(label, text.c_str());
      auto id = new std::string(room.id);
      lv_obj_add_event_cb(
          button,
          [](lv_event_t *event) {
            auto id = static_cast<std::string *>(lv_event_get_user_data(event));
            if (lv_event_get_code(event) == LV_EVENT_DELETE) {
              delete id;
            } else if (lv_event_get_code(event) == LV_EVENT_CLICKED) {
              auto &app = Application::GetInstance();
              app.Schedule(
                  [id = *id, &app]() { app.GetIntercom().SelectRoom(id); });
            }
          },
          LV_EVENT_ALL, id);
    }
  }
  if (intercom.RoomPickerPending()) {
    lv_label_set_text_fmt(room_picker_title_, english ? "Switching… %ds" : "正在切换… %d秒",
                          intercom.RoomPickerSeconds());
  } else if (!intercom.RoomPickerError().empty()) {
    lv_label_set_text_fmt(room_picker_title_, english ? "%s · %ds" : "%s · %d秒",
                          intercom.RoomPickerError().c_str(),
                          intercom.RoomPickerSeconds());
  } else {
    lv_label_set_text_fmt(room_picker_title_, english ? "Select room · %ds" : "选择房间 · %d秒",
                          intercom.RoomPickerSeconds());
  }
  const auto count = lv_obj_get_child_count(room_picker_list_);
  for (uint32_t i = 0; i < count; ++i) {
    auto button = lv_obj_get_child(room_picker_list_, i);
    if (intercom.RoomPickerPending())
      lv_obj_add_state(button, LV_STATE_DISABLED);
    else
      lv_obj_remove_state(button, LV_STATE_DISABLED);
  }
}

void CustomLcdDisplay::SetChatMessage(const char *role, const char *content) {
  // Keep assistant replies in the left control rail on this board; the
  // right column remains reserved for the future intercom timeline.
  if (intercom_bar_ && role && strcmp(role, "user") == 0)
    return;
  if (!intercom_bar_ || !role || strcmp(role, "assistant") != 0) {
    LcdDisplay::SetChatMessage(role, content);
    return;
  }
  if (!Lock(100))
    return;
  if (!assistant_message_label_) {
    assistant_message_label_ = lv_label_create(intercom_bar_);
    lv_obj_set_width(assistant_message_label_, 164);
    lv_label_set_long_mode(assistant_message_label_, LV_LABEL_LONG_WRAP);
    // Keep the assistant reply below the large talk button so it never
    // overlaps the primary touch target.
    lv_obj_align(assistant_message_label_, LV_ALIGN_BOTTOM_LEFT, 0, -4);
  }
  lv_label_set_text(assistant_message_label_, content ? content : "");
  Unlock();
}

void CustomLcdDisplay::ShowIntercomImage(const std::string &url, bool popup) {
  if (url.empty()) return;
  struct Job { CustomLcdDisplay *display; std::string url; bool popup; };
  auto *job = new Job{this, url, popup};
  if (xTaskCreate([](void *arg) {
        auto *job = static_cast<Job *>(arg);
        auto http = Board::GetInstance().GetNetwork()->CreateHttp(4);
        std::string source;
        if (http && http->Open("GET", job->url)) source = http->ReadAll();
        if (http) http->Close();
        uint8_t *data = nullptr; size_t len = 0, w = 0, h = 0, stride = 0;
        bool ok = !source.empty() && jpeg_to_image(reinterpret_cast<const uint8_t *>(source.data()), source.size(), &data, &len, &w, &h, &stride) == ESP_OK;
        if (ok && job->display->Lock(1000)) {
          auto host = job->popup ? lv_obj_create(lv_screen_active()) : job->display->chat_list_;
          if (job->popup) {
            lv_obj_set_size(host, 472, 312); lv_obj_center(host);
          }
          auto image = lv_image_create(host);
          lv_obj_add_flag(image, LV_OBJ_FLAG_CLICKABLE);
          auto *img = new LvglAllocatedImage(data, len, w, h, stride, LV_COLOR_FORMAT_RGB565);
          lv_image_set_src(image, img->image_dsc()); lv_obj_center(image);
          if (!job->popup) { lv_obj_set_width(image, 120); lv_obj_set_height(image, 90); }
          lv_obj_add_event_cb(image, [](lv_event_t *e) { if (lv_event_get_code(e) == LV_EVENT_DELETE) delete static_cast<LvglImage *>(lv_event_get_user_data(e)); }, LV_EVENT_DELETE, img);
          if (!job->popup) {
            auto *link = new IntercomImageLink{job->display, job->url};
            lv_obj_add_event_cb(image, [](lv_event_t *e) {
              auto *link = static_cast<IntercomImageLink *>(lv_event_get_user_data(e));
              if (lv_event_get_code(e) == LV_EVENT_CLICKED && link)
                link->display->ShowIntercomImage(link->url, true);
              if (lv_event_get_code(e) == LV_EVENT_DELETE) delete link;
            }, LV_EVENT_ALL, link);
          }
          if (job->popup) {
            auto close = lv_button_create(host);
            lv_obj_set_size(close, 56, 44);
            lv_obj_align(close, LV_ALIGN_TOP_RIGHT, -4, 4);
            auto close_label = lv_label_create(close); lv_label_set_text(close_label, "X"); lv_obj_center(close_label);
            lv_obj_add_event_cb(close, [](lv_event_t *e) {
              if (lv_event_get_code(e) == LV_EVENT_CLICKED) {
                auto popup = static_cast<lv_obj_t *>(lv_event_get_user_data(e));
                if (popup) lv_obj_del(popup);
              }
            }, LV_EVENT_CLICKED, host);
            lv_obj_move_foreground(host);
            lv_obj_move_foreground(close);
          }
          job->display->Unlock();
        } else if (data) heap_caps_free(data);
        delete job; vTaskDelete(nullptr);
      }, "image_dl", 8192, job, 2, nullptr) != pdPASS) delete job;
}

void CustomLcdDisplay::UpdateChatRecords() {
  if (!chat_list_)
    return;
  auto &intercom = Application::GetInstance().GetIntercom();
  if (chat_revision_ == intercom.ChatRevision())
    return;
  chat_revision_ = intercom.ChatRevision();
  const auto old_scroll = lv_obj_get_scroll_y(chat_list_);
  const bool at_bottom = lv_obj_get_scroll_bottom(chat_list_) < 24;
  lv_obj_clean(chat_list_);
  const auto &messages = intercom.ChatMessages();
  if (messages.empty()) {
    auto empty = lv_label_create(chat_list_);
      lv_label_set_text(empty, English(intercom) ? "No intercom messages" : "暂无对讲记录");
  }
  for (const auto &message : messages) {
    auto button = lv_button_create(chat_list_);
    lv_obj_set_width(button, LV_PCT(100));
    lv_obj_set_height(button, LV_SIZE_CONTENT);
    lv_obj_set_style_pad_all(button, 8, 0);
    const bool playing = intercom.ReplayMessage() == message.id;
    uint32_t bubble_color = 0xDFEAF5;
    if (playing) {
      bubble_color = 0xFFD690;
    } else if (message.event_notification) {
      bubble_color = message.event_terminal ? 0xCDEAD9 : 0xF2B6B2;
    } else if (message.system) {
      bubble_color = 0xEEE2C9;
    } else if (message.own) {
      bubble_color = 0xCDEAD9;
    }
    lv_obj_set_style_bg_color(button, lv_color_hex(bubble_color), 0);
    lv_obj_set_style_text_color(button, lv_color_hex(0x17332D), 0);
    auto label = lv_label_create(button);
    lv_obj_set_width(label, LV_PCT(100));
    lv_label_set_long_mode(label, LV_LABEL_LONG_WRAP);
    const bool english = English(intercom);
    const std::string sender = message.event_notification ? (english ? "Event" : "事件通知") : (message.system ? (english ? "Room notice" : "房间通知") : (message.own ? (english ? "Me" : "我") : message.source));
    if (message.image) {
      lv_label_set_text(label, (sender + (english ? " · Received an image" : " · 收到一张图片")).c_str());
      ShowIntercomImage(message.image_url, false);
      auto *message_link = new IntercomImageLink{this, message.image_url};
      lv_obj_add_event_cb(button, [](lv_event_t *e) {
        auto *link = static_cast<IntercomImageLink *>(lv_event_get_user_data(e));
        if (lv_event_get_code(e) == LV_EVENT_CLICKED && link)
          link->display->ShowIntercomImage(link->url, true);
        if (lv_event_get_code(e) == LV_EVENT_DELETE) delete link;
      }, LV_EVENT_ALL, message_link);
      continue;
    }
    std::string text = sender +
        " · " +
        (message.automated ? (english ? std::string("Hosted reply · ") : std::string("托管回复 · ")) : std::string()) +
        std::to_string((message.duration_ms + 999) / 1000) + (english ? "s" : "秒") +
        (playing ? (english ? " · Stop" : " · 点击停止") : (english ? " · Play" : " · 点击播放")) + "\n";
    if (message.audio_kind == "translation") text += english ? "Translation\n" : "翻译\n";
    text += !message.text.empty()
                ? message.text
                : (message.status == "pending" ? (english ? "Recognizing…" : "正在识别…") : (english ? "Voice message" : "语音消息"));
    lv_label_set_text(label, text.c_str());
    auto id = new std::string(message.id);
    lv_obj_add_event_cb(
        button,
        [](lv_event_t *event) {
          auto id = static_cast<std::string *>(lv_event_get_user_data(event));
          if (lv_event_get_code(event) == LV_EVENT_DELETE) {
            delete id;
            return;
          }
          if (lv_event_get_code(event) == LV_EVENT_CLICKED) {
            auto &app = Application::GetInstance();
            app.Schedule(
                [id = *id, &app]() { app.GetIntercom().PlayChatMessage(id); });
          }
        },
        LV_EVENT_ALL, id);
    if (!message.available)
      lv_obj_add_state(button, LV_STATE_DISABLED);
  }
  lv_obj_update_layout(chat_list_);
  if (at_bottom)
    lv_obj_scroll_to_y(chat_list_, LV_COORD_MAX, LV_ANIM_OFF);
  else
    lv_obj_scroll_to_y(chat_list_, old_scroll, LV_ANIM_OFF);
}

bool CustomLcdDisplay::lvgl_port_flush_io_ready_callback(
    esp_lcd_panel_io_handle_t panel_io, esp_lcd_panel_io_event_data_t *edata,
    void *user_ctx) {
  BaseType_t taskAwake = pdFALSE;
  lv_display_t *disp_drv = (lv_display_t *)user_ctx;
  assert(disp_drv != NULL);
  if (trans_done_sem) {
    xSemaphoreGiveFromISR(trans_done_sem, &taskAwake);
  }
  return false;
}

void CustomLcdDisplay::lvgl_port_flush_callback(lv_display_t *drv,
                                                const lv_area_t *area,
                                                uint8_t *color_map) {
  assert(drv != NULL);
  esp_lcd_panel_handle_t panel_handle =
      (esp_lcd_panel_handle_t)lv_display_get_driver_data(drv);
  assert(panel_handle != NULL);

  size_t len = lv_area_get_size(area);
  lv_draw_sw_rgb565_swap(color_map, len);

  const int x_start = area->x1;
  const int x_end = area->x2;
  const int y_start = area->y1;
  const int y_end = area->y2;
  const int width = x_end - x_start + 1;
  const int height = y_end - y_start + 1;

  int32_t hor_res = lv_display_get_horizontal_resolution(drv);
  int32_t ver_res = lv_display_get_vertical_resolution(drv);

  // printf("hor_res: %ld, ver_res: %ld\r\n", hor_res, ver_res);
  // printf("x_start: %d, x_end: %d, y_start: %d, y_end: %d, width: %d, height:
  // %d\r\n", x_start, x_end, y_start, y_end, width, height);
  uint16_t *from = (uint16_t *)color_map;
  uint16_t *to = NULL;

  if (DISPLAY_TRANS_SIZE > 0) {
    assert(trans_buf_1 != NULL);

    int x_draw_start = 0;
    int x_draw_end = 0;
    int y_draw_start = 0;
    int y_draw_end = 0;
    int trans_count = 0;

    trans_act = trans_buf_1;
    lv_display_rotation_t rotate = LV_DISPLAY_ROTATION;

    int x_start_tmp = 0;
    int x_end_tmp = 0;
    int max_width = 0;
    int trans_width = 0;

    int y_start_tmp = 0;
    int y_end_tmp = 0;
    int max_height = 0;
    int trans_height = 0;

    if (LV_DISPLAY_ROTATION_270 == rotate || LV_DISPLAY_ROTATION_90 == rotate) {
      max_width = ((DISPLAY_TRANS_SIZE / height) > width)
                      ? (width)
                      : (DISPLAY_TRANS_SIZE / height);
      trans_count = width / max_width + (width % max_width ? (1) : (0));

      x_start_tmp = x_start;
      x_end_tmp = x_end;
    } else {
      max_height = ((DISPLAY_TRANS_SIZE / width) > height)
                       ? (height)
                       : (DISPLAY_TRANS_SIZE / width);
      trans_count = height / max_height + (height % max_height ? (1) : (0));

      y_start_tmp = y_start;
      y_end_tmp = y_end;
    }

    for (int i = 0; i < trans_count; i++) {

      if (LV_DISPLAY_ROTATION_90 == rotate) {
        trans_width = (x_end - x_start_tmp + 1) > max_width
                          ? max_width
                          : (x_end - x_start_tmp + 1);
        x_end_tmp = (x_end - x_start_tmp + 1) > max_width
                        ? (x_start_tmp + max_width - 1)
                        : x_end;
      } else if (LV_DISPLAY_ROTATION_270 == rotate) {
        trans_width = (x_end_tmp - x_start + 1) > max_width
                          ? max_width
                          : (x_end_tmp - x_start + 1);
        x_start_tmp = (x_end_tmp - x_start + 1) > max_width
                          ? (x_end_tmp - trans_width + 1)
                          : x_start;
      } else if (LV_DISPLAY_ROTATION_0 == rotate) {
        trans_height = (y_end - y_start_tmp + 1) > max_height
                           ? max_height
                           : (y_end - y_start_tmp + 1);
        y_end_tmp = (y_end - y_start_tmp + 1) > max_height
                        ? (y_start_tmp + max_height - 1)
                        : y_end;
      } else {
        trans_height = (y_end_tmp - y_start + 1) > max_height
                           ? max_height
                           : (y_end_tmp - y_start + 1);
        y_start_tmp = (y_end_tmp - y_start + 1) > max_height
                          ? (y_end_tmp - max_height + 1)
                          : y_start;
      }

      trans_act = (trans_act == trans_buf_1) ? (trans_buf_2) : (trans_buf_1);
      to = trans_act;

      switch (rotate) {
      case LV_DISPLAY_ROTATION_90:
        for (int y = 0; y < height; y++) {
          for (int x = 0; x < trans_width; x++) {
            *(to + x * height + (height - y - 1)) =
                *(from + y * width + (x_start_tmp - x_start) + x);
          }
        }
        x_draw_start = ver_res - y_end - 1;
        x_draw_end = ver_res - y_start - 1;
        y_draw_start = x_start_tmp;
        y_draw_end = x_end_tmp;
        break;
      case LV_DISPLAY_ROTATION_270:
        for (int y = 0; y < height; y++) {
          for (int x = 0; x < trans_width; x++) {
            *(to + (trans_width - x - 1) * height + y) =
                *(from + y * width + (x_start_tmp - x_start) + x);
          }
        }
        x_draw_start = y_start;
        x_draw_end = y_end;
        y_draw_start = hor_res - x_end_tmp - 1;
        y_draw_end = hor_res - x_start_tmp - 1;
        break;
      case LV_DISPLAY_ROTATION_180:
        for (int y = 0; y < trans_height; y++) {
          for (int x = 0; x < width; x++) {
            *(to + (trans_height - y - 1) * width + (width - x - 1)) =
                *(from + (y_start_tmp - y_start) * width + y * (width) + x);
          }
        }
        x_draw_start = hor_res - x_end - 1;
        x_draw_end = hor_res - x_start - 1;
        y_draw_start = ver_res - y_end_tmp - 1;
        y_draw_end = ver_res - y_start_tmp - 1;
        break;
      case LV_DISPLAY_ROTATION_0:
        for (int y = 0; y < trans_height; y++) {
          for (int x = 0; x < width; x++) {
            *(to + y * (width) + x) =
                *(from + (y_start_tmp - y_start) * width + y * (width) + x);
          }
        }
        x_draw_start = x_start;
        x_draw_end = x_end;
        y_draw_start = y_start_tmp;
        y_draw_end = y_end_tmp;
        break;
      default:
        break;
      }

      if (0 == i) {
        // if (disp_ctx->draw_wait_cb) {
        //     disp_ctx->draw_wait_cb(disp_ctx->panel_handle->user_data);
        // }
        xSemaphoreGive(trans_done_sem);
      }

      xSemaphoreTake(trans_done_sem, portMAX_DELAY);
      // printf("i: %d, x_draw_start: %d, x_draw_end: %d, y_draw_start: %d,
      // y_draw_end: %d\r\n", i, x_draw_start, x_draw_end, y_draw_start,
      // y_draw_end);
      esp_lcd_panel_draw_bitmap(panel_handle, x_draw_start, y_draw_start,
                                x_draw_end + 1, y_draw_end + 1, to);

      if (LV_DISPLAY_ROTATION_90 == rotate) {
        x_start_tmp += max_width;
      } else if (LV_DISPLAY_ROTATION_270 == rotate) {
        x_end_tmp -= max_width;
      }
      if (LV_DISPLAY_ROTATION_0 == rotate) {
        y_start_tmp += max_height;
      } else {
        y_end_tmp -= max_height;
      }
    }
  } else {
    esp_lcd_panel_draw_bitmap(panel_handle, x_start, y_start, x_end + 1,
                              y_end + 1, color_map);
  }
  lv_disp_flush_ready(drv);
}

CustomLcdDisplay::CustomLcdDisplay(esp_lcd_panel_io_handle_t panel_io,
                                   esp_lcd_panel_handle_t panel, int width,
                                   int height, int offset_x, int offset_y,
                                   bool mirror_x, bool mirror_y, bool swap_xy)
    : LcdDisplay(panel_io, panel, width, height) {
  //     width_ = width;
  // height_ = height;

  // draw white
  std::vector<uint16_t> buffer(width_, 0xFFFF);
  for (int y = 0; y < height_; y++) {
    esp_lcd_panel_draw_bitmap(panel_, 0, y, width_, y + 1, buffer.data());
  }

  // Set the display to on
  ESP_LOGI(TAG, "Turning display on");
  ESP_ERROR_CHECK(esp_lcd_panel_disp_on_off(panel_, true));

  ESP_LOGI(TAG, "Initialize LVGL library");
  lv_init();

  ESP_LOGI(TAG, "Initialize LVGL port");
  lvgl_port_cfg_t port_cfg = ESP_LVGL_PORT_INIT_CONFIG();
  port_cfg.task_priority = 1;
  port_cfg.timer_period_ms = 50;
  lvgl_port_init(&port_cfg);
  trans_done_sem = xSemaphoreCreateCounting(1, 0);
  trans_buf_1 = (uint16_t *)heap_caps_malloc(
      DISPLAY_TRANS_SIZE * sizeof(uint16_t), MALLOC_CAP_DMA);
  trans_buf_2 = (uint16_t *)heap_caps_malloc(
      DISPLAY_TRANS_SIZE * sizeof(uint16_t), MALLOC_CAP_DMA);
#if 0
    ESP_LOGI(TAG, "Adding LCD screen");
    const lvgl_port_display_cfg_t display_cfg = {
        .io_handle = panel_io_,
        .panel_handle = panel_,
        .control_handle = nullptr,
        .buffer_size = static_cast<uint32_t>(width_ * height_),
        .double_buffer = false,
        .trans_size = 0,
        .hres = static_cast<uint32_t>(width_),
        .vres = static_cast<uint32_t>(height_),
        .monochrome = false,
        .rotation = {
            .swap_xy = swap_xy,
            .mirror_x = mirror_x,
            .mirror_y = mirror_y,
        },
        .color_format = LV_COLOR_FORMAT_RGB565,
        .flags = {
            .buff_dma = 0,
            .buff_spiram = 1,
            .sw_rotate = 0,
            .swap_bytes = 1,
            .full_refresh = 1,
            .direct_mode = 0,
        },
    };

    display_ = lvgl_port_add_disp(&display_cfg);
    lv_display_set_flush_cb(display_, lvgl_port_flush_callback);
#else

  uint32_t buffer_size = 0;
  lv_color_t *buf1 = NULL;
  lvgl_port_lock(0);
  uint8_t color_bytes = lv_color_format_get_size(LV_COLOR_FORMAT_RGB565);
  display_ = lv_display_create(width_, height_);
  lv_display_set_flush_cb(display_, lvgl_port_flush_callback);
  buffer_size = width_ * height_;
  buf1 = (lv_color_t *)heap_caps_aligned_alloc(1, buffer_size * color_bytes,
                                               MALLOC_CAP_SPIRAM);
  lv_display_set_buffers(display_, buf1, NULL, buffer_size * color_bytes,
                         LV_DISPLAY_RENDER_MODE_FULL);
  lv_display_set_driver_data(display_, panel_);
  lvgl_port_unlock();

#endif

  esp_lcd_panel_io_callbacks_t cbs = {
      .on_color_trans_done = lvgl_port_flush_io_ready_callback,
  };
  /* Register done callback */
  esp_lcd_panel_io_register_event_callbacks(panel_io_, &cbs, display_);

  esp_lcd_panel_disp_on_off(panel_, false);

  if (display_ == nullptr) {
    ESP_LOGE(TAG, "Failed to add display");
    return;
  }

  if (offset_x != 0 || offset_y != 0) {
    lv_display_set_offset(display_, offset_x, offset_y);
  }

  // Note: SetupUI() should be called by Application::Initialize(), not in
  // constructor to ensure lvgl objects are created after the display is fully
  // initialized.
}
