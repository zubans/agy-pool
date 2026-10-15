package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := LoadFrom(map[string]string{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"TimeoutMax", cfg.TimeoutMax, 600 * time.Second},
		{"Concurrency", cfg.Concurrency, 2},
		{"QuotaCooldown", cfg.QuotaCooldown, time.Hour},
		{"AuthCooldown", cfg.AuthCooldown, 10 * time.Minute},
		{"RegionCooldown", cfg.RegionCooldown, 24 * time.Hour},
		{"EndpointCooldown", cfg.EndpointCooldown, 5 * time.Minute},
		{"PreferPrimary", cfg.PreferPrimary, false},
		{"LogDays", cfg.LogDays, 90},
		{"AttachMaxFiles", cfg.AttachMaxFiles, 10},
		{"AttachMaxBytes", cfg.AttachMaxBytes, ByteSize(209715200)},
		{"SecretsKey", cfg.SecretsKey, ""},
		{"Currency", cfg.Currency, "USD"},
		{"Timezone", cfg.Timezone, "Europe/Moscow"},
		{"NATSURL", cfg.NATSURL, "nats://nats:4222"},
		{"JobsTTL", cfg.JobsTTL, 24 * time.Hour},
		{"JobMaxAttempts", cfg.JobMaxAttempts, 5},
		{"FilesTTL", cfg.FilesTTL, 24 * time.Hour},
		{"SandboxCPU", cfg.SandboxCPU, 2.0},
		{"SandboxMemory", cfg.SandboxMemory, ByteSize(2 << 30)},
		{"SandboxTimeout", cfg.SandboxTimeout, 5 * time.Minute},
		{"HTTPAddr", cfg.HTTPAddr, ":8080"},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://agy:agy@postgres:5432/agy?sslmode=disable"},
		{"DataDir", cfg.DataDir, "/data"},
		{"LogLevel", cfg.LogLevel, "info"},
		{"SandboxdURL", cfg.SandboxdURL, "http://sandboxd:8090"},
		{"AccountsDir", cfg.AccountsDir(), "/data/accounts"},
		{"SkillsDir", cfg.SkillsDir(), "/data/skills"},
		{"WorkDir", cfg.WorkDir(), "/data/work"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	if cfg.Location == nil || cfg.Location.String() != "Europe/Moscow" {
		t.Errorf("Location = %v, want Europe/Moscow", cfg.Location)
	}
	if cfg.SecretsKeyRaw != nil {
		t.Errorf("SecretsKeyRaw must be empty without AGY_SECRETS_KEY")
	}
}

func TestOverride(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", SecretsKeySize)))
	cfg, err := LoadFrom(map[string]string{
		"AGY_TIMEOUT_MAX":    "30",
		"AGY_CONCURRENCY":    "4",
		"AGY_PREFER_PRIMARY": "true",
		"AGY_TIMEZONE":       "UTC",
		"AGY_SANDBOX_MEMORY": "512MiB",
		"AGY_SECRETS_KEY":    key,
		"NATS_URL":           "nats://localhost:4222",
		"AGY_DATA_DIR":       "/srv/agy",
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.TimeoutMax != 30*time.Second {
		t.Errorf("TimeoutMax = %v", cfg.TimeoutMax)
	}
	if cfg.Concurrency != 4 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if !cfg.PreferPrimary {
		t.Errorf("PreferPrimary = false")
	}
	if cfg.Location.String() != "UTC" {
		t.Errorf("Location = %v", cfg.Location)
	}
	if cfg.SandboxMemory != 512<<20 {
		t.Errorf("SandboxMemory = %d", cfg.SandboxMemory)
	}
	if len(cfg.SecretsKeyRaw) != SecretsKeySize {
		t.Errorf("SecretsKeyRaw length = %d", len(cfg.SecretsKeyRaw))
	}
	if cfg.NATSURL != "nats://localhost:4222" {
		t.Errorf("NATSURL = %q", cfg.NATSURL)
	}
	if cfg.WorkDir() != "/srv/agy/work" {
		t.Errorf("WorkDir = %q", cfg.WorkDir())
	}
}

func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"zero timeout", map[string]string{"AGY_TIMEOUT_MAX": "0"}, "AGY_TIMEOUT_MAX"},
		{"negative concurrency", map[string]string{"AGY_CONCURRENCY": "-1"}, "AGY_CONCURRENCY"},
		{"seconds not a number", map[string]string{"AGY_QUOTA_COOLDOWN": "1h"}, "AGY_QUOTA_COOLDOWN"},
		{"unknown timezone", map[string]string{"AGY_TIMEZONE": "Mars/Olympus"}, "AGY_TIMEZONE"},
		{"key not base64", map[string]string{"AGY_SECRETS_KEY": "not base64!"}, "AGY_SECRETS_KEY"},
		{"key too short", map[string]string{"AGY_SECRETS_KEY": base64.StdEncoding.EncodeToString([]byte("short"))}, "32 bytes"},
		{"bad memory", map[string]string{"AGY_SANDBOX_MEMORY": "lots"}, "AGY_SANDBOX_MEMORY"},
		{"memory overflows", map[string]string{"AGY_SANDBOX_MEMORY": "9999999999G"}, "AGY_SANDBOX_MEMORY"},
		{"attachments overflow", map[string]string{"AGY_ATTACH_MAX_BYTES": "-9999999999G"}, "AGY_ATTACH_MAX_BYTES"},
		{"bad currency", map[string]string{"AGY_CURRENCY": "rub"}, "AGY_CURRENCY"},
		{"bad log level", map[string]string{"AGY_LOG_LEVEL": "loud"}, "AGY_LOG_LEVEL"},
		{"empty nats url", map[string]string{"NATS_URL": " "}, "NATS_URL"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadFrom(c.env)
			if err == nil {
				t.Fatalf("expected error containing %q", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("error %q does not mention %q", err, c.wantErr)
			}
		})
	}
}

func TestByteSize(t *testing.T) {
	cases := map[string]ByteSize{
		"1024":  1024,
		"2GiB":  2 << 30,
		"2 GB":  2 << 30,
		"512m":  512 << 20,
		"10KiB": 10 << 10,
	}
	for in, want := range cases {
		var got ByteSize
		if err := got.UnmarshalText([]byte(in)); err != nil {
			t.Errorf("%q: %v", in, err)

			continue
		}
		if got != want {
			t.Errorf("%q = %d, want %d", in, got, want)
		}
	}
}
