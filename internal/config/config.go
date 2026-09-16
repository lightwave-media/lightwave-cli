package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/spf13/viper"
)

// Validate checks Database.URL parses as a Postgres DSN if set.
// Empty URL is fine — components are used. Component fields are not
// validated here (they have defaults).
func (d *DatabaseConfig) Validate() error {
	if d.URL == "" {
		return nil
	}
	if _, err := pgconn.ParseConfig(d.URL); err != nil {
		return fmt.Errorf("invalid database URL: %w", err)
	}
	return nil
}

// Config holds all CLI configuration
type Config struct {
	Environment  string             `mapstructure:"environment"`
	Database     DatabaseConfig     `mapstructure:"database"`
	API          APIConfig          `mapstructure:"api"`
	Orchestrator OrchestratorConfig `mapstructure:"orchestrator"`
	Tenant       string             `mapstructure:"tenant"`
	Paths        PathsConfig        `mapstructure:"paths"`
	Deploy       DeployConfig       `mapstructure:"deploy"`
}

// DeployConfig names the ECS targets `lw deploy` acts on.
//
// These were derived from the environment name — cluster `platform-<env>`, log
// group `/ecs/<cluster>-<service>` — which was the Django-era convention. The
// cluster that exists is `lightwave-platform` and its log group is
// `/ecs/lightwave-platform`, so every `lw deploy` verb failed with
// ClusterNotFoundException (#368). A naming convention cannot be corrected once
// the thing it names stops following it; configuration can.
//
// Clusters is keyed by environment, for when a second environment exists.
// Cluster is the single-target fallback, and the default below is the cluster
// that is actually deployed today.
type DeployConfig struct {
	Clusters  map[string]string `mapstructure:"clusters"`
	LogGroups map[string]string `mapstructure:"log_groups"`
	Cluster   string            `mapstructure:"cluster"`
	LogGroup  string            `mapstructure:"log_group"`
}

// OrchestratorConfig for Elixir Phoenix orchestrator access
type OrchestratorConfig struct {
	URL string `mapstructure:"url"`
}

// DatabaseConfig for direct PostgreSQL access (Tier 2).
//
// URL takes precedence over the individual host/port/name/user/password
// fields. When set, it must be a valid postgres:// or postgresql:// DSN
// (validated on Load). Use it for one-off overrides via --db-url or
// LW_DB_URL; use the component fields when persisting per-machine
// defaults in ~/.config/lw/config.yaml.
type DatabaseConfig struct {
	URL      string `mapstructure:"url"`
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Name     string `mapstructure:"name"`
	User     string `mapstructure:"user"`
	Password string `mapstructure:"password"`
}

// APIConfig for Django API access (Tier 3)
type APIConfig struct {
	Local      string `mapstructure:"local"`
	Staging    string `mapstructure:"staging"`
	Production string `mapstructure:"production"`
}

// PathsConfig for workspace paths
type PathsConfig struct {
	LightwaveRoot string `mapstructure:"lightwave_root"`
	Platform      string `mapstructure:"platform"`
}

// defaultPostgresPort is PostgreSQL's standard port. Local brew postgresql@17
// and the compose stack (5432:5432) both serve it (CORE-0049 §5).
const defaultPostgresPort = 5432

// cfg is the process-wide config singleton, and mu is the only thing that may
// read or write it.
//
// #400: there was no synchronisation here at all. Two goroutines whose FIRST
// Get() landed together both saw nil, both ran Load(), and both wrote this
// global plus viper's own package state — 83 race reports from a single
// `go test -race -run TestMCP ./internal/cli/`. It hid because the race needs
// two concurrent first calls, and in a full package run something almost always
// loads config single-threaded first, after which cfg is non-nil forever. Test
// ordering was the only thing between this and a red suite.
//
// Latent rather than live while the CLI is single-goroutine at startup, but
// `lw mcp serve` is a long-running server surface, which is exactly the shape
// that turns an init race into a real one.
//
// A mutex rather than sync.Once, because Reset() must be able to clear the
// singleton and have the next call rebuild it; a Once cannot be re-armed.
var (
	mu  sync.Mutex
	cfg *Config
)

// Load reads configuration from file and environment.
func Load() (*Config, error) {
	mu.Lock()
	defer mu.Unlock()

	return loadLocked()
}

// loadLocked does the work. Callers must hold mu.
func loadLocked() (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}

	// A FRESH instance per load, never the viper package global (#459).
	//
	// viper.AddConfigPath APPENDS to a package-global list and never replaces
	// it, while Reset() clears only the cfg singleton — so every load after a
	// Reset() permanently grew viper's search order. Once $HOME differed
	// between two loads, the list held both homes and ReadInConfig took the
	// FIRST match, which is the older directory. `Set()` writes the file and
	// calls Reset() precisely so the next Get() re-reads it; that contract
	// depends on the second load searching the same place as the first.
	//
	// It read as a test-only problem because $HOME does not change inside a
	// normal `lw` invocation. It is not: an instance per load is what makes
	// Reset() mean what its docstring says.
	v := viper.New()

	v.SetConfigName("config")
	v.SetConfigType("yaml")

	// Config locations
	home, _ := os.UserHomeDir()
	v.AddConfigPath(filepath.Join(home, ".config", "lw"))
	v.AddConfigPath(filepath.Join(home, ".lw"))
	v.AddConfigPath(".")

	// Set defaults
	setDefaults(v)

	// Read config file (optional)
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return nil, fmt.Errorf("error reading config: %w", err)
		}
		// Config file not found is OK, use defaults + env
	}

	// Environment variables override
	v.SetEnvPrefix("LW")
	v.AutomaticEnv()

	// Map specific env vars
	_ = v.BindEnv("environment", "LW_ENV")
	_ = v.BindEnv("tenant", "LW_TENANT")
	_ = v.BindEnv("database.url", "LW_DB_URL")
	_ = v.BindEnv("database.host", "LW_DB_HOST")
	_ = v.BindEnv("database.port", "LW_DB_PORT")
	_ = v.BindEnv("database.name", "LW_DB_NAME")
	_ = v.BindEnv("database.user", "LW_DB_USER")
	_ = v.BindEnv("database.password", "LW_DB_PASSWORD")
	_ = v.BindEnv("api.agent_key", "LW_AGENT_KEY")

	// Built into a LOCAL and published only once it is complete and valid.
	//
	// This used to assign `cfg = &Config{}` and then unmarshal and validate
	// into it, so a failed load left the half-built value cached: the next
	// Load() short-circuited on `cfg != nil` and returned it with a nil error.
	// A config that failed validation was then served as valid for the life of
	// the process, silently. The concurrent reader had it worse — it could see
	// a non-nil cfg whose fields had not been unmarshalled yet.
	loaded := &Config{}
	if err := v.Unmarshal(loaded); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	if err := loaded.Database.Validate(); err != nil {
		return nil, err
	}

	cfg = loaded

	return cfg, nil
}

// Reset clears the cached config. Test-only helper.
func Reset() {
	mu.Lock()
	defer mu.Unlock()

	cfg = nil
}

// ApplyDBURL overrides the cached config's Database.URL with the provided
// value (typically from --db-url). Validates the URL parses; returns an
// error otherwise.
func ApplyDBURL(c *Config, url string) error {
	if url == "" {
		return nil
	}
	if _, err := pgconn.ParseConfig(url); err != nil {
		return fmt.Errorf("invalid --db-url: %w", err)
	}
	c.Database.URL = url
	return nil
}

func setDefaults(v *viper.Viper) {
	home, _ := os.UserHomeDir()

	// Environment
	v.SetDefault("environment", "local")
	v.SetDefault("tenant", "lwm_core")

	// Database defaults — local Postgres 17 on the standard port (CORE-0049
	// §5: one PG major across local, compose and RDS). The old 5433 default
	// pointed at a compose mapping that no longer exists; nothing listened
	// there, which surfaced as "platform database unavailable" on every data
	// verb. The compose stack maps 5432:5432; a nonstandard local port is
	// opt-in via LW_DB_PORT.
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", defaultPostgresPort)
	v.SetDefault("database.name", "lightwave_platform")
	v.SetDefault("database.user", "postgres")
	v.SetDefault("database.password", "postgres")

	// API defaults
	v.SetDefault("api.local", "http://api.local.lightwave-media.ltd/api/createos")
	v.SetDefault("api.staging", "https://api.staging.lightwave-media.ltd/api/createos")
	v.SetDefault("api.production", "https://api.lightwave-media.ltd/api/createos")

	// Orchestrator defaults — nullboiler (lightwave-ai src/nullboiler), which
	// serves :8080. The previous :4000 default pointed at a retired Elixir
	// Phoenix service and matched no null* port (nullclaw 3000, nulltickets
	// 7700, nullboiler 8080, nullhub 19800), so `lw health` could never pass.
	v.SetDefault("orchestrator.url", "http://localhost:8080")
	_ = v.BindEnv("orchestrator.url", "LW_ORCHESTRATOR_URL")

	// Paths — LW_LIGHTWAVE_ROOT overrides default ~/dev (needed for sandboxed e2e + CI).
	v.SetDefault("paths.lightwave_root", filepath.Join(home, "dev"))
	v.SetDefault("paths.platform", filepath.Join(home, "dev", "lightwave-platform"))
	_ = v.BindEnv("paths.lightwave_root", "LW_LIGHTWAVE_ROOT", "LW_DEV_ROOT")

	// Deploy — the cluster that exists today (lightwave-infrastructure-live#72),
	// not the `platform-<env>` name the Django-era stack used. See DeployConfig.
	v.SetDefault("deploy.cluster", "lightwave-platform")

	_ = v.BindEnv("deploy.cluster", "LW_DEPLOY_CLUSTER")
	_ = v.BindEnv("deploy.log_group", "LW_DEPLOY_LOG_GROUP")
}

// Get returns the loaded config, loading it if necessary.
//
// The error is deliberately still swallowed — 40 call sites rely on the
// no-error shape and nil-checking `Get()` is the established contract. What
// changed is that a failed load no longer leaves a half-built config behind for
// the next caller to trust (see loadLocked).
func Get() *Config {
	mu.Lock()
	defer mu.Unlock()

	c, _ := loadLocked()

	return c
}

// GetAPIURL returns the API URL for the current environment
func (c *Config) GetAPIURL() string {
	switch c.Environment {
	case "production":
		return c.API.Production
	case "staging":
		return c.API.Staging
	default:
		return c.API.Local
	}
}

// GetOrchestratorURL returns the orchestrator URL for the current environment
func (c *Config) GetOrchestratorURL() string {
	return c.Orchestrator.URL
}

// GetAgentKey returns the agent key from environment
func GetAgentKey() string {
	return os.Getenv("LW_AGENT_KEY")
}

// GetDSN returns the PostgreSQL connection string. When Database.URL is
// set (via --db-url, LW_DB_URL, or config.yaml), it wins outright. Otherwise
// the keyword form is built from the individual fields.
func (c *Config) GetDSN() string {
	if c.Database.URL != "" {
		return c.Database.URL
	}
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=disable",
		c.Database.Host,
		c.Database.Port,
		c.Database.Name,
		c.Database.User,
		c.Database.Password,
	)
}

// DisplayHost returns the host to show in `lw config show` and error
// messages: the URL's parsed host if URL is set, else the keyword Host.
func (c *Config) DisplayHost() string {
	if c.Database.URL != "" {
		if pc, err := pgconn.ParseConfig(c.Database.URL); err == nil {
			return pc.Host
		}
	}
	return c.Database.Host
}

// DisplayPort returns the port to show in `lw config show` and error
// messages: the URL's parsed port if URL is set, else the keyword Port.
func (c *Config) DisplayPort() int {
	if c.Database.URL != "" {
		if pc, err := pgconn.ParseConfig(c.Database.URL); err == nil {
			return int(pc.Port)
		}
	}
	return c.Database.Port
}

// PrintRoot is the rendered ~/.lightwave home, honouring LW_HOME_PRINT.
//
// It lives here because config is the one internal package that imports no
// other, so every consumer can reach it without a cycle. internal/homepolicy
// and internal/observability each carried a private copy of these six lines;
// a resolver that disagrees between packages points two halves of the same
// operation at two different trees, which for a hygiene verb means measuring
// one home and repairing another.
//
// Deliberately independent of Get(): callers reach for this before the config
// singleton is loaded, and a print root that returns "" until Load() succeeds
// is a footgun for exactly the diagnostic paths that run when Load() failed.
func PrintRoot() string {
	if root := os.Getenv("LW_HOME_PRINT"); root != "" {
		return root
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".lightwave")
}
