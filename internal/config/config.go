package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	Paperclip    PaperclipConfig    `mapstructure:"paperclip"`
	Tenant       string             `mapstructure:"tenant"`
	Paths        PathsConfig        `mapstructure:"paths"`
}

// OrchestratorConfig for Elixir Phoenix orchestrator access
type OrchestratorConfig struct {
	URL string `mapstructure:"url"`
}

// PaperclipConfig for Paperclip AI agent orchestration
type PaperclipConfig struct {
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

var cfg *Config

// Load reads configuration from file and environment
func Load() (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Config locations
	home, _ := os.UserHomeDir()
	viper.AddConfigPath(filepath.Join(home, ".config", "lw"))
	viper.AddConfigPath(filepath.Join(home, ".lw"))
	viper.AddConfigPath(".")

	// Set defaults
	setDefaults()

	// Read config file (optional)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config: %w", err)
		}
		// Config file not found is OK, use defaults + env
	}

	// Environment variables override
	viper.SetEnvPrefix("LW")
	viper.AutomaticEnv()

	// Map specific env vars
	_ = viper.BindEnv("environment", "LW_ENV")
	_ = viper.BindEnv("tenant", "LW_TENANT")
	_ = viper.BindEnv("database.url", "LW_DB_URL")
	_ = viper.BindEnv("database.host", "LW_DB_HOST")
	_ = viper.BindEnv("database.port", "LW_DB_PORT")
	_ = viper.BindEnv("database.name", "LW_DB_NAME")
	_ = viper.BindEnv("database.user", "LW_DB_USER")
	_ = viper.BindEnv("database.password", "LW_DB_PASSWORD")
	_ = viper.BindEnv("api.agent_key", "LW_AGENT_KEY")

	cfg = &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	if err := cfg.Database.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Reset clears the cached config. Test-only helper.
func Reset() {
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

func setDefaults() {
	home, _ := os.UserHomeDir()

	// Environment
	viper.SetDefault("environment", "local")
	viper.SetDefault("tenant", "lwm_core")

	// Database defaults (local Docker)
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5433)
	viper.SetDefault("database.name", "lightwave_platform")
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "postgres")

	// API defaults
	viper.SetDefault("api.local", "http://api.local.lightwave-media.ltd/api/createos")
	viper.SetDefault("api.staging", "https://api.staging.lightwave-media.ltd/api/createos")
	viper.SetDefault("api.production", "https://api.lightwave-media.ltd/api/createos")

	// Orchestrator defaults (Elixir Phoenix)
	viper.SetDefault("orchestrator.url", "http://localhost:4000")
	_ = viper.BindEnv("orchestrator.url", "LW_ORCHESTRATOR_URL")

	// Paperclip defaults
	viper.SetDefault("paperclip.url", "http://localhost:3100")
	_ = viper.BindEnv("paperclip.url", "PAPERCLIP_URL")

	// Paths
	viper.SetDefault("paths.lightwave_root", filepath.Join(home, "dev"))
	viper.SetDefault("paths.platform", filepath.Join(home, "dev", "lightwave-platform"))
}

// Get returns the loaded config (loads if not already loaded)
func Get() *Config {
	if cfg == nil {
		cfg, _ = Load()
	}
	return cfg
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

// GetPaperclipURL returns the Paperclip API base URL
func (c *Config) GetPaperclipURL() string {
	return c.Paperclip.URL
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
