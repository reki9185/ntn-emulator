package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"ntn-emulator/config"
	uelink "ntn-emulator/ue/link"
	"ntn-emulator/ue/tun"
)

func main() {
	// Parse command-line arguments
	configPath := flag.String("config", "configs/ue.yaml", "Path to UE config file")
	ueIP := flag.String("ue-ip", "", "UE IP address (from PDU session, overrides config)")
	ranAddr := flag.String("ran-addr", "", "RAN data plane address (overrides config)")
	imsi := flag.String("imsi", "", "UE IMSI (overrides config)")
	tunName := flag.String("tun", "", "TUN interface name (overrides config)")

	flag.Parse()

	// Load UE configuration
	ueCfg, err := config.LoadUEConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load UE config: %v", err)
	}

	// Use values from command line or config
	if *imsi == "" {
		*imsi = ueCfg.GetIMSI()
	}
	if *tunName == "" {
		*tunName = ueCfg.UE.UETunnelDevice
	}
	if *ranAddr == "" {
		*ranAddr = fmt.Sprintf("%s:%d", ueCfg.UE.RANDataPlaneIP, ueCfg.UE.RANDataPlanePort)
	}

	// Validate UE IP (must be provided)
	if *ueIP == "" {
		log.Println("Usage:")
		log.Println("  1. Build: go build -o /tmp/ntn_ue ./cmd/ue.go")
		log.Println("  2. Run: sudo /tmp/ntn_ue -ue-ip <UE_IP> [-config configs/ue.yaml]")
		log.Fatal("Missing required parameter: -ue-ip")
	}

	log.Println("========================================")
	log.Println("NTN UE Data Plane Process")
	log.Println("========================================")
	log.Printf("UE IP: %s\n", *ueIP)
	log.Printf("IMSI: %s\n", *imsi)
	log.Printf("TUN Interface: %s\n", *tunName)
	log.Printf("RAN Address: %s\n", *ranAddr)
	log.Printf("Config: %s\n", *configPath)
	log.Println("========================================")

	// Create TUN interface
	log.Println("\n[Step 1] Creating TUN interface...")
	tunIface, err := tun.NewTUNInterface(*tunName, *ueIP)
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()
	log.Printf("✓ TUN interface created: %s (%s)\n", *tunName, *ueIP)

	// Create UE Data Plane (simple UDP to RAN, no GTP)
	log.Println("\n[Step 2] Connecting to RAN data plane...")
	ueDataPlane, err := uelink.NewUEDataPlane(*ranAddr, *imsi, tunIface)
	if err != nil {
		tunIface.Close()
		log.Fatalf("Failed to create UE data plane: %v", err)
	}
	defer ueDataPlane.Stop()

	// Start data plane forwarding
	log.Println("\n[Step 3] Starting data plane forwarding...")
	ueDataPlane.Start()

	log.Println("\n========================================")
	log.Println("✅ UE Data Plane is ACTIVE!")
	log.Println("========================================")
	log.Println("\nYou can now test connectivity:")
	log.Printf("  sudo ping -I %s 8.8.8.8\n", *tunName)
	log.Printf("  sudo ping -I %s google.com\n", *tunName)
	log.Println("\nPress Ctrl+C to stop")

	// Setup signal handling for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for interrupt signal
	<-sigChan

	log.Println("\n\nShutting down UE...")
	log.Println("✓ UE shutdown completed")
}
