#pragma once

#include <string>

struct IntercomChatMessage {
  std::string id;
  std::string parent_id;
  std::string language;
  std::string audio_kind;
  std::string source;
  std::string text;
  std::string status;
  std::string audio_url;
  int duration_ms = 0;
  bool automated = false;
  bool system = false;
  bool own = false;
  bool available = false;
  bool text_truncated = false;
  bool event_notification = false;
  bool event_terminal = false;
};
