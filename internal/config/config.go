package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	RoonHost string
	RoonPort string
}

func Load() Config {
	return Config{
		RoonHost: os.Getenv("ROON_HOST"),
		RoonPort: os.Getenv("ROON_PORT"),
	}
}

func TokenPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "roonamp", "token")
}

func LoadToken() string {
	data, err := os.ReadFile(TokenPath())
	if err != nil {
		return ""
	}
	return string(data)
}

func SaveToken(token string) error {
	path := TokenPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token), 0600)
}

func ZonePath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "roonamp", "zone")
}

func LoadZone() string {
	data, err := os.ReadFile(ZonePath())
	if err != nil {
		return ""
	}
	return string(data)
}

func SaveZone(zoneID string) error {
	path := ZonePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(zoneID), 0600)
}
