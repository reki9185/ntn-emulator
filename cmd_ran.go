package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	ntnemulator "ntn/ntn-emulator"
	"test"
	"test/consumerTestdata/UDM/TestGenAuthData"

	"github.com/free5gc/openapi/models"
)

const (
	// AMF and RAN addresses (3-namespace architecture)
	// Control Plane Network: 10.0.1.x
	amfN2IP   = "10.0.1.1" // AMF on host
	ranN2IP   = "10.0.1.2" // RAN Control Plane in ns3 namespace
	amfN2Port = 38412      // AMF N2 port
	ranN2Port = 38413      // RAN N2 SCTP port

	// gNB parameters
	gnbID  = "\x00\x01\x02"
	gnbTAC = 1 // Must match AMF's supportTaiList (tac: 000001)

	// RAN Data Plane parameters (3-namespace architecture)
	// User Plane Network: 10.0.2.x (UE <-> RAN)
	// Control Plane Network: 10.0.1.x (RAN <-> UPF)
	ranDataPlaneIP   = "10.0.2.1" // RAN Data Plane in ns3 (receives from UE at 10.0.2.2)
	ranDataPlanePort = 31414      // RAN Data Plane port for UE connection
	ranN3IP          = "10.0.1.2" // RAN N3 in ns3 (GTP-U to UPF)
	ranN3Port        = 2152       // RAN N3 GTP-U port
	upfN3IP          = "10.0.1.1" // UPF N3 on host
	upfN3Port        = 2152       // UPF N3 GTP-U port
	upfN3Addr        = "10.0.1.1:2152"
)

func main() {
	// Parse command-line arguments
	imsi := flag.String("imsi", "208930000000001", "UE IMSI")
	satellite := flag.String("satellite", "UNKNOWN", "Satellite name (NTN identifier)")
	ueN3IP := flag.String("ue-n3-ip", "127.0.0.100", "UE N3 IP for GTP-U (unique per UE)")
	flag.Parse()

	log.Println("========================================")
	log.Println("NTN RAN Control Plane Process")
	log.Println("========================================")
	log.Printf("UE IMSI: %s\n", *imsi)
	log.Printf("Satellite gNB: %s\n", *satellite)
	log.Printf("UE N3 IP: %s\n", *ueN3IP)
	log.Println("========================================")
	log.Println()
	log.Println("⚠️  Make sure UE is registered in free5GC webconsole first!")
	log.Printf("   IMSI: %s\n", *imsi)
	log.Println("   Webconsole: http://localhost:5000")
	log.Println()

	// Create UE Context
	supi := fmt.Sprintf("imsi-%s", *imsi)
	ue := ntnemulator.NewUEContext(supi, 1)

	// Set authentication subscription (using TestGenAuthData from free5GC)
	ue.AuthenticationSubs = test.GetAuthSubscription(
		TestGenAuthData.MilenageTestSet19.K,
		TestGenAuthData.MilenageTestSet19.OPC,
		TestGenAuthData.MilenageTestSet19.OP)

	// Create NGAP Client
	ngapClient := ntnemulator.NewNGAPClient(amfN2IP, ranN2IP, amfN2Port, ranN2Port)

	// Connect to AMF
	log.Println("\n[Step 1] Connecting to AMF...")
	if err := ngapClient.Connect(); err != nil {
		log.Fatalf("Failed to connect to AMF: %v", err)
	}
	defer ngapClient.Close()
	log.Println("✓ Connected to AMF")

	// Perform NG Setup
	log.Println("\n[Step 2] Performing NG Setup...")
	gnbName := fmt.Sprintf("NTN-SAT-%s", *satellite)
	ngSetup := ntnemulator.NewNGSetupHandler(ngapClient, []byte(gnbID), gnbName, gnbTAC)
	if err := ngSetup.PerformNGSetup(); err != nil {
		log.Fatalf("NG Setup failed: %v", err)
	}
	log.Printf("✓ NG Setup successful (gNB: %s)\n", gnbName)

	// Perform UE Registration
	log.Println("\n[Step 3] Performing UE Registration...")
	nasCodec := ntnemulator.NewNASCodec(ue)
	regHandler := ntnemulator.NewRegistrationHandler(ue, nasCodec, ngapClient)
	if err := regHandler.PerformRegistration(); err != nil {
		log.Fatalf("UE Registration failed: %v", err)
	}
	log.Printf("✓ UE Registration successful (SUPI: %s)\n", ue.Supi)

	// Perform PDU Session Establishment
	log.Println("\n[Step 4] Performing PDU Session Establishment...")
	pduHandler := ntnemulator.NewPDUSessionHandler(ue, nasCodec, ngapClient)
	snssai := &models.Snssai{Sst: 1, Sd: "010203"}

	// Pass RAN N3 IP to PDU handler (RAN will receive GTP-U downlink at this address)
	if err := pduHandler.EstablishPDUSessionForSeparateUE(1, "internet", snssai, ranN3IP); err != nil {
		log.Fatalf("PDU Session Establishment failed: %v\n", err)
	}

	log.Printf("✓ PDU Session established (ID: %d, DNN: %s)\n", 1, "internet")
	log.Printf("✓ UE IP Address: %s\n", ue.UEIPAddress)
	log.Printf("✓ RAN N3 IP (for GTP-U): %s:%d\n", ranN3IP, ranN3Port)
	log.Printf("✓ UPF TEID: 0x%08x\n", ue.UPFTEID)
	log.Printf("✓ RAN TEID: 0x%08x\n", ue.RANTEID)

	log.Println("\n========================================")
	log.Println("✅ RAN Control Plane Setup Completed!")
	log.Println("========================================")

	// Start RAN Data Plane Server (like free-ran-ue)
	log.Println("\n[Step 5] Starting RAN Data Plane Server...")
	dataPlane, err := ntnemulator.NewRANDataPlane(
		ranDataPlaneIP,
		ranDataPlanePort,
		ranN3IP,
		ranN3Port,
		upfN3Addr,
		uint32(ue.RANTEID), // UL TEID (RAN->UPF)
		uint32(ue.UPFTEID), // DL TEID (UPF->RAN)
		ue.Supi,
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

	log.Printf("✓ RAN Data Plane Server started on port %d\n", ranDataPlanePort)
	log.Printf("✓ RAN N3 GTP-U endpoint: %s:%d\n", ranN3IP, actualN3Port)
	log.Println("\nYou can now start the UE process with:")
	log.Println("\nIn Terminal 2, first build:")
	log.Println("  go build -o /tmp/ntn_ue cmd_ue.go")
	log.Println("\nThen run with sudo:")
	log.Printf("  sudo /tmp/ntn_ue -ue-ip %s -ran-addr %s:%d -imsi %s\n",
		ue.UEIPAddress, ranDataPlaneIP, ranDataPlanePort, *imsi)

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
