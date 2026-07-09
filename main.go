package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"roonamp/internal/config"
	"roonamp/internal/roon"
	"roonamp/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	// Discard log output so it doesn't corrupt the TUI.
	// Use -debug flag to log to ~/.config/roonamp/debug.log instead.
	log.SetOutput(io.Discard)

	cfg := config.Load()
	token := config.LoadToken()

	client, err := connectClient(cfg, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	info, err := client.GetInfo()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Get info failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Core: %s (v%s)\n", info.DisplayName, info.DisplayVersion)

	fmt.Println("Registering extension...")
	if token == "" {
		fmt.Println(">> Go to Roon Settings -> Extensions and enable 'roonamp' <<")
	}

	reg, err := client.Register()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Register failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Registered! Core: %s\n", reg.DisplayName)

	if client.Token() != "" {
		config.SaveToken(client.Token())
	}

	if err := client.SubscribeZones(); err != nil {
		fmt.Fprintf(os.Stderr, "Subscribe zones failed: %v\n", err)
		os.Exit(1)
	}

	// Launch TUI
	m := tui.NewModel(client)
	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

// connectClient resolves the Roon Core address and returns a connected client.
// Resolution order: explicit -host/-port (or env vars), then the last
// successfully used address cached in ~/.config/roonamp/server, then SOOD
// network discovery. The cached address is refreshed on every successful
// connect, so a router-assigned IP change only costs one discovery round.
func connectClient(cfg config.Config, token string) (*roon.Client, error) {
	try := func(host, port string) (*roon.Client, error) {
		fmt.Printf("Connecting to ws://%s:%s/api ...\n", host, port)
		c := roon.NewClient(host, port, token)
		if err := c.Connect(); err != nil {
			return nil, fmt.Errorf("connect failed: %w", err)
		}
		config.SaveServer(host, port)
		return c, nil
	}

	if cfg.RoonHost != "" && cfg.RoonPort != "" {
		return try(cfg.RoonHost, cfg.RoonPort)
	}

	if host, port := config.LoadServer(); host != "" {
		c, err := try(host, port)
		if err == nil {
			return c, nil
		}
		fmt.Printf("Cached server %s:%s unreachable, discovering...\n", host, port)
	}

	fmt.Println("Discovering Roon Core on the network...")
	cores, err := roon.Discover(3 * time.Second)
	if err != nil {
		return nil, fmt.Errorf("discovery failed: %w", err)
	}
	if len(cores) == 0 {
		return nil, fmt.Errorf("no Roon Core found on the network\nUsage: roonamp -host <ip> -port <port>\n   or: ROON_HOST=<ip> ROON_PORT=<port> roonamp")
	}
	if len(cores) > 1 {
		fmt.Println("Multiple Roon Cores found:")
		for _, c := range cores {
			fmt.Printf("  %s (%s:%s)\n", c.DisplayName, c.IP, c.HTTPPort)
		}
		fmt.Println("Using the first one; pass -host/-port to pick another.")
	}
	core := cores[0]
	fmt.Printf("Found: %s at %s:%s\n", core.DisplayName, core.IP, core.HTTPPort)
	return try(core.IP, core.HTTPPort)
}
