package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ntn-emulator/config"
	ntnlink "ntn-emulator/ntn-link"
	"ntn-emulator/ue"
	uelink "ntn-emulator/ue/link"
	uenas "ntn-emulator/ue/nas"
	"ntn-emulator/ue/tun"

	"github.com/free5gc/openapi/models"
)

func main() {
	// Configure log format with microsecond precision
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	processStartTime := time.Now()

	// Parse command-line arguments
	configPath := flag.String("config", "configs/ue.yaml", "Path to UE config file")
	ueIP := flag.String("ue-ip", "", "UE IP address (from PDU session, overrides config)")
	ranAddr := flag.String("ran-addr", "", "RAN data plane address (overrides config)")
	imsi := flag.String("imsi", "", "UE IMSI (overrides config)")
	tunName := flag.String("tun", "", "TUN interface name (overrides config)")
	startTimeStr := flag.String("start-time", "", "Timeline start time (UNIX timestamp or 'now+10s'). Default: start immediately")
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
	} else {
		// Default to process start so the UE's satellite watcher stays aligned
		// with the rest of the session even if it starts after registration.
		scheduledStartTime = &processStartTime
	}

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

	// ── Satellite watcher: auto-switch data plane when timeline satellite changes ──
	if ueCfg.UE.NTNStateFile != "" && len(ueCfg.UE.SatelliteRANMap) > 0 {
		satPlayer, err := ntnlink.NewTimelinePlayer(ueCfg.UE.NTNStateFile)
		if err != nil {
			log.Printf("⚠️  [UE] Failed to load NTN timeline: %v\n", err)
		} else {
			// Set scheduled start time if provided (for synchronized timeline replay)
			if scheduledStartTime != nil {
				satPlayer.SetScheduledStartTime(*scheduledStartTime)
			}

			if err := satPlayer.Start(); err != nil {
				log.Printf("⚠️  [UE] Failed to start satellite timeline player: %v\n", err)
			} else {
				defer satPlayer.Stop()
				// Record the satellite we started with so we only act on changes.
				var currentSatellite string
				if state := satPlayer.GetCurrentState(); state != nil {
					currentSatellite = state.Satellite
				}
				log.Printf("✓ [UE] Satellite timeline player started (current=%s, watching %s)\n",
					currentSatellite, ueCfg.UE.NTNStateFile)

				go func() {
					updateCh := satPlayer.GetUpdateChannel()
					for state := range updateCh {
						if state == nil || state.Satellite == currentSatellite {
							continue
						}
						newSat := state.Satellite
						entry, ok := ueCfg.UE.SatelliteRANMap[newSat]
						if !ok {
							log.Printf("⚠️  [UE] No RAN mapping for satellite %s — ignoring\n", newSat)
							currentSatellite = newSat
							continue
						}
						newAddr := fmt.Sprintf("%s:%d", entry.DataPlaneIP, entry.DataPlanePort)
						log.Printf("🛰️  [UE] Satellite changed: %s → %s\n", currentSatellite, newSat)
						log.Printf("   Switching data plane to %s (allowing path switch to complete)\n", newAddr)
						currentSatellite = newSat
						// Removed artificial delay - reconnect immediately for faster handover
						if err := ueDataPlane.Reconnect(newAddr); err != nil {
							log.Printf("❌ [UE] Data plane reconnect failed: %v\n", err)
						} else {
							log.Printf("✅ [UE] Data plane switched to %s (satellite %s)\n", newAddr, newSat)
						}
					}
				}()
			}
		}
	}

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
