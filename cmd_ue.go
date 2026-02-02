package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	ntnemulator "ntn/ntn-emulator"
)

func main() {
	// Parse command-line arguments
	ueIP := flag.String("ue-ip", "", "UE IP address (from PDU session)")
	ranAddr := flag.String("ran-addr", "127.0.0.1:9487", "RAN data plane address")
	imsi := flag.String("imsi", "", "UE IMSI")
	tunName := flag.String("tun", "uetun0", "TUN interface name")

	flag.Parse()

	// Validate arguments
	if *ueIP == "" || *imsi == "" {
		log.Println("Usage:")
		log.Println("  1. Build: go build -o /tmp/ntn_ue cmd_ue.go")
		log.Println("  2. Run: sudo /tmp/ntn_ue -ue-ip <UE_IP> -ran-addr <RAN_ADDR> -imsi <IMSI>")
		log.Fatal("Missing required parameters")
	}

	log.Println("========================================")
	log.Println("NTN UE Data Plane Process (free-ran-ue pattern)")
	log.Println("========================================")
	log.Printf("UE IP: %s\n", *ueIP)
	log.Printf("IMSI: %s\n", *imsi)
	log.Printf("TUN Interface: %s\n", *tunName)
	log.Printf("RAN Address: %s\n", *ranAddr)
	log.Println("========================================")

	// Create TUN interface
	log.Println("\n[Step 1] Creating TUN interface...")
	tunIface, err := ntnemulator.NewTUNInterface(*tunName, *ueIP)
	if err != nil {
		log.Fatalf("Failed to create TUN interface: %v", err)
	}
	defer tunIface.Close()
	log.Printf("✓ TUN interface created: %s (%s)\n", *tunName, *ueIP)

	// Create UE Data Plane (simple UDP to RAN, no GTP)
	log.Println("\n[Step 2] Connecting to RAN data plane...")
	ueDataPlane, err := ntnemulator.NewUEDataPlane(*ranAddr, *imsi, tunIface)
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
