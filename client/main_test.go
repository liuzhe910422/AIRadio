package main

import (
	"strings"
	"testing"
)

func TestViewHasMessageAndCommandAreas(t *testing.T) {
	session := NewSession()
	ui := newModel(session)
	ui.layout(40, 12)
	session.Execute("nope")
	ui.render()
	view := ui.View()
	if !strings.Contains(view, "未知命令") || !strings.Contains(view, "> ") {
		t.Fatalf("界面:\n%s", view)
	}
	if strings.Index(view, "未知命令") > strings.LastIndex(view, "> ") {
		t.Fatal("命令区应在消息区下方")
	}
}
