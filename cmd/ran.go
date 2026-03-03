package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"ntn-emulator/util"
	"ntn-emulator/config"
	"ntn-emulator/ran"
	"ntn-emulator/ran/ngap"
	"ntn-emulator/ue"

	ngapType "github.com/free5gc/ngap/ngapType"
	"github.com/free5gc/openapi/models"
)

func main() {
	// Parse command-line arguments
	configPath := flag.String("config", "configs/ran.yaml", "Path to RAN config file")
	imsi := flag.String("imsi", "", "UE IMSI (overrides config)")
	ueConfigPath := flag.String("ue-config", "configs/ue.yaml", "Path to UE config file (for auth)")
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

	log.Println("\n========================================")
	log.Println("✅ RAN-AMF Connection Established!")
	log.Println("========================================")

	// Prepare PLMN ID and TAI for UE handler from config
	plmnID, err := util.PlmnIdToNgap(ranCfg.GNB.PLMNID.MCC, ranCfg.GNB.PLMNID.MNC)
	if err != nil {
		log.Fatalf("Failed to encode PLMN ID: %v", err)
	}

	tac, err := util.TacToNgap(ranCfg.GNB.TAI.TAC)
	if err != nil {
		log.Fatalf("Failed to encode TAC: %v", err)
	}

	tai := ngapType.TAI{
		PLMNIdentity: plmnID,
		TAC:          tac,
	}

	// Start RAN Control Plane Server to accept UE connections
	log.Println("\n[Step 3] Starting RAN Control Plane Server...")
	ranControlPlaneAddr := fmt.Sprintf("%s:%d", ranCfg.GNB.RANDataPlaneIP, ranCfg.GNB.RANControlPlanePort)
	listener, err := net.Listen("tcp", ranControlPlaneAddr)
	if err != nil {
		log.Fatalf("Failed to start control plane listener: %v", err)
	}
	defer listener.Close()
	log.Printf("✓ RAN Control Plane listening on %s\n", ranControlPlaneAddr)

	// Handle UE connections
	ranUeNgapID := int64(1) // Simple counter for RAN UE NGAP ID

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("Error accepting UE connection: %v\n", err)
				continue
			}
			log.Printf("✓ New UE connection from: %s\n", conn.RemoteAddr())

			// Handle each UE in a goroutine
			currentID := ranUeNgapID
			ranUeNgapID++

			go func(c net.Conn, id int64) {
				defer c.Close()

				handler := ran.NewUEHandler(c, ngapClient, id, plmnID, tai, ranCfg)
				if err := handler.HandleRegistration(); err != nil {
					log.Printf("❌ UE registration failed: %v\n", err)
				} else {
					log.Println("✓ UE registered successfully")
				}
			}(conn, currentID)
		}
	}()

	log.Println("")
	log.Println("==================================================")
	log.Println("📡 RAN is READY - Waiting for UE...")
	log.Println("==================================================")
	log.Println("")
	log.Println("RAN Services:")
	log.Printf("  Control Plane: %s:%d\n", ranCfg.GNB.RANDataPlaneIP, ranCfg.GNB.RANControlPlanePort)
	log.Printf("  Data Plane: %s:%d\n", ranCfg.GNB.RANDataPlaneIP, ranCfg.GNB.RANDataPlanePort)
	log.Println("")
	log.Println("You can now start the UE process with:")
	log.Println("  go build -o /tmp/ntn_ue ./cmd/ue.go")
	log.Println("  sudo ip netns exec ue_ns /tmp/ntn_ue")
	log.Println("\n📡 Press Ctrl+C to stop RAN")

	// Setup signal handling for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Wait for interrupt signal
	<-sigChan

	log.Println("\n\n========================================")
	log.Println("Initiating Graceful Shutdown...")
	log.Println("========================================")

	// Close NGAP connection
	log.Println("\n[Shutdown Step 1] Closing NGAP connection...")
	ngapClient.Close()
	log.Println("✓ NGAP connection closed")

	log.Println("\n========================================")
	log.Println("✓ RAN shutdown completed gracefully")
	log.Println("========================================")
}

// insertUEDataToMongoDB inserts UE subscription data to MongoDB
