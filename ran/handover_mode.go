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

// RunHandoverController continuously monitors ntn_state.json.
// When the satellite matches this RAN's gnbName, it becomes ACTIVE:
//  1. Fetches UE context from the peer RAN via Xn.
//  2. Sends PathSwitchRequest to AMF.
//  3. Starts its data plane.
//  4. Puts the updated context on its own XnServer.
// When the satellite changes away, it becomes STANDBY:
//  1. Stops its data plane.
func RunHandoverController(ngapClient *ngap.NGAPClient, ranCfg *config.RANConfig, xnSrv *XnServer, xnPeerAddr string) {
	log.Printf("🛰️  [Controller] Watching %s for satellite=%s\n", ranCfg.GNB.NTNStateFile, ranCfg.GNB.GNBName)

	watcher := ntnlink.NewJSONWatcher(ranCfg.GNB.NTNStateFile, 200*time.Millisecond)
	if err := watcher.Start(); err != nil {
		log.Printf("❌ start NTN watcher: %v\n", err)
		return
	}
	defer watcher.Stop()

	isActive := false
	if state := watcher.GetCurrentState(); state != nil && state.Satellite == ranCfg.GNB.GNBName {
		isActive = true
		log.Printf("✓ [Controller] Initial state matches (%s), waiting for UE registration.\n", ranCfg.GNB.GNBName)
	}

	for state := range watcher.GetUpdateChannel() {
		if state == nil {
			continue
		}

		shouldBeActive := (state.Satellite == ranCfg.GNB.GNBName)
		
		if shouldBeActive && !isActive {
			log.Printf("✅ [Controller] Satellite match (%s)! Initiating Path Switch...\n", state.Satellite)
			isActive = true

			if xnPeerAddr == "" {
				log.Println("⚠️  No -xn-peer configured. Cannot pull UE context!")
				continue
			}

			// ── Fetch UE context from the other RAN via Xn ───────────────────
			ueCtx, err := FetchContextFromXn(xnPeerAddr)
			if err != nil {
				log.Printf("❌ fetch xn context: %v\n", err)
				isActive = false
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
			err = psHandler.SendPathSwitchRequest(ueCtx.AmfUeNgapID, newRanUeNgapID, ueCtx.PDUSessionID, ranCfg.GNB.RANN3IP, newDLTEID, plmnID, nrCellID, tac.Value)
			if err != nil {
				log.Printf("❌ send PathSwitchRequest: %v\n", err)
				isActive = false
				continue
			}
			log.Println("✓ [Path Switch] PathSwitchRequest sent")

			// ── Receive PathSwitchRequestAcknowledge ─────────────────────────
			newULTEID, upfN3IP, err := psHandler.ReceivePathSwitchRequestAcknowledge()
			if err != nil {
				log.Printf("❌ receive PathSwitchRequestAck: %v\n", err)
				isActive = false
				continue
			}
			log.Printf("✓ [Path Switch] Ack: UPF N3=%s UL TEID=0x%08x\n", upfN3IP, newULTEID)
			if upfN3IP == "" {
				upfN3IP = ueCtx.UPFN3IP
			}
			upfAddr := fmt.Sprintf("%s:%d", upfN3IP, ueCtx.UPFPort)

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
				isActive = false
				continue
			}
			if err := dp.Start(); err != nil {
				log.Printf("❌ start data plane: %v\n", err)
				isActive = false
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

		} else if !shouldBeActive && isActive {
			log.Println("⏸️  [Controller] Satellite lost. Entering standby mode.")
			isActive = false
			StopDataPlane()
		}
	}
}
