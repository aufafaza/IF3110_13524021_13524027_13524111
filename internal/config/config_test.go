package config_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/config"
)

var validKey = base64.StdEncoding.EncodeToString(make([]byte, 32))

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	t.Setenv("ENCRYPTION_KEY", validKey)
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddr != ":8080" {
		t.Errorf("ListenAddr = %q", cfg.ListenAddr)
	}
	s := cfg.Session
	if s.IdleTimeout != 30*time.Minute || s.AbsoluteTimeout != 8*time.Hour ||
		s.RotationInterval != 15*time.Minute || s.GracePeriod != 30*time.Second {
		t.Errorf("unexpected session durations: %+v", s)
	}
	if !s.Secure {
		t.Error("Secure should default to true")
	}
	if s.SameSite != "Lax" || s.CookieName != "session" {
		t.Errorf("unexpected cookie defaults: %+v", s)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Errorf("EncryptionKey length = %d", len(cfg.EncryptionKey))
	}
}

func TestLoadOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("LISTEN_ADDR", ":9000")
	t.Setenv("SESSION_IDLE_TIMEOUT", "5m")
	t.Setenv("SESSION_SECURE_COOKIES", "false")
	t.Setenv("SESSION_SAME_SITE", "Strict")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":9000" || cfg.Session.IdleTimeout != 5*time.Minute ||
		cfg.Session.Secure || cfg.Session.SameSite != "Strict" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"missing database url", map[string]string{"DATABASE_URL": ""}, "DATABASE_URL"},
		{"missing key", map[string]string{"ENCRYPTION_KEY": ""}, "ENCRYPTION_KEY"},
		{"key not base64", map[string]string{"ENCRYPTION_KEY": "!!!"}, "ENCRYPTION_KEY"},
		{"key wrong length", map[string]string{"ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(make([]byte, 16))}, "32 bytes"},
		{"bad duration", map[string]string{"SESSION_IDLE_TIMEOUT": "soon"}, "SESSION_IDLE_TIMEOUT"},
		{"zero duration", map[string]string{"SESSION_GRACE_PERIOD": "0s"}, "SESSION_GRACE_PERIOD"},
		{"bad bool", map[string]string{"SESSION_SECURE_COOKIES": "maybe"}, "SESSION_SECURE_COOKIES"},
		{"bad samesite", map[string]string{"SESSION_SAME_SITE": "None"}, "SESSION_SAME_SITE"},
		{"idle exceeds absolute", map[string]string{"SESSION_IDLE_TIMEOUT": "9h"}, "must not exceed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := config.Load()
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}
