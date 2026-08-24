package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestDatabaseConfig_Validate_Empty(t *testing.T) {
	d := DatabaseConfig{}
	if err := d.Validate(); err != nil {
		t.Fatalf("empty URL should pass validation, got: %v", err)
	}
}

func TestDatabaseConfig_Validate_BadURL(t *testing.T) {
	d := DatabaseConfig{URL: "::: not a url"}
	if err := d.Validate(); err == nil {
		t.Fatal("invalid URL should fail validation")
	}
}

func TestDatabaseConfig_Validate_GoodURL(t *testing.T) {
	d := DatabaseConfig{URL: "postgres://u:p@host:5433/db"}
	if err := d.Validate(); err != nil {
		t.Fatalf("valid URL should pass validation, got: %v", err)
	}
}

func TestGetDSN_URLPrefersOverComponents(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{
			URL:  "postgres://u@host:9999/db",
			Host: "should-be-ignored",
			Port: 1111,
		},
	}
	got := c.GetDSN()
	if got != "postgres://u@host:9999/db" {
		t.Errorf("URL should win; got %q", got)
	}
}

func TestGetDSN_FallsBackToComponents(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{
			Host: "h", Port: 5432, Name: "n", User: "u", Password: "p",
		},
	}
	got := c.GetDSN()
	want := "host=h port=5432 dbname=n user=u password=p sslmode=disable"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}

func TestDisplayHostPort_FromURL(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{
			URL:  "postgres://u@example.invalid:7777/db",
			Host: "fallback-host",
			Port: 1234,
		},
	}
	if h := c.DisplayHost(); h != "example.invalid" {
		t.Errorf("DisplayHost from URL: got %q, want example.invalid", h)
	}
	if p := c.DisplayPort(); p != 7777 {
		t.Errorf("DisplayPort from URL: got %d, want 7777", p)
	}
}

func TestDisplayHostPort_FromComponents(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{Host: "fallback-host", Port: 1234},
	}
	if h := c.DisplayHost(); h != "fallback-host" {
		t.Errorf("DisplayHost from components: got %q", h)
	}
	if p := c.DisplayPort(); p != 1234 {
		t.Errorf("DisplayPort from components: got %d", p)
	}
}

func TestApplyDBURL_OverridesCachedConfig(t *testing.T) {
	c := &Config{
		Database: DatabaseConfig{Host: "old", Port: 5432},
	}
	if err := ApplyDBURL(c, "postgres://u@new-host:6666/db"); err != nil {
		t.Fatal(err)
	}
	if c.Database.URL != "postgres://u@new-host:6666/db" {
		t.Errorf("ApplyDBURL did not set URL field: %q", c.Database.URL)
	}
	if h := c.DisplayHost(); h != "new-host" {
		t.Errorf("DisplayHost after override: got %q, want new-host", h)
	}
}

func TestApplyDBURL_RejectsInvalidURL(t *testing.T) {
	c := &Config{}
	if err := ApplyDBURL(c, "::: bad :::"); err == nil {
		t.Fatal("invalid URL should be rejected")
	}
}

func TestApplyDBURL_EmptyIsNoop(t *testing.T) {
	c := &Config{Database: DatabaseConfig{Host: "h", Port: 1}}
	if err := ApplyDBURL(c, ""); err != nil {
		t.Fatal(err)
	}
	if c.Database.URL != "" {
		t.Errorf("empty input should not set URL, got %q", c.Database.URL)
	}
	if c.Database.Host != "h" {
		t.Errorf("empty input should not touch components, got Host=%q", c.Database.Host)
	}
}

// TestSetGet_RoundTrip verifies Set persists to a temp HOME and Lookup
// reads back. Manipulates HOME to keep the test hermetic.
func TestSetGet_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	Reset()

	if err := Set("database.host", "127.0.0.99"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	path := filepath.Join(tmp, ".config", "lw", "config.yaml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected config.yaml at %s: %v", path, err)
	}

	got, ok := Lookup("database.host")
	if !ok {
		t.Fatal("Lookup did not find database.host")
	}
	if got != "127.0.0.99" {
		t.Errorf("got %q, want 127.0.0.99", got)
	}
}

func TestSet_RejectsUnknownKey(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	Reset()
	if err := Set("not.a.real.key", "x"); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

// TestSetDefaults_OrchestratorPort pins the nullboiler port (lightwave-cli#287).
//
// The default was http://localhost:4000 — a retired Elixir Phoenix service that
// matches no null* port (nullclaw 3000, nulltickets 7700, nullboiler 8080,
// nullhub 19800), so the `lw health` orchestrator probe could never pass.
//
// Not parallel: setDefaults writes to the process-global viper registry.
func TestSetDefaults_OrchestratorPort(t *testing.T) { //nolint:paralleltest // global viper state
	// setDefaults binds LW_ORCHESTRATOR_URL, so an operator shell that exports it
	// (e.g. after `source <(mise run env)`) would otherwise fail this pin even
	// though the default is correct. viper.AllowEmptyEnv defaults to false, so an
	// empty value reads as unset and the default wins.
	t.Setenv("LW_ORCHESTRATOR_URL", "")
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("LW_ORCHESTRATOR_URL", "")
	setDefaults()

	const want = "http://localhost:8080"
	if got := viper.GetString("orchestrator.url"); got != want {
		t.Errorf("orchestrator.url default\n got: %q\nwant: %q (nullboiler)", got, want)
	}
}

// TestSetDefaults_OrchestratorURLEnvOverride pins that LW_ORCHESTRATOR_URL still
// wins over the default, so the port stays overridable per machine.
func TestSetDefaults_OrchestratorURLEnvOverride(t *testing.T) { //nolint:paralleltest // global viper state
	viper.Reset()
	t.Cleanup(viper.Reset)
	t.Setenv("LW_ORCHESTRATOR_URL", "http://localhost:19800")
	setDefaults()

	const want = "http://localhost:19800"
	if got := viper.GetString("orchestrator.url"); got != want {
		t.Errorf("LW_ORCHESTRATOR_URL should override the default\n got: %q\nwant: %q", got, want)
	}
}
