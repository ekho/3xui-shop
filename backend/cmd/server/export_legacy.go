package main

import (
	"errors"
	"os"
	"strconv"

	"example.com/cabinet/backend/internal/modules/operations"
)

// runLegacyExport only reads a private SQLite file; it never loads server configuration.
func runLegacyExport(args []string) error {
	bad := errors.New("EXPORT_FAILED")
	if len(args) != 7 || args[1] != "--source" || args[3] != "--support-bot-id" || args[5] != "--support-group-id" {
		return bad
	}
	bot, e := strconv.ParseInt(args[4], 10, 64)
	if e != nil {
		return bad
	}
	group, e := strconv.ParseInt(args[6], 10, 64)
	if e != nil {
		return bad
	}
	packet, e := operations.ExportLegacy(args[0], args[2], bot, group)
	if e != nil {
		return bad
	}
	raw, e := operations.MarshalLegacyExport(packet)
	if e != nil {
		return bad
	}
	if _, e = os.Stdout.Write(raw); e != nil {
		return bad
	}
	return nil
}
