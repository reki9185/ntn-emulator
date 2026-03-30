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
	"strings"
	"syscall"
	"time"

	"ntn-emulator/config"
	"ntn-emulator/ran"
	"ntn-emulator/ran/ngap"
	"ntn-emulator/ue"
	"ntn-emulator/util"

	ngapType "github.com/free5gc/ngap/ngapType"
	"github.com/free5gc/openapi/models"
)

func main() {
	// Configure log format with microsecond precision
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)

	// Parse command-line arguments
	configPath := flag.String("config", "configs/ran.yaml", "Path to RAN config file")
	imsi := flag.String("imsi", "", "UE IMSI (overrides config)")
	ueConfigPath := flag.String("ue-config", "configs/ue.yaml", "Path to UE config file (for auth)")
	// -xn-listen: RAN-1 exposes the UE context on this TCP address after PDU
	// session establishment so RAN-2 can fetch it when performing a path switch.
	xnListen := flag.String("xn-listen", "", "TCP address for Xn context server, e.g. 127.0.0.1:9001 (source RAN only)")
	// -xn-peer: RAN-2 connects to this address to fetch UE context from RAN-1
	// when the satellite in ntn_state.json matches this RAN's gnbName.
	xnPeer := flag.String("xn-peer", "", "TCP address of RAN-1 Xn context server, e.g. 127.0.0.1:9001 (target RAN only)")
	// -start-time: Synchronize timeline start to an absolute UNIX timestamp or "now+Ns" format
	startTimeStr := flag.String("start-time", "", "Timeline start time (UNIX timestamp or 'now+10s'). Default: start immediately")
	// -single-ran: Enable single-RAN mode where link parameters change but no handovers occur
	singleRan := flag.Bool("single-ran", false, "Enable single-RAN mode (link parameters change, no handovers)")
	flag.Parse()

	// Parse scheduled start time if provided
	var scheduledStartTime *time.Time
	if *startTimeStr != "" {
		t, err := parseStartTime(*startTimeStr)
		if err != nil {
			log.Fatalf("Invalid -start-time format: %v", err)
		}
		scheduledStartTime = &t
		log.Printf("⏱️  Scheduled timeline start: %s (UNIX: %d)", t.Format("2006-01-02 15:04:05.000"), t.Unix())
	}

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
	
	// ── RAN Operation Mode Selection ───────────────────────────────────────────
	// Choose between Single-RAN mode (link params only) or Handover mode (multi-RAN)
	
	var xnSrv *ran.XnServer // Xn server for context sharing (nil in single-RAN mode)
	
	if *singleRan {
		// ── Single-RAN Mode: Dynamic link parameters, no handovers ────────────
		log.Println("\n🛰️  Single-RAN mode enabled")
		log.Println("   📡 Link parameters will change dynamically over time")
		log.Println("   ⚠️  Handover functionality disabled (ignoring -xn-listen and -xn-peer)")
		
		// Start single-RAN controller in background
		go ran.RunSingleRANModeController(ranCfg, scheduledStartTime)
		
	} else {
		// ── Multi-RAN Handover Mode: Continuous Bidirectional Handover ────────
		// Create Xn server if -xn-listen is set (serves UE context to peer RAN when needed).
		if *xnListen != "" {
			xnSrv = ran.NewXnServer(*xnListen)
			if err := xnSrv.Start(); err != nil {
				log.Fatalf("Failed to start Xn server: %v", err)
			}
			defer xnSrv.Stop()
			log.Printf("✓ Xn context server started on %s\n", *xnListen)
		}

		// Start continuous Handover Controller in the background
		if *xnPeer != "" {
			log.Printf("\n🛰️  Continuous Handover mode enabled (xn-peer=%s)\n", *xnPeer)
			go ran.RunHandoverController(ngapClient, ranCfg, xnSrv, *xnPeer, scheduledStartTime)
		} else {
			log.Printf("\n🛰️  No -xn-peer provided. Handover controller disabled. (Source-only mode)\n")
		}
	}

	// Always start the Control Plane listener to handle normal UE registrations
	// or intermediate NAS messages.

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

				handler := ran.NewUEHandler(c, ngapClient, id, plmnID, tai, ranCfg, xnSrv)
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

// parseStartTime parses a start time string in the following formats:
//   - UNIX timestamp (integer seconds): "1648000000"
//   - UNIX timestamp with fractional seconds: "1648000000.123456"
//   - Relative time from now: "now+10s", "now+5m", "now+1h"
func parseStartTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)

	// Handle "now+duration" format
	if strings.HasPrefix(s, "now+") || strings.HasPrefix(s, "now-") {
		durationStr := s[3:] // Skip "now"
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid duration in '%s': %w", s, err)
		}
		return time.Now().Add(duration), nil
	}

	// Handle UNIX timestamp (with optional fractional seconds)
	var timestamp float64
	if _, err := fmt.Sscanf(s, "%f", &timestamp); err != nil {
		return time.Time{}, fmt.Errorf("expected UNIX timestamp or 'now+duration', got '%s'", s)
	}

	sec := int64(timestamp)
	nsec := int64((timestamp - float64(sec)) * 1e9)
	return time.Unix(sec, nsec), nil
}

// insertUEDataToMongoDB inserts UE subscription data to MongoDB
