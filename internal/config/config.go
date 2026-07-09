package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	RoonHost string
	RoonPort string
}

func Load() Config {
	host := flag.String("host", "", "Roon Core host/IP address")
	port := flag.String("port", "", "Roon Core HTTP port")
	flag.Parse()

	cfg := Config{
		RoonHost: *host,
		RoonPort: *port,
	}

	// Env vars as fallback if flags not set
	if cfg.RoonHost == "" {
		cfg.RoonHost = os.Getenv("ROON_HOST")
	}
	if cfg.RoonPort == "" {
		cfg.RoonPort = os.Getenv("ROON_PORT")
	}

	return cfg
}

// configPath returns the path of a named file inside the roonamp config
// directory (XDG_CONFIG_HOME or ~/.config).
func configPath(name string) string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "roonamp", name)
}

// loadString reads a single-value config file, trimming surrounding
// whitespace so hand-edited files with trailing newlines still work.
// Returns "" if the file doesn't exist.
func loadString(name string) string {
	data, err := os.ReadFile(configPath(name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func saveString(name, value string) error {
	path := configPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value), 0600)
}

func LoadToken() string            { return loadString("token") }
func SaveToken(token string) error { return saveString("token", token) }

func LoadZone() string             { return loadString("zone") }
func SaveZone(zoneID string) error { return saveString("zone", zoneID) }

// LoadServer returns the last successfully connected host and port,
// or empty strings if none has been cached yet.
func LoadServer() (host, port string) {
	host, port, ok := strings.Cut(loadString("server"), ":")
	if !ok {
		return "", ""
	}
	return host, port
}

func SaveServer(host, port string) error {
	return saveString("server", host+":"+port)
}

func LoadShowArt() bool {
	v := loadString("prefs")
	if v == "" {
		return true // default: show art
	}
	return v != "0"
}

func SaveShowArt(show bool) error {
	val := "1"
	if !show {
		val = "0"
	}
	return saveString("prefs", val)
}
