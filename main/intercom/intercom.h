#pragma once

#include <atomic>
#include <condition_variable>
#include <cstddef>
#include <deque>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "chat_message.h"
#include "protocol.h"
#include <freertos/FreeRTOS.h>
#include <freertos/task.h>
#include <web_socket.h>

class Application;

struct IntercomRoomOption {
  std::string id;
  std::string name;
  std::string code;
  bool current = false;
  bool is_default = false;
};

class Intercom {
public:
  explicit Intercom(Application &app);
  void Start();
  void NetworkChanged(bool connected);
  void Press();
  void Cancel();
  void Tick();
  void PlaybackDrained();
  bool Busy() const;
  bool IsRecording() const;
  const char *Status() const;
  const char *DeviceId() const { return id_.c_str(); }
  std::string SpeakingDeviceId();
  const char *RoomName() const { return room_name_.c_str(); }
  bool CanRecord() const;
  bool IsHosted() const { return hosted_; }
  bool SetHosted(bool enabled);
  bool CreateRoom(const std::string &name,
                  const std::vector<std::string> &invite_ids);
  bool JoinRoom(const std::string &code);
  bool LeaveRoom();
  bool AcceptInvite();
  std::string ListRooms();
  // Room picker state is owned by the Application task.
  const std::vector<IntercomRoomOption> &RoomOptions() const {
    return room_options_;
  }
  bool RoomPickerVisible() const { return room_picker_visible_; }
  bool RoomPickerPending() const { return !room_picker_pending_.empty(); }
  int RoomPickerSeconds() const;
  const std::string &RoomPickerError() const { return room_picker_error_; }
  bool HasPendingInvite() const { return !pending_invite_code_.empty(); }
  uint32_t RoomPickerRevision() const { return room_picker_revision_; }
  void SelectRoom(const std::string &id);
  void CloseRoomPicker();
  bool AuthError() const { return auth_error_; }
  void MarkAuthError() { auth_error_ = true; }
  // Called by MCP on the Application task; caller owns the returned JSON.
  struct cJSON *GetSessionTranscript() const;
  // UI access and controls run on Application's task only.
  const std::vector<IntercomChatMessage> &ChatMessages() const {
    return chat_messages_;
  }
  uint32_t ChatRevision() const { return chat_revision_; }
  const std::string &ReplayMessage() const { return replay_message_; }
  void PlayChatMessage(const std::string &id);
  void StopChatReplay();

private:
  void ShowRoomPicker(const std::string &response, uint32_t generation);
  std::vector<IntercomRoomOption> room_options_;
  std::atomic<uint32_t> room_picker_generation_{0};
  uint32_t room_picker_revision_ = 0;
  bool room_picker_visible_ = false;
  std::atomic<bool> auth_error_{false};
  int64_t room_picker_deadline_ = 0;
  std::string room_picker_pending_;
  std::string room_picker_error_;
  std::string pending_invite_code_;
  std::string pending_invite_room_name_;
  enum class State {
    Disabled,
    Disconnected,
    Connecting,
    Ready,
    Recording,
    Sending,
    Playing,
    Replaying
  };
  std::deque<std::string> announcements_;
  std::deque<std::string> announced_ids_;
  std::vector<IntercomChatMessage> chat_messages_;
  std::string chat_session_;
  std::string replay_message_;
  uint32_t chat_revision_ = 0;
  bool chat_records_evicted_ = false;
  std::atomic<uint32_t> replay_generation_{0};
  std::atomic<bool> replay_task_active_{false};
  std::mutex replay_mutex_;
  bool replay_download_done_ = false;
  void ClearChatSession();
  void HandleChatEvent(const struct cJSON *json);
  void ReplayTask(std::string url, uint32_t generation);
  std::atomic<bool> hosted_{false};
  Application &app_;
  std::atomic<State> state_{State::Disabled};
  std::atomic<bool> network_{false};
  std::atomic<bool> stop_recording_{false};
  std::atomic<bool> cancel_recording_{false};
  std::atomic<bool> session_failed_{false};
  std::atomic<bool> record_task_active_{false};
  std::string id_;
  std::string url_;
  std::string api_key_;
  std::string room_name_ = "默认房间";
  std::string message_id_;
  std::string incoming_id_;
  std::string incoming_source_id_;
  std::shared_ptr<WebSocket> ws_;
  uint16_t frame_lengths_[500] = {};
  int frame_count_ = 0;
  size_t bytes_ = 0;
  int incoming_frames_ = 0;
  std::atomic<int64_t> playback_started_at_{0};
  std::atomic<int64_t> playback_ended_at_{0};
  std::atomic<bool> hello_ok_{false};
  std::atomic<bool> auth_error_notified_{false};
  std::atomic<bool> auth_error_received_{false};
  std::atomic<bool> start_ok_{false};
  std::atomic<bool> end_ok_{false};
  std::atomic<bool> end_pending_{false};
  std::atomic<bool> start_sent_{false};
  std::mutex control_mutex_;
  std::mutex rooms_mutex_;
  std::condition_variable rooms_cv_;
  std::string rooms_request_id_;
  std::string rooms_response_;
  std::deque<std::string> pending_control_;
  std::mutex frame_mutex_;
  std::deque<std::vector<uint8_t>> outgoing_frames_;
  std::deque<std::unique_ptr<AudioStreamPacket>> incoming_buffer_;
  std::mutex incoming_mutex_;
  int incoming_buffer_target_ = 8;
  bool incoming_playback_started_ = false;
  std::atomic<bool> incoming_initialized_{false};
  int64_t record_started_ = 0;
  void SetState(State state);
  void NetworkTask();
  void RecordTask();
  void Upload();
  void SendRealtimeFrames();
  void OnData(const char *data, size_t len, bool binary);
  bool SendText(const std::string &text);
  void QueueText(std::string text);
  void FlushControl();
  void ResumeAssistantAudio();
};
