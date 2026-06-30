package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// Duration is a time.Duration that (un)marshals as a Go duration string
// (e.g. "30s", "15m", "2h") in JSON/YAML, while also accepting a raw
// nanosecond number for backward compatibility.
type Duration struct {
	time.Duration
}

// MarshalJSON renders the duration as its string form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// UnmarshalJSON accepts either a duration string or a nanosecond number.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		d.Duration = time.Duration(value)
		return nil
	case string:
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", value, err)
		}
		d.Duration = parsed
		return nil
	default:
		return fmt.Errorf("invalid duration type %T", v)
	}
}

// Config holds all runtime configuration for the controller manager.
type Config struct {
	// MetricsAddr is the bind address for the Prometheus /metrics endpoint.
	MetricsAddr string `json:"metricsAddr"`
	// ProbeAddr is the bind address for /healthz and /readyz.
	ProbeAddr string `json:"probeAddr"`
	// UIEnabled turns on the built-in web dashboard. Off by default: the UI has no
	// authentication, so expose it only behind an authenticating proxy or via
	// `kubectl port-forward`.
	UIEnabled bool `json:"uiEnabled"`
	// UIAddr is the bind address for the web dashboard when UIEnabled.
	UIAddr string `json:"uiAddr"`
	// LeaderElection enables single-active-instance leader election.
	LeaderElection bool `json:"leaderElection"`
	// LeaderElectionID is the name of the lease used for leader election.
	LeaderElectionID string `json:"leaderElectionID"`
	// ReconcileConcurrency is the max number of concurrent reconciles per controller.
	ReconcileConcurrency int `json:"reconcileConcurrency"`
	// EvictionPollInterval is how often a draining node is re-checked.
	EvictionPollInterval Duration `json:"evictionPollInterval"`
	// GlobalRequeueInterval is the steady-state requeue period for active requests.
	GlobalRequeueInterval Duration `json:"globalRequeueInterval"`
	// DefaultDrainTimeout applies when a request leaves spec.drainTimeout unset.
	DefaultDrainTimeout Duration `json:"defaultDrainTimeout"`
	// DefaultGlobalTimeout applies when a request leaves spec.globalTimeout unset.
	DefaultGlobalTimeout Duration `json:"defaultGlobalTimeout"`
	// DefaultReplacementTimeout bounds the wait for a replacement node when a
	// request with spec.upgrade leaves replacementTimeout unset.
	DefaultReplacementTimeout Duration `json:"defaultReplacementTimeout"`
	// LogLevel is one of debug|info|warn|error.
	LogLevel string `json:"logLevel"`
	// LogFormat is one of json|console.
	LogFormat string `json:"logFormat"`
	// EnableK8sEvents toggles emission of Kubernetes Events alongside logs.
	EnableK8sEvents bool `json:"enableK8sEvents"`
	// DefaultPolicyName is the MaintenancePolicy used when a request omits PolicyRef.
	DefaultPolicyName string `json:"defaultPolicyName"`
	// AuditExportPath, if set, is a file path the audit logger appends to.
	AuditExportPath string `json:"auditExportPath"`
	// DefaultPoolKeys is the ordered list of node-label keys treated as pool keys.
	DefaultPoolKeys []string `json:"defaultPoolKeys"`
}

// Defaults returns a Config populated with safe production defaults.
func Defaults() *Config {
	return &Config{
		MetricsAddr:               ":8080",
		ProbeAddr:                 ":8081",
		UIEnabled:                 false,
		UIAddr:                    ":8082",
		LeaderElection:            true,
		LeaderElectionID:          "maintenance-orchestrator.maintenance.platform.dev",
		ReconcileConcurrency:      2,
		EvictionPollInterval:      Duration{5 * time.Second},
		GlobalRequeueInterval:     Duration{30 * time.Second},
		DefaultDrainTimeout:       Duration{15 * time.Minute},
		DefaultGlobalTimeout:      Duration{2 * time.Hour},
		DefaultReplacementTimeout: Duration{20 * time.Minute},
		LogLevel:                  "info",
		LogFormat:                 "json",
		EnableK8sEvents:           true,
		DefaultPolicyName:         "cluster-default",
		AuditExportPath:           "",
		DefaultPoolKeys: []string{
			"machine.openshift.io/cluster-api-machineset",
			"eks.amazonaws.com/nodegroup",
			"cloud.google.com/gke-nodepool",
			"agentpool",
			"karpenter.sh/nodepool",
		},
	}
}

// Load builds the effective configuration: defaults, then an optional YAML file
// (path from the CONFIG_FILE env var), then environment-variable overrides.
func Load() (*Config, error) {
	cfg := Defaults()

	if path := os.Getenv("CONFIG_FILE"); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read config file %q: %w", path, err)
		}
		// Unmarshalling into the already-defaulted struct only overrides keys
		// that are present in the file.
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("parse config file %q: %w", path, err)
		}
	}

	if err := applyEnvOverrides(cfg); err != nil {
		return nil, fmt.Errorf("invalid environment override: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// applyEnvOverrides applies environment-variable overrides. A variable that is
// set but malformed is a hard error rather than being silently ignored, so an
// operator typo cannot quietly fall back to the default.
func applyEnvOverrides(cfg *Config) error {
	var err error
	cfg.MetricsAddr = envString("METRICS_ADDR", cfg.MetricsAddr)
	cfg.ProbeAddr = envString("PROBE_ADDR", cfg.ProbeAddr)
	if cfg.UIEnabled, err = envBool("UI_ENABLED", cfg.UIEnabled); err != nil {
		return err
	}
	cfg.UIAddr = envString("UI_ADDR", cfg.UIAddr)
	if cfg.LeaderElection, err = envBool("LEADER_ELECTION", cfg.LeaderElection); err != nil {
		return err
	}
	cfg.LeaderElectionID = envString("LEADER_ELECTION_ID", cfg.LeaderElectionID)
	if cfg.ReconcileConcurrency, err = envInt("RECONCILE_CONCURRENCY", cfg.ReconcileConcurrency); err != nil {
		return err
	}
	if cfg.EvictionPollInterval, err = envDuration("EVICTION_POLL_INTERVAL", cfg.EvictionPollInterval); err != nil {
		return err
	}
	if cfg.GlobalRequeueInterval, err = envDuration("GLOBAL_REQUEUE_INTERVAL", cfg.GlobalRequeueInterval); err != nil {
		return err
	}
	if cfg.DefaultDrainTimeout, err = envDuration("DEFAULT_DRAIN_TIMEOUT", cfg.DefaultDrainTimeout); err != nil {
		return err
	}
	if cfg.DefaultGlobalTimeout, err = envDuration("DEFAULT_GLOBAL_TIMEOUT", cfg.DefaultGlobalTimeout); err != nil {
		return err
	}
	if cfg.DefaultReplacementTimeout, err = envDuration("DEFAULT_REPLACEMENT_TIMEOUT", cfg.DefaultReplacementTimeout); err != nil {
		return err
	}
	cfg.LogLevel = envString("LOG_LEVEL", cfg.LogLevel)
	cfg.LogFormat = envString("LOG_FORMAT", cfg.LogFormat)
	if cfg.EnableK8sEvents, err = envBool("ENABLE_K8S_EVENTS", cfg.EnableK8sEvents); err != nil {
		return err
	}
	cfg.DefaultPolicyName = envString("DEFAULT_POLICY_NAME", cfg.DefaultPolicyName)
	cfg.AuditExportPath = envString("AUDIT_EXPORT_PATH", cfg.AuditExportPath)
	if v := os.Getenv("DEFAULT_POOL_KEYS"); v != "" {
		cfg.DefaultPoolKeys = splitTrim(v)
	}
	return nil
}

// Validate checks that the configuration is internally consistent.
func (c *Config) Validate() error {
	switch c.LogFormat {
	case "json", "console":
	default:
		return fmt.Errorf("logFormat %q must be one of json|console", c.LogFormat)
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logLevel %q must be one of debug|info|warn|error", c.LogLevel)
	}
	if c.ReconcileConcurrency < 1 {
		return fmt.Errorf("reconcileConcurrency must be >= 1, got %d", c.ReconcileConcurrency)
	}
	// Durations below one second are almost always a misconfiguration: a bare
	// number (e.g. "5") is interpreted as nanoseconds, not seconds, which for the
	// requeue-driving intervals would put the controller into a tight hot loop.
	// Reject them with an actionable message instead of silently accepting 5ns.
	for _, d := range []struct {
		name string
		val  time.Duration
	}{
		{"evictionPollInterval", c.EvictionPollInterval.Duration},
		{"globalRequeueInterval", c.GlobalRequeueInterval.Duration},
		{"defaultDrainTimeout", c.DefaultDrainTimeout.Duration},
		{"defaultGlobalTimeout", c.DefaultGlobalTimeout.Duration},
		{"defaultReplacementTimeout", c.DefaultReplacementTimeout.Duration},
	} {
		if d.val < time.Second {
			return fmt.Errorf("%s must be >= 1s, got %s "+
				"(a bare number is parsed as nanoseconds; use a unit, e.g. \"5s\")", d.name, d.val)
		}
	}
	if strings.TrimSpace(c.DefaultPolicyName) == "" {
		return fmt.Errorf("defaultPolicyName must not be empty")
	}
	if c.UIEnabled && strings.TrimSpace(c.UIAddr) == "" {
		return fmt.Errorf("uiAddr must be set when uiEnabled is true")
	}
	return nil
}

func envString(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return def, fmt.Errorf("%s=%q is not a valid boolean (use true/false)", key, v)
	}
	return b, nil
}

func envInt(key string, def int) (int, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def, fmt.Errorf("%s=%q is not a valid integer", key, v)
	}
	return n, nil
}

func envDuration(key string, def Duration) (Duration, error) {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return def, fmt.Errorf("%s=%q is not a valid duration (e.g. \"30s\", \"5m\")", key, v)
	}
	return Duration{d}, nil
}

func splitTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
