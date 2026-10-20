package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type SessionConfig struct {
	CookieName       string
	Secure           bool
	SameSite         string
	Domain           string
	IdleTimeout      time.Duration
	AbsoluteTimeout  time.Duration
	RotationInterval time.Duration
	GracePeriod      time.Duration
}

type Config struct {
	ListenAddr    string
	DatabaseURL   string
	EncryptionKey []byte
	Session       SessionConfig
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:  getenv("LISTEN_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Session: SessionConfig{
			CookieName: getenv("SESSION_COOKIE_NAME", "session"),
			SameSite:   getenv("SESSION_SAME_SITE", "Lax"),
			Domain:     os.Getenv("SESSION_DOMAIN"),
		},
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	key, err := loadEncryptionKey()
	if err != nil {
		return Config{}, err
	}
	cfg.EncryptionKey = key

	if cfg.Session.Secure, err = getBool("SESSION_SECURE_COOKIES", true); err != nil {
		return Config{}, err
	}

	durations := []struct {
		dst      *time.Duration
		key, def string
	}{
		{&cfg.Session.IdleTimeout, "SESSION_IDLE_TIMEOUT", "30m"},
		{&cfg.Session.AbsoluteTimeout, "SESSION_ABSOLUTE_TIMEOUT", "8h"},
		{&cfg.Session.RotationInterval, "SESSION_ROTATION_INTERVAL", "15m"},
		{&cfg.Session.GracePeriod, "SESSION_GRACE_PERIOD", "30s"},
	}
	for _, d := range durations {
		v, err := time.ParseDuration(getenv(d.key, d.def))
		if err != nil {
			return Config{}, fmt.Errorf("%s: %w", d.key, err)
		}
		if v <= 0 {
			return Config{}, fmt.Errorf("%s: must be positive", d.key)
		}
		*d.dst = v
	}

	if err := cfg.Session.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (s SessionConfig) validate() error {
	if s.SameSite != "Lax" && s.SameSite != "Strict" {
		return fmt.Errorf("SESSION_SAME_SITE: must be Lax or Strict, got %q", s.SameSite)
	}
	if s.IdleTimeout > s.AbsoluteTimeout {
		return errors.New("SESSION_IDLE_TIMEOUT must not exceed SESSION_ABSOLUTE_TIMEOUT")
	}
	return nil
}

func loadEncryptionKey() ([]byte, error) {
	raw := os.Getenv("ENCRYPTION_KEY")
	if raw == "" {
		return nil, errors.New("ENCRYPTION_KEY is required (base64 of 32 random bytes)")
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("ENCRYPTION_KEY: not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("ENCRYPTION_KEY: must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getBool(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}
