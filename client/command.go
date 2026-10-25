package main

import "strings"

// Command 是一行终端输入。Text 只在 sendtext 时有意义，为命令名之后去掉首尾空白的整段文本。
type Command struct {
	Name string
	Args []string
	Text string
}

// ParseCommand 把一行解析为命令名和参数。空行的 Name 为空。
func ParseCommand(line string) Command {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Command{}
	}
	name, rest, _ := strings.Cut(trimmed, " ")
	rest = strings.TrimSpace(rest)
	cmd := Command{Name: name}
	if rest != "" {
		cmd.Args = strings.Fields(rest)
	}
	if name == "sendtext" {
		cmd.Text = rest
	}
	return cmd
}
