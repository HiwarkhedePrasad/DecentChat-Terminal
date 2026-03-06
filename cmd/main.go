package main

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"decentchat/internal/config"
	"decentchat/internal/identity"
	"decentchat/internal/signaling"
	"decentchat/internal/ui"
	"decentchat/internal/webrtc"
)

const VERSION = "0.1.0"

func main() {
	// Print banner
	printBanner()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Load or create identity
	id, err := identity.LoadOrCreate(cfg.DataDir)
	if err != nil {
		fmt.Printf("Error loading identity: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("User ID: %s\n", id.ShortID())
	fmt.Println("Initializing...")

	// Create signaling client
	sigClient := signaling.NewClient(cfg.SupabaseURL, cfg.SupabaseKey, id)

	// Create WebRTC manager
	webrtcMgr := webrtc.NewManager(id)

	// Create and run UI
	app := ui.NewApp(id, sigClient, webrtcMgr, VERSION)

	// Handle shutdown
	setupShutdownHandler(sigClient, id)

	// Run the terminal UI
	if _, err := app.Run(); err != nil {
		fmt.Printf("Error running app: %v\n", err)
		os.Exit(1)
	}
}

func printBanner() {
	banner := `
██████╗ ███████╗███████╗ ██████╗ ██████╗ ██████╗ ██████╗ ███╗   ██╗
██╔══██╗██╔════╝██╔════╝██╔════╝██╔═══██╗██╔══██╗██╔══██╗████╗  ██║
██║  ██║█████╗  ███████╗██║     ██║   ██║██║  ██║██║  ██║██╔██╗ ██║
██║  ██║██╔══╝  ╚════██║██║     ██║   ██║██║  ██║██║  ██║██║╚██╗██║
██████╔╝███████╗███████║╚██████╗╚██████╔╝██████╔╝██████╔╝██║ ╚████║
╚═════╝ ╚══════╝╚══════╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚═════╝ ╚═╝  ╚═══╝

`
	fmt.Println(banner)
	fmt.Printf("Version %s - Semi-Decentralized Encrypted Terminal Chat\n", VERSION)
	fmt.Println(strings.Repeat("━", 60))
}

func setupShutdownHandler(sigClient *signaling.Client, id *identity.Identity) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		fmt.Println("\nShutting down...")
		sigClient.SetOffline(id.UserID)
		os.Exit(0)
	}()
}
