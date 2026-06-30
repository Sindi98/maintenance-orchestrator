package config

import (
	"testing"
	"time"
)

func TestValidateRejectsSubSecondDurations(t *testing.T) {
	// A bare number like "5" in YAML is parsed as 5ns by Duration.UnmarshalJSON;
	// Validate must reject it rather than let the controller hot-loop.
	cases := []struct {
		name  string
		mut   func(c *Config)
		field string
	}{
		{"evictionPoll 5ns", func(c *Config) { c.EvictionPollInterval = Duration{5} }, "evictionPollInterval"},
		{"globalRequeue 5ns", func(c *Config) { c.GlobalRequeueInterval = Duration{5} }, "globalRequeueInterval"},
		{"drainTimeout 5ns", func(c *Config) { c.DefaultDrainTimeout = Duration{5} }, "defaultDrainTimeout"},
		{"globalTimeout 5ns", func(c *Config) { c.DefaultGlobalTimeout = Duration{5} }, "defaultGlobalTimeout"},
		{"replacementTimeout 5ns", func(c *Config) { c.DefaultReplacementTimeout = Duration{5} }, "defaultReplacementTimeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Defaults()
			tc.mut(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error mentioning %q", tc.field)
			}
		})
	}
}

func TestValidateAcceptsDefaults(t *testing.T) {
	if err := Defaults().Validate(); err != nil {
		t.Fatalf("Defaults() must validate: %v", err)
	}
}

func TestValidateAcceptsOneSecond(t *testing.T) {
	c := Defaults()
	c.EvictionPollInterval = Duration{time.Second}
	c.GlobalRequeueInterval = Duration{time.Second}
	if err := c.Validate(); err != nil {
		t.Fatalf("1s intervals must validate: %v", err)
	}
}

func TestDurationUnmarshalUnitsVsBareNumber(t *testing.T) {
	var d Duration
	if err := d.UnmarshalJSON([]byte(`"5s"`)); err != nil {
		t.Fatalf("unmarshal \"5s\": %v", err)
	}
	if d.Duration != 5*time.Second {
		t.Errorf("\"5s\" = %s, want 5s", d.Duration)
	}

	// A bare number is still accepted (backward compatibility) but as nanoseconds.
	var n Duration
	if err := n.UnmarshalJSON([]byte(`5`)); err != nil {
		t.Fatalf("unmarshal 5: %v", err)
	}
	if n.Duration != 5*time.Nanosecond {
		t.Errorf("bare 5 = %s, want 5ns", n.Duration)
	}
}

func TestLoadRejectsMalformedEnvOverride(t *testing.T) {
	t.Setenv("RECONCILE_CONCURRENCY", "notanumber")
	if _, err := Load(); err == nil {
		t.Fatal("Load() = nil, want an error for a malformed RECONCILE_CONCURRENCY")
	}
}

func TestLoadAppliesValidEnvOverride(t *testing.T) {
	t.Setenv("UI_ENABLED", "true")
	t.Setenv("EVICTION_POLL_INTERVAL", "10s")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.UIEnabled {
		t.Error("UI_ENABLED=true not applied")
	}
	if cfg.EvictionPollInterval.Duration != 10*time.Second {
		t.Errorf("EVICTION_POLL_INTERVAL = %s, want 10s", cfg.EvictionPollInterval.Duration)
	}
}
