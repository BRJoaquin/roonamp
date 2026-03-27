package main

import (
	"fmt"
	"os"

	"roonamp/internal/config"
	"roonamp/internal/roon"
	"roonamp/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	cfg := config.Load()

	if cfg.RoonHost == "" || cfg.RoonPort == "" {
		fmt.Fprintln(os.Stderr, "Roon server address required.")
		fmt.Fprintln(os.Stderr, "Usage: roonamp -host <ip> -port <port>")
		fmt.Fprintln(os.Stderr, "   or: ROON_HOST=<ip> ROON_PORT=<port> roonamp")
		os.Exit(1)
	}

	host := cfg.RoonHost
	port := cfg.RoonPort
	fmt.Printf("Connecting to %s:%s...\n", host, port)

	token := config.LoadToken()

	client := roon.NewClient(host, port, token)
	fmt.Printf("Connecting to ws://%s:%s/api ...\n", host, port)

	if err := client.Connect(); err != nil {
		fmt.Fprintf(os.Stderr, "Connect failed: %v\n", err)
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
