package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	Region         string   `yaml:"region"`
	Brokers        []string `yaml:"brokers"`
	Output         string   `yaml:"output"`
	SCRAMMechanism string   `yaml:"scram_mechanism"`
	KMSKeyID       string   `yaml:"kms_key_id"`
	ClusterARN     string   `yaml:"cluster_arn"`
	SASLUsername   string   `yaml:"sasl_username"`
	SASLPassword   string   `yaml:"sasl_password"`
}

var loadedConfig AppConfig
var configLoaded bool
var configPathUsed string

// LoadAppConfig loads YAML config from an explicit path or common default locations.
// Returns config (may be empty), the used path (may be empty), and error if reading/parsing fails.
func LoadAppConfig(explicitPath string) (AppConfig, string, error) {
	if configLoaded {
		return loadedConfig, configPathUsed, nil
	}

	paths := []string{}
	if explicitPath != "" {
		paths = append(paths, explicitPath)
	}
	// current directory default
	paths = append(paths, "default-config.yaml")
	// XDG config
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "msk-admin", "config.yaml"))
	}
	// $HOME/.config
	if home, _ := os.UserHomeDir(); home != "" {
		paths = append(paths, filepath.Join(home, ".config", "msk-admin", "config.yaml"))
		paths = append(paths, filepath.Join(home, ".msk-admin.yaml"))
	}

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			// other read errors should bubble up
			return AppConfig{}, "", err
		}
		var cfg AppConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return AppConfig{}, "", err
		}
		// normalize
		for i := range cfg.Brokers {
			cfg.Brokers[i] = strings.TrimSpace(cfg.Brokers[i])
		}
		loadedConfig = cfg
		configLoaded = true
		configPathUsed = p
		return loadedConfig, configPathUsed, nil
	}
	// not found -> empty config
	loadedConfig = AppConfig{}
	configLoaded = true
	configPathUsed = ""
	return loadedConfig, configPathUsed, nil
}

func Defaults() AppConfig            { return loadedConfig }
func GetDefaultRegion() string       { return strings.TrimSpace(loadedConfig.Region) }
func GetDefaultBrokers() []string    { return append([]string(nil), loadedConfig.Brokers...) }
func GetDefaultOutput() string       { return strings.TrimSpace(loadedConfig.Output) }
func GetDefaultSCRAM() string        { return strings.TrimSpace(loadedConfig.SCRAMMechanism) }
func GetDefaultKMSKeyID() string     { return strings.TrimSpace(loadedConfig.KMSKeyID) }
func GetDefaultClusterARN() string   { return strings.TrimSpace(loadedConfig.ClusterARN) }
func GetDefaultSASLUsername() string { return strings.TrimSpace(loadedConfig.SASLUsername) }
func GetDefaultSASLPassword() string { return strings.TrimSpace(loadedConfig.SASLPassword) }
