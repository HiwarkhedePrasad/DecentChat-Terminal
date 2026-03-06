package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	SupabaseURL string
	SupabaseKey string
	DataDir     string
}

func Load() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dataDir := filepath.Join(homeDir, ".decentchat")
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}

	// Try to load .env file from current directory
	loadEnvFile(".env")

	cfg := &Config{
		SupabaseURL: getEnv("SUPABASE_URL", "YourKey"),
		SupabaseKey: getEnv("SUPABASE_KEY", "Your Key"),
		DataDir:     dataDir,
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func loadEnvFile(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
	}
}

