package telegram

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

type serverCommand struct {
	action, id, name, host string
	maxClients             int64
	valid                  bool
}

func parseServerCommand(text, username string) (serverCommand, bool) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || len(text) > 4096 {
		return serverCommand{}, false
	}
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return serverCommand{}, false
	}
	name, mention, addressed := strings.Cut(strings.TrimPrefix(fields[0], "/"), "@")
	if addressed && (username == "" || !strings.EqualFold(mention, username)) {
		return serverCommand{}, false
	}
	cmd := serverCommand{}
	switch name {
	case "servers":
		cmd.action, cmd.valid = "list", len(fields) == 1
	case "server", "server_ping", "server_delete":
		cmd.action = map[string]string{"server": "card", "server_ping": "ping", "server_delete": "delete"}[name]
		if len(fields) >= 2 && len(fields[1]) <= 128 {
			cmd.id = fields[1]
			cmd.valid = len(fields) == 2 && name != "server_delete" || len(fields) == 3 && name == "server_delete" && fields[2] == "confirm"
		}
	case "server_add":
		cmd.action = "add"
		body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), fields[0]))
		parts := strings.Split(body, "|")
		if len(parts) == 3 {
			cmd.name, cmd.host = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			limit := strings.TrimSpace(parts[2])
			max, err := strconv.ParseInt(limit, 10, 32)
			if err == nil && max >= 0 && cmd.name != "" && cmd.host != "" {
				cmd.maxClients, cmd.valid = max, true
			}
		}
	case "servers_sync":
		cmd.action, cmd.valid = "sync", len(fields) == 1
	default:
		return serverCommand{}, false
	}
	return cmd, true
}
