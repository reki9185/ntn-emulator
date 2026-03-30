package ran

import (
	"fmt"
	"log"
	"time"

	"ntn-emulator/config"
	ntnlink "ntn-emulator/ntn-link"
	ranlink "ntn-emulator/ran/link"
	"ntn-emulator/ran/ngap"
	"ntn-emulator/util"
)

// HOState represents the handover controller state machine.
type HOState int

const (
	// HOIdle: no active connection, safe to begin a new path switch.
	HOIdle HOState = iota
	// HOInProgress: PathSwitchRequest has been sent to AMF.
	// Duplicate satellite-match triggers must be ignored in this state.
	HOInProgress
	// HOActive: path switch completed, data plane is running.
	HOActive
)

// RunHandoverController loads the NTN timeline and watches for satellite
// changes.  When the satellite matches this RAN's gnbName, it becomes ACTIVE:
//  1. Fetches UE context from the peer RAN via Xn.
//  2. Sends PathSwitchRequest to AMF.
//  3. Starts its data plane.
//  4. Puts the updated context on its own XnServer.
//
// When the satellite changes away, it becomes STANDBY:
//  1. Stops its data plane.
//
// If scheduledStartTime is provided, the timeline will wait until that absolute
// time before beginning event replay (for synchronization with other processes).
func RunHandoverController(ngapClient *ngap.NGAPClient, ranCfg *config.RANConfig, xnSrv *XnServer, xnPeerAddr string, scheduledStartTime *time.Time) {
	log.Printf("🛰️  [Controller] Watching %s for satellite=%s\n", ranCfg.GNB.NTNStateFile, ranCfg.GNB.GNBName)

	player, err := ntnlink.NewTimelinePlayer(ranCfg.GNB.NTNStateFile)
	if err != nil {
		log.Printf("❌ load NTN timeline: %v\n", err)
		return
	}

	// Set scheduled start time if provided (for synchronized timeline replay)
	if scheduledStartTime != nil {
		player.SetScheduledStartTime(*scheduledStartTime)
	}

	if err := player.Start(); err != nil {
		log.Printf("❌ start NTN timeline player: %v\n", err)
		return
	}
	defer player.Stop()

	hoState := HOIdle
	if state := player.GetCurrentState(); state != nil && state.Satellite == ranCfg.GNB.GNBName {
		hoState = HOActive
		log.Printf("✓ [Controller] Initial state matches (%s), waiting for UE registration.\n", ranCfg.GNB.GNBName)
	}

	for state := range player.GetUpdateChannel() {
		if state == nil {
			continue
		}

		if state.Event == "handover_start" {
			log.Println("🚨 [Controller] Handover START detected — freezing old data plane")

			StopDataPlane()
			continue
		}

		shouldBeActive := (state.Satellite == ranCfg.GNB.GNBName)

		// Suppress duplicate triggers while a path switch is already committed.
		if shouldBeActive && hoState == HOInProgress {
			log.Printf("⚠️  [Controller] Handover already in progress for satellite %s — ignoring duplicate trigger\n", state.Satellite)
			continue
		}
		if shouldBeActive && hoState == HOActive {
			// Already serving this satellite.
			continue
		}

		if shouldBeActive && hoState == HOIdle {
			log.Printf("✅ [Controller] Satellite match (%s)! Initiating Path Switch...\n", state.Satellite)
			// Transition to InProgress immediately — any duplicate events in the
			// channel will be suppressed by the guard above.
			hoState = HOInProgress

			if xnPeerAddr == "" {
				log.Println("⚠️  No -xn-peer configured. Cannot pull UE context!")
				hoState = HOIdle // nothing sent to network; safe to reset
				continue
			}

			// ── Fetch UE context from the other RAN via Xn ───────────────────
			ueCtx, err := FetchContextFromXn(xnPeerAddr)
			if err != nil {
				log.Printf("❌ fetch xn context: %v\n", err)
				hoState = HOIdle // nothing sent to network; safe to reset
				continue
			}
			log.Printf("✓ [Xn] UE context: IMSI=%s AmfUeNgapID=%d PDUSessionID=%d\n", ueCtx.IMSI, ueCtx.AmfUeNgapID, ueCtx.PDUSessionID)

			// ── Build NGAP parameters ────────────────────────────────────────
			plmnID, _ := util.PlmnIdToNgap(ranCfg.GNB.PLMNID.MCC, ranCfg.GNB.PLMNID.MNC)
			tac, _ := util.TacToNgap(ranCfg.GNB.TAI.TAC)
			nrCellID := []byte{0x00, 0x00, 0x00, 0x00, 0x20}

			const newRanUeNgapID = int64(100)
			newDLTEID := uint32(newRanUeNgapID)

			// ── Send PathSwitchRequest ───────────────────────────────────────
			psHandler := ngap.NewPathSwitchHandler(ngapClient)
			// log.Printf("📤 [Path Switch] Sending PathSwitchRequest (TEID=%d, time=%s)\n", newDLTEID, nowStr())
			err = psHandler.SendPathSwitchRequest(ueCtx.AmfUeNgapID, newRanUeNgapID, ueCtx.PDUSessionID, ranCfg.GNB.RANN3IP, newDLTEID, plmnID, nrCellID, tac.Value)
			if err != nil {
				log.Printf("❌ send PathSwitchRequest: %v\n", err)
				hoState = HOIdle // PSR not sent; safe to reset
				continue
			}
			log.Println("✓ [Path Switch] PathSwitchRequest sent")

			// ── Receive PathSwitchRequestAcknowledge ─────────────────────────
			// From this point the AMF/UPF has committed to the path switch.
			// Do NOT reset hoState to Idle on failure — that would allow a
			// duplicate PathSwitchRequest on the next timeline event and cause
			// the UPF to send a second GTP End Marker.
			newULTEID, upfN3IP, err := psHandler.ReceivePathSwitchRequestAcknowledge()
			if err != nil {
				log.Printf("❌ receive PathSwitchRequestAck: %v — PSR already committed, staying in InProgress to prevent duplicate End Marker\n", err)
				// hoState stays HOInProgress — duplicate triggers blocked
				continue
			}
			log.Printf("✓ [Path Switch] Ack: UPF N3=%s UL TEID=0x%08x\n", upfN3IP, newULTEID)
			if upfN3IP == "" {
				upfN3IP = ueCtx.UPFN3IP
			}
			upfAddr := fmt.Sprintf("%s:%d", upfN3IP, ueCtx.UPFPort)

			StopDataPlane()

			// ── Start RAN data plane ─────────────────────────────────────────
			log.Printf("🔌 [Path Switch] Starting data plane  UPF=%s ulTEID=0x%08x dlTEID=0x%08x\n", upfAddr, newULTEID, newDLTEID)
			dp, err := ranlink.NewRANDataPlane(
				ranCfg.GNB.RANDataPlaneIP,
				ranCfg.GNB.RANDataPlanePort,
				ranCfg.GNB.RANN3IP,
				ranCfg.GNB.RANN3Port,
				upfAddr,
				newULTEID,
				newDLTEID,
				ueCtx.IMSI,
				ranCfg.GNB.NTNStateFile,
			)
			if err != nil {
				log.Printf("❌ create data plane: %v\n", err)
				// PSR committed — stay in InProgress
				continue
			}
			if err := dp.Start(); err != nil {
				log.Printf("❌ start data plane: %v\n", err)
				// PSR committed — stay in InProgress
				continue
			}
			RegisterDataPlane(dp)

			// ── Expose Context on XnServer for future switchback ─────────────
			if xnSrv != nil {
				xnUpdate := &UEHandoverContext{
					IMSI:         ueCtx.IMSI,
					AmfUeNgapID:  ueCtx.AmfUeNgapID,
					PDUSessionID: ueCtx.PDUSessionID,
					UPFN3IP:      upfN3IP,
					UPFPort:      ueCtx.UPFPort,
					UPFTEID:      newULTEID,
				}
				xnSrv.SetContext(xnUpdate)
				log.Printf("✓ [Xn] Updated local context server so peer can fetch it later.\n")
			}

			log.Println("\n========================================")
			log.Println("✅ Path Switch Complete — RAN is ACTIVE")
			log.Printf("   UE data plane endpoint: %s:%d\n", ranCfg.GNB.RANDataPlaneIP, ranCfg.GNB.RANDataPlanePort)
			log.Println("========================================")
			hoState = HOActive

		} else if !shouldBeActive && hoState == HOActive {
			log.Println("⏸️  [Controller] Satellite lost. Entering standby mode.")
			hoState = HOIdle
			// StopDataPlane()
		}
	}
}

// RunSingleRANModeController monitors the NTN timeline and applies link parameter
// changes (delay, PDR) to a single RAN without performing handovers. This mode is
// intended for scenarios where the satellite/channel characteristics change over time,
// but the UE remains connected to a single RAN throughout the simulation.
//
// Unlike RunHandoverController, this mode:
//   - Ignores the "satellite" field in timeline events
//   - Does not perform path switches or Xn context transfers
//   - Only updates link parameters (delays, PDR) based on timeline events
//
// If scheduledStartTime is provided, the timeline will wait until that absolute
// time before beginning event replay (for synchronization with other processes).
func RunSingleRANModeController(ranCfg *config.RANConfig, scheduledStartTime *time.Time) {
	log.Printf("🛰️  [Single-RAN Mode] Starting controller for %s\n", ranCfg.GNB.NTNStateFile)
	log.Println("📡 [Single-RAN Mode] Link parameters will change dynamically, no handovers")

	player, err := ntnlink.NewTimelinePlayer(ranCfg.GNB.NTNStateFile)
	if err != nil {
		log.Printf("❌ load NTN timeline: %v\n", err)
		return
	}

	// Set scheduled start time if provided (for synchronized timeline replay)
	if scheduledStartTime != nil {
		player.SetScheduledStartTime(*scheduledStartTime)
		log.Printf("⏱️  [Single-RAN Mode] Scheduled start: %s\n", scheduledStartTime.Format("15:04:05.000"))
	}

	if err := player.Start(); err != nil {
		log.Printf("❌ start NTN timeline player: %v\n", err)
		return
	}
	defer player.Stop()

	log.Println("✅ [Single-RAN Mode] Timeline player started")

	// Initial state logging
	if state := player.GetCurrentState(); state != nil {
		log.Printf("📊 [Single-RAN Mode] Initial state: UE-RAN=%.3fms, RAN-5G=%.3fms, PDR=%.3f\n",
			state.DelayUERan, state.DelayRan5G, state.PDR)
	}

	// Monitor timeline events and log link parameter changes
	// The actual parameter updates happen automatically via callbacks registered
	// in ran/link/dataplane.go → ntnlink.Link.UpdateFromState()
	for state := range player.GetUpdateChannel() {
		if state == nil {
			continue
		}

		// Log significant link parameter changes
		log.Printf("📡 [Link Update] t=%.3fs: UE-RAN=%.3fms, RAN-5G=%.3fms, PDR=%.3f (%.1f%% success)\n",
			state.Timestamp,
			state.DelayUERan,
			state.DelayRan5G,
			state.PDR,
			state.PDR*100.0)

		// Note: In single-RAN mode, we ignore special handover events
		if state.Event == "handover_start" || state.Event == "handover_end" {
			log.Printf("⚠️  [Single-RAN Mode] Ignoring handover event '%s' in single-RAN mode\n", state.Event)
		}
	}

	log.Println("🛑 [Single-RAN Mode] Controller stopped")
}

// nowStr returns a short wall-clock timestamp string for debug logging.
func nowStr() string {
	return time.Now().Format("15:04:05.000")
}
