package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"decentchat/internal/config"
	"decentchat/internal/identity"
	"decentchat/internal/network"
	"decentchat/internal/signaling"
	"decentchat/internal/ui"
)

const VERSION = "0.1.0"

func main() {
	if shouldExit, exitCode := handleCLIFlags(); shouldExit {
		os.Exit(exitCode)
	}

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

	// Create network manager
	networkMgr := network.NewManager(id, cfg.DataDir)

	// Start the local tunnel server
	fmt.Println("Starting Cloudflare secure tunnel...")
	tunnelURL, err := networkMgr.StartServer()
	if err != nil {
		fmt.Printf("Failed to start tunnel: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Local tunnel established: %s\n", tunnelURL)

	// Update signaling client with our tunnel URL
	sigClient.UpdateTunnelURL(tunnelURL)

	// Ensure cleanup on exit
	defer func() {
		networkMgr.Shutdown()
		sigClient.ClearTunnelURL()
		sigClient.SetOffline(id.UserID)
	}()

	// Handle shutdown
	setupShutdownHandler(sigClient, id)

	// Create and run UI
	app := ui.NewApp(id, sigClient, networkMgr, tunnelURL, VERSION)

	// Run the terminal UI
	if _, err := app.Run(); err != nil {
		fmt.Printf("Error running app: %v\n", err)
		os.Exit(1)
	}
}

func handleCLIFlags() (bool, int) {
	var showHelp bool
	var showVersion bool

	flag.BoolVar(&showHelp, "help", false, "Show help")
	flag.BoolVar(&showHelp, "h", false, "Show help")
	flag.BoolVar(&showVersion, "version", false, "Show version")
	flag.BoolVar(&showVersion, "v", false, "Show version")
	flag.Usage = printUsage
	flag.CommandLine.SetOutput(os.Stdout)

	if err := flag.CommandLine.Parse(os.Args[1:]); err != nil {
		return true, 2
	}

	if showHelp {
		printUsage()
		return true, 0
	}

	if showVersion {
		fmt.Printf("decentchat %s\n", VERSION)
		return true, 0
	}

	return false, 0
}

func printUsage() {
	fmt.Println("DecentChat - Semi-Decentralized Encrypted Terminal Chat")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  decentchat [flags]")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  -h, --help      Show help")
	fmt.Println("  -v, --version   Show version")
}

func printBanner() {
	banner := `
██████╗ ███████╗ ██████╗ ███████╗███╗   ██╗████████╗ ██████╗ ██╗  ██╗ █████╗ ████████╗
██╔══██╗██╔════╝██╔════╝ ██╔════╝████╗  ██║╚══██╔══╝██╔════╝ ██║  ██║██╔══██╗╚══██╔══╝
██║  ██║█████╗  ██║      █████╗  ██╔██╗ ██║   ██║   ██║      ███████║███████║   ██║   
██║  ██║██╔══╝  ██║      ██╔══╝  ██║╚██╗██║   ██║   ██║      ██╔══██║██╔══██║   ██║   
██████╔╝███████╗╚██████╗ ███████╗██║ ╚████║   ██║   ╚██████╗ ██║  ██║██║  ██║   ██║   
╚═════╝ ╚══════╝ ╚═════╝ ╚══════╝╚═╝  ╚═══╝   ╚═╝    ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝   ╚═╝   
`
	fmt.Println(banner)
	fmt.Printf("Version %s - Semi-Decentralized Encrypted Terminal Chat\n", VERSION)
	fmt.Println(strings.Repeat("━", 85))
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
