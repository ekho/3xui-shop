package app

import (
	"strings"
	"testing"
)

func TestAuditConfigRejectsUnsafeRetention(t *testing.T) {
	t.Setenv("AUDIT_RETENTION_TIMEZONE", "")
	t.Setenv("BOT_TIMEZONE", "")
	for _, value := range []string{"0", "-1", "3651", "bad", "1.5"} {
		t.Setenv("AUDIT_RETENTION_DAYS", value)
		if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "AUDIT_RETENTION_DAYS") || strings.Contains(err.Error(), value) && value == "bad" {
			t.Fatal("unsafe retention config accepted or failed at unrelated prerequisite", err)
		}
	}
	t.Setenv("AUDIT_RETENTION_DAYS", "365")
	t.Setenv("AUDIT_RETENTION_TIMEZONE", "bad/private-zone")
	if _, err := LoadConfig(); err == nil || !strings.Contains(err.Error(), "AUDIT_RETENTION_TIMEZONE") || strings.Contains(err.Error(), "bad/private-zone") {
		t.Fatal("invalid audit timezone reached prerequisites", err)
	}
}

func TestAuditConfigDefaultsAndTimezone(t *testing.T) {
	t.Setenv("AUDIT_RETENTION_DAYS", "")
	t.Setenv("AUDIT_RETENTION_TIMEZONE", "")
	t.Setenv("BOT_TIMEZONE", "")
	if cfg, err := readAuditConfig(); err != nil || cfg.RetentionDays != 365 || cfg.Timezone.String() != "UTC" {
		t.Fatal("audit defaults", err)
	}
	t.Setenv("BOT_TIMEZONE", "Europe/Moscow")
	if cfg, err := readAuditConfig(); err != nil || cfg.Timezone.String() != "Europe/Moscow" {
		t.Fatal("legacy schedule compatibility", err)
	}
	t.Setenv("AUDIT_RETENTION_DAYS", "3650")
	t.Setenv("AUDIT_RETENTION_TIMEZONE", "Asia/Tokyo")
	if cfg, err := readAuditConfig(); err != nil || cfg.RetentionDays != 3650 || cfg.Timezone.String() != "Asia/Tokyo" {
		t.Fatal("explicit retention config", err)
	}
}
