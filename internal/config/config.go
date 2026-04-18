package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	API       string
	Web       string
	TokenPath string
	Parallel  int
	DryRun    bool
}

func Load() (*Config, error) {
	tokenPath, err := defaultTokenPath()
	if err != nil {
		return nil, err
	}
	if t := os.Getenv("CHAAGA_TOKEN"); t != "" {
		tokenPath = t
	}

	parallel := 8
	if p := os.Getenv("PARALLEL"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 && n <= 32 {
			parallel = n
		}
	}

	return &Config{
		API:       envOrDefault("CHAAGA_API", "https://chaaga-api.fly.dev"),
		Web:       envOrDefault("CHAAGA_WEB", "https://auth.chaaga.com"),
		TokenPath: tokenPath,
		Parallel:  parallel,
		DryRun:    os.Getenv("DRY_RUN") == "1",
	}, nil
}

func defaultTokenPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "chaaga", "token"), nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
