package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	session  *Session
	viewport viewport.Model
	input    textinput.Model
	ready    bool
}

func newModel(s *Session) model {
	input := textinput.New()
	input.Placeholder = "输入命令"
	input.Prompt = "> "
	input.Focus()
	return model{
		session:  s,
		viewport: viewport.New(80, 20),
		input:    input,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, waitEvent(m.session.Events()))
}

func waitEvent(ch <-chan Event) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.layout(msg.Width, msg.Height)
		m.render()
		return m, nil
	case Event:
		m.render()
		return m, waitEvent(m.session.Events())
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		if msg.Type == tea.KeyEnter {
			line := m.input.Value()
			m.input.SetValue("")
			m.session.Execute(line)
			m.render()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *model) layout(width, height int) {
	if width < 2 {
		width = 2
	}
	if height < 2 {
		height = 2
	}
	m.viewport.Width = width
	m.viewport.Height = height - 1
	m.input.Width = width - 2
	m.ready = true
}

func (m *model) render() {
	m.viewport.SetContent(strings.TrimRight(Render(m.session.Snapshot()), "\n"))
	m.viewport.GotoBottom()
}

func (m model) View() string {
	body := "就绪"
	if m.ready {
		body = m.viewport.View()
	}
	return body + "\n" + m.input.View()
}

func main() {
	program := tea.NewProgram(newModel(NewSession()), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
