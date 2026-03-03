package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"ntn-emulator/config"
	"ntn-emulator/ue"
	uelink "ntn-emulator/ue/link"
	uenas "ntn-emulator/ue/nas"
	"ntn-emulator/ue/tun"

	"github.com/free5gc/openapi/models"
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
		*ueIP = ueCfg.UE.UEIP

		// Exit if UE IP is null after checking config
		if *ueIP == "" {
			log.Fatal("Missing UE IP: please provide via -ue-ip flag or ueIp in config")
		}
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
	log.Printf("✓ Connected to RAN at %s\n", *ranAddr)

	// Create UE Context for registration
	log.Println("\n[Step 2.5] Creating UE context...")
	supi := ueCfg.GetSUPI()
	uectx := ue.NewUEContext(supi, 1)

	// Set authentication subscription from config
	uectx.AuthenticationSubs = models.AuthenticationSubscription{
		AuthenticationMethod:          models.AuthMethod__5_G_AKA,
		EncPermanentKey:               ueCfg.UE.AuthenticationSubscription.EncPermanentKey,
		EncOpcKey:                     ueCfg.UE.AuthenticationSubscription.EncOpcKey,
		AuthenticationManagementField: ueCfg.UE.AuthenticationSubscription.AuthenticationManagementField,
		SequenceNumber: &models.SequenceNumber{
			Sqn: ueCfg.UE.AuthenticationSubscription.SequenceNumber,
		},
	}

	// Connect to RAN Control Plane (TCP)
	log.Println("\n[Step 2.6] Connecting to RAN Control Plane...")
	ranControlPlaneAddr := fmt.Sprintf("%s:%d", ueCfg.UE.RANControlPlaneIP, ueCfg.UE.RANControlPlanePort)
	ranControlPlaneConn, err := net.Dial("tcp", ranControlPlaneAddr)
	if err != nil {
		log.Fatalf("Failed to connect to RAN control plane: %v", err)
	}
	defer ranControlPlaneConn.Close()
	log.Printf("✓ Connected to RAN Control Plane at %s\n", ranControlPlaneAddr)

	// Create NAS codec
	nasCodec := uenas.NewNASCodec(uectx)

	// Perform UE Registration (via RAN control plane)
	log.Println("\n[Step 2.7] Performing UE Registration...")
	regHandler := uenas.NewRegistrationHandler(uectx, nasCodec, ranControlPlaneConn)
	if err := regHandler.PerformRegistration(); err != nil {
		log.Fatalf("Registration failed: %v", err)
	}
	log.Println("✓ UE Registration completed successfully")

	// Perform PDU Session Establishment
	log.Println("\n[Step 2.8] Establishing PDU Session...")
	pduSessionHandler := uenas.NewPDUSessionHandler(uectx, nasCodec, ranControlPlaneConn)

	// Get PDU session parameters from config
	pduSessionID := uint8(1)
	dnn := ueCfg.UE.PDUSession.DNN
	sNssai := &models.Snssai{
		Sst: int32(1),
		Sd:  ueCfg.UE.PDUSession.SNSSAI.SD,
	}

	if err := pduSessionHandler.EstablishPDUSession(pduSessionID, dnn, sNssai); err != nil {
		log.Fatalf("PDU Session Establishment failed: %v", err)
	}
	log.Println("✓ PDU Session Establishment completed")

	// Update TUN interface with IP received from PDU session
	if uectx.UEIPAddress != "" && uectx.UEIPAddress != *ueIP {
		log.Printf("\n[Step 2.9] Updating TUN interface with assigned IP: %s\n", uectx.UEIPAddress)
		if err := tunIface.UpdateIP(uectx.UEIPAddress); err != nil {
			log.Fatalf("Failed to update TUN IP: %v", err)
		}
		log.Printf("✓ TUN interface updated to %s\n", uectx.UEIPAddress)
		*ueIP = uectx.UEIPAddress // Update for logging
	}

	// Store UE IP and TEIDs for data plane
	log.Printf("✓ UE IP Address: %s\n", uectx.UEIPAddress)
	log.Printf("✓ UPF TEID: %d\n", uectx.UPFTEID)
	log.Printf("✓ RAN TEID: %d\n", uectx.RANTEID)

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

	// Perform deregistration
	log.Println("Performing UE deregistration...")
	deregHandler := uenas.NewDeregistrationHandler(uectx, nasCodec, ranControlPlaneConn)
	if err := deregHandler.PerformDeregistration(false); err != nil {
		log.Printf("Warning: Deregistration failed: %v\n", err)
	} else {
		log.Println("✓ UE deregistration completed")
	}

	log.Println("✓ UE shutdown completed")
}
