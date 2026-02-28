package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"ntn-emulator/config"
	"ntn-emulator/ran"
	ranlink "ntn-emulator/ran/link"
	"ntn-emulator/ran/ngap"
	"ntn-emulator/ue"
	uenas "ntn-emulator/ue/nas"

	"github.com/free5gc/openapi/models"
)

func main() {
	// Parse command-line arguments
	configPath := flag.String("config", "configs/ran.yaml", "Path to RAN config file")
	imsi := flag.String("imsi", "", "UE IMSI (overrides config)")
	ueConfigPath := flag.String("ue-config", "configs/ue.yaml", "Path to UE config file (for auth)")
	ueN3IP := flag.String("ue-n3-ip", "127.0.0.100", "UE N3 IP for GTP-U (unique per UE)")
	flag.Parse()

	// Load RAN configuration
	ranCfg, err := config.LoadRANConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load RAN config: %v", err)
	}

	// Load UE configuration for authentication
	ueCfg, err := config.LoadUEConfig(*ueConfigPath)
	if err != nil {
		log.Fatalf("Failed to load UE config: %v", err)
	}

	// Use IMSI from command line or config
	var imsiStr string
	if *imsi != "" {
		imsiStr = *imsi
	} else {
		imsiStr = ueCfg.GetIMSI()
	}

	// Use satellite from config if not overridden
	satellite := ranCfg.GNB.GNBName

	log.Println("========================================")
	log.Println("NTN RAN Control Plane Process")
	log.Println("========================================")
	log.Printf("UE IMSI: %s\n", imsiStr)
	log.Printf("Satellite gNB: %s\n", satellite)
	log.Printf("UE N3 IP: %s\n", *ueN3IP)
	log.Printf("Config: %s\n", *configPath)
	log.Println("========================================")
	log.Println()
	log.Println("⚠️  Make sure UE is registered in free5GC webconsole first!")
	log.Printf("   IMSI: %s\n", imsiStr)
	log.Println("   Webconsole: http://localhost:5000")
	log.Println()

	// Create UE Context
	supi := ueCfg.GetSUPI()
	uectx := ue.NewUEContext(supi, 1)

	// Set authentication subscription from config (manually to use config SQN)
	uectx.AuthenticationSubs = models.AuthenticationSubscription{
		AuthenticationMethod:          models.AuthMethod__5_G_AKA,
		EncPermanentKey:               ueCfg.UE.AuthenticationSubscription.EncPermanentKey,
		EncOpcKey:                     ueCfg.UE.AuthenticationSubscription.EncOpcKey,
		AuthenticationManagementField: ueCfg.UE.AuthenticationSubscription.AuthenticationManagementField,
		SequenceNumber: &models.SequenceNumber{
			Sqn: ueCfg.UE.AuthenticationSubscription.SequenceNumber,
		},
	}

	// Parse gNB ID from hex string
	gnbIDBytes, err := hex.DecodeString(ranCfg.GNB.GNBID)
	if err != nil {
		log.Fatalf("Invalid gNB ID in config: %v", err)
	}

	// Parse TAC from hex string
	gnbTAC, err := strconv.ParseUint(ranCfg.GNB.TAI.TAC, 16, 64)
	if err != nil {
		log.Fatalf("Invalid TAC in config: %v", err)
	}

	// Create NGAP Client with config values
	ngapClient := ngap.NewNGAPClient(
		ranCfg.GNB.AMFN2IP,
		ranCfg.GNB.RANN2IP,
		ranCfg.GNB.AMFN2Port,
		ranCfg.GNB.RANN2Port,
	)

	// Connect to AMF
	log.Println("\n[Step 1] Connecting to AMF...")
	if err := ngapClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to AMF: %v", err)
	}
	defer ngapClient.Close()
	log.Println("✓ Connected to AMF")

	// Perform NG Setup
	log.Println("\n[Step 2] Performing NG Setup...")
	gnbName := fmt.Sprintf("NTN-SAT-%s", satellite)
	ngSetup := ngap.NewNGSetupHandler(ngapClient, gnbIDBytes, gnbName, int(gnbTAC))
	if err := ngSetup.PerformNGSetup(); err != nil {
		log.Fatalf("NG Setup failed: %v", err)
	}
	log.Printf("✓ NG Setup successful (gNB: %s)\n", gnbName)

	// Perform UE Registration
	log.Println("\n[Step 3] Performing UE Registration...")
	nasCodec := uenas.NewNASCodec(uectx)
	regHandler := uenas.NewRegistrationHandler(uectx, nasCodec, ngapClient)
	if err := regHandler.PerformRegistration(); err != nil {
		log.Fatalf("UE Registration failed: %v", err)
	}
	log.Printf("✓ UE Registration successful (SUPI: %s)\n", uectx.Supi)

	// Perform PDU Session Establishment
	log.Println("\n[Step 4] Performing PDU Session Establishment...")
	pduHandler := ran.NewPDUSessionHandler(uectx, nasCodec, ngapClient)

	// Parse SNSSAI from config
	sst, err := strconv.ParseInt(ueCfg.UE.PDUSession.SNSSAI.SST, 10, 32)
	if err != nil {
		log.Fatalf("Invalid SST in config: %v", err)
	}
	snssai := &models.Snssai{Sst: int32(sst), Sd: ueCfg.UE.PDUSession.SNSSAI.SD}

	// Pass RAN N3 IP to PDU handler (RAN will receive GTP-U downlink at this address)
	if err := pduHandler.EstablishPDUSessionForSeparateUE(1, ueCfg.UE.PDUSession.DNN, snssai, ranCfg.GNB.RANN3IP); err != nil {
		log.Fatalf("PDU Session Establishment failed: %v\n", err)
	}

	log.Printf("✓ PDU Session established (ID: %d, DNN: %s)\n", 1, ueCfg.UE.PDUSession.DNN)
	log.Printf("✓ UE IP Address: %s\n", uectx.UEIPAddress)
	log.Printf("✓ RAN N3 IP (for GTP-U): %s:%d\n", ranCfg.GNB.RANN3IP, ranCfg.GNB.RANN3Port)
	log.Printf("✓ UPF TEID: 0x%08x\n", uectx.UPFTEID)
	log.Printf("✓ RAN TEID: 0x%08x\n", uectx.RANTEID)

	log.Println("\n========================================")
	log.Println("✅ RAN Control Plane Setup Completed!")
	log.Println("========================================")

	// Start RAN Data Plane Server (like free-ran-ue)
	log.Println("\n[Step 5] Starting RAN Data Plane Server...")
	upfN3Addr := fmt.Sprintf("%s:%d", ranCfg.GNB.UPFN3IP, ranCfg.GNB.UPFN3Port)
	dataPlane, err := ranlink.NewRANDataPlane(
		ranCfg.GNB.RANDataPlaneIP,
		ranCfg.GNB.RANDataPlanePort,
		ranCfg.GNB.RANN3IP,
		ranCfg.GNB.RANN3Port,
		upfN3Addr,
		uint32(uectx.UPFTEID), // UL TEID (RAN->UPF) - Use UPF-assigned TEID
		uint32(uectx.RANTEID), // DL TEID (UPF->RAN) - Use RAN-assigned TEID
		uectx.Supi,
	)
	if err != nil {
		log.Fatalf("Failed to create RAN data plane: %v", err)
	}

	// Start the data plane processing
	if err := dataPlane.Start(); err != nil {
		log.Fatalf("Failed to start RAN data plane: %v", err)
	}
	defer dataPlane.Stop()

	// Get actual N3 port assigned by kernel
	actualN3Port := dataPlane.GetN3Port()

	log.Printf("✓ RAN Data Plane Server started on port %d\n", ranCfg.GNB.RANDataPlanePort)
	log.Printf("✓ RAN N3 GTP-U endpoint: %s:%d\n", ranCfg.GNB.RANN3IP, actualN3Port)
	log.Println("\nYou can now start the UE process with:")
	log.Println("\nIn Terminal 2, first build:")
	log.Println("  go build -o /tmp/ntn_ue ./cmd/ue.go")
	log.Println("\nThen run with sudo:")
	log.Printf("  sudo /tmp/ntn_ue -ue-ip %s -ran-addr %s:%d -imsi %s\n",
		uectx.UEIPAddress, ranCfg.GNB.RANDataPlaneIP, ranCfg.GNB.RANDataPlanePort, imsiStr)

	log.Println("\n📡 RAN is ACTIVE (Control + Data Plane) - Press Ctrl+C to stop")

	// Setup signal handling for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for interrupt signal
	<-sigChan

	log.Println("\n\nShutting down RAN...")
	time.Sleep(100 * time.Millisecond)
	log.Println("✓ RAN shutdown completed")
}

// insertUEDataToMongoDB inserts UE subscription data to MongoDB
