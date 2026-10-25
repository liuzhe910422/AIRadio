package main

import "testing"

func TestParseCommand(t *testing.T) {
	tests := []struct {
		line string
		name string
		args []string
		text string
	}{
		{line: "connect ws://127.0.0.1:8080/ws device-1", name: "connect", args: []string{"ws://127.0.0.1:8080/ws", "device-1"}},
		{line: "connect ws://127.0.0.1:8080/ws device-1 secret", name: "connect", args: []string{"ws://127.0.0.1:8080/ws", "device-1", "secret"}},
		{line: "sendstart", name: "sendstart"},
		{line: "sendtext 你好 世界", name: "sendtext", args: []string{"你好", "世界"}, text: "你好 世界"},
		{line: "sendend", name: "sendend"},
		{line: "nope extra", name: "nope", args: []string{"extra"}},
		{line: "sendtext", name: "sendtext", text: ""},
		{line: "   ", name: ""},
	}
	for _, tt := range tests {
		got := ParseCommand(tt.line)
		if got.Name != tt.name || got.Text != tt.text || len(got.Args) != len(tt.args) {
			t.Fatalf("parse %q: %+v", tt.line, got)
		}
		for i := range tt.args {
			if got.Args[i] != tt.args[i] {
				t.Fatalf("parse %q arg %d: got %q", tt.line, i, got.Args[i])
			}
		}
	}
}
