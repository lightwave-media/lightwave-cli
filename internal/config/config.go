package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds all CLI configuration
type Config struct {
	Environment string         `mapstructure:"environment"`
	Database    DatabaseConfig `mapstructure:"database"`
	API         APIConfig      `mapstructure:"api"`
	Tenant      string         `mapstructure:"tenant"`
	Paths       PathsConfig    `mapstructure:"paths"`
}

// DatabaseConfig for direct PostgreSQL access (Tier 2)
type DatabaseConfig struct {
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
	viper.BindEnv("environment", "LW_ENV")
	viper.BindEnv("tenant", "LW_TENANT")
	viper.BindEnv("database.host", "LW_DB_HOST")
	viper.BindEnv("database.port", "LW_DB_PORT")
	viper.BindEnv("database.name", "LW_DB_NAME")
	viper.BindEnv("database.user", "LW_DB_USER")
	viper.BindEnv("database.password", "LW_DB_PASSWORD")
	viper.BindEnv("api.agent_key", "LW_AGENT_KEY")

	cfg = &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("error parsing config: %w", err)
	}

	return cfg, nil
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
	viper.SetDefault("api.local", "http://local.lightwave-media.site:8000/api/createos")
	viper.SetDefault("api.staging", "https://api.staging.lightwave-media.ltd/api/createos")
	viper.SetDefault("api.production", "https://api.lightwave-media.ltd/api/createos")

	// Paths
	viper.SetDefault("paths.lightwave_root", filepath.Join(home, "dev", "lightwave-media"))
	viper.SetDefault("paths.platform", filepath.Join(home, "dev", "lightwave-media", "lightwave-platform", "src"))
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

// GetAgentKey returns the agent key from environment
func GetAgentKey() string {
	return os.Getenv("LW_AGENT_KEY")
}

// GetDSN returns the PostgreSQL connection string
func (c *Config) GetDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=disable",
		c.Database.Host,
		c.Database.Port,
		c.Database.Name,
		c.Database.User,
		c.Database.Password,
	)
}
