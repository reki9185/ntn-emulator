package ran

import (
	"fmt"
	"log"
	"net"

	"ntn-emulator/config"
	ranlink "ntn-emulator/ran/link"
	"ntn-emulator/ran/ngap"
	"ntn-emulator/util"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/ngap/ngapType"
)

// UEHandler handles a single UE's registration flow via control plane
type UEHandler struct {
	conn        net.Conn
	ngapClient  *ngap.NGAPClient
	ranUeNgapID int64
	amfUeNgapID int64
	plmnID      ngapType.PLMNIdentity
	tai         ngapType.TAI
	mobileIMSI  string
	ranConfig   *config.RANConfig
}

// NewUEHandler creates a new UE handler
func NewUEHandler(conn net.Conn, ngapClient *ngap.NGAPClient, ranUeNgapID int64, plmnID ngapType.PLMNIdentity, tai ngapType.TAI, ranConfig *config.RANConfig) *UEHandler {
	return &UEHandler{
		conn:        conn,
		ngapClient:  ngapClient,
		ranUeNgapID: ranUeNgapID,
		plmnID:      plmnID,
		tai:         tai,
		ranConfig:   ranConfig,
	}
}

// HandleRegistration processes the complete UE registration procedure
func (h *UEHandler) HandleRegistration() error {
	log.Println("[RAN->UE] Starting UE registration handling...")

	// Step 1: Receive UE Registration Request (plain NAS)
	ueRegRequest, err := util.ReadMessage(h.conn)
	if err != nil {
		return fmt.Errorf("failed to receive registration request from UE: %w", err)
	}
	log.Printf("[RAN<-UE] Received %d bytes of registration request\n", len(ueRegRequest))

	// Extract Mobile Identity from NAS message
	nasMsg := nas.NewMessage()
	if err := nasMsg.GmmMessageDecode(&ueRegRequest); err != nil {
		return fmt.Errorf("failed to decode registration request: %w", err)
	}
	if nasMsg.GmmMessage.RegistrationRequest != nil {
		mobileID := nasMsg.GmmMessage.RegistrationRequest.MobileIdentity5GS
		h.mobileIMSI = extractIMSIFromMobileIdentity(mobileID)
		log.Printf("[RAN] Mobile Identity buffer (hex): %x\n", mobileID.Buffer)
		log.Printf("[RAN] Extracted IMSI: %s\n", h.mobileIMSI)
	}

	// Send Initial UE Message to AMF (wraps NAS in NGAP)
	if err := h.ngapClient.SendInitialUEMessage(h.ranUeNgapID, ueRegRequest, h.plmnID, h.tai); err != nil {
		return fmt.Errorf("failed to send initial UE message to AMF: %w", err)
	}
	log.Println("[RAN->AMF] Sent Initial UE Message")

	// Step 2: Receive Authentication Request from AMF, forward to UE
	nasAuthReq, amfID, _, err := h.ngapClient.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive auth request from AMF: %w", err)
	}
	if amfID != nil {
		h.amfUeNgapID = *amfID
	}
	log.Printf("[RAN<-AMF] Received %d bytes of authentication request\n", len(nasAuthReq))

	if err := util.WriteMessage(h.conn, nasAuthReq); err != nil {
		return fmt.Errorf("failed to send auth request to UE: %w", err)
	}
	log.Println("[RAN->UE] Forwarded authentication request")

	// Step 3: Receive Authentication Response from UE, forward to AMF
	ueAuthResp, err := util.ReadMessage(h.conn)
	if err != nil {
		return fmt.Errorf("failed to receive auth response from UE: %w", err)
	}
	log.Printf("[RAN<-UE] Received %d bytes of authentication response\n", len(ueAuthResp))

	if err := h.ngapClient.SendUplinkNASTransport(h.amfUeNgapID, h.ranUeNgapID, ueAuthResp); err != nil {
		return fmt.Errorf("failed to send auth response to AMF: %w", err)
	}
	log.Println("[RAN->AMF] Forwarded authentication response")

	// Step 4: Receive Security Mode Command from AMF, forward to UE
	nasSecCmd, _, _, err := h.ngapClient.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive security mode command from AMF: %w", err)
	}
	log.Printf("[RAN<-AMF] Received %d bytes of security mode command\n", len(nasSecCmd))

	if err := util.WriteMessage(h.conn, nasSecCmd); err != nil {
		return fmt.Errorf("failed to send security mode command to UE: %w", err)
	}
	log.Println("[RAN->UE] Forwarded security mode command")

	// Step 5: Receive Security Mode Complete from UE, forward to AMF
	ueSecComplete, err := util.ReadMessage(h.conn)
	if err != nil {
		return fmt.Errorf("failed to receive security mode complete from UE: %w", err)
	}
	log.Printf("[RAN<-UE] Received %d bytes of security mode complete\n", len(ueSecComplete))

	if err := h.ngapClient.SendUplinkNASTransport(h.amfUeNgapID, h.ranUeNgapID, ueSecComplete); err != nil {
		return fmt.Errorf("failed to send security mode complete to AMF: %w", err)
	}
	log.Println("[RAN->AMF] Forwarded security mode complete")

	// Step 6: Loop to handle intermediate NAS messages (e.g., Identity Request)
	// until we receive Initial Context Setup Request with Registration Accept
	for {
		log.Println("[RAN] DEBUG: Waiting for message from AMF...")
		nasMessage, _, _, isInitialContextSetup, err := h.ngapClient.ReceiveNASPDUWithType()
		if err != nil {
			return fmt.Errorf("failed to receive NAS message from AMF: %w", err)
		}
		log.Printf("[RAN] DEBUG: Received %d bytes from AMF (isInitialContextSetup=%v)\n", len(nasMessage), isInitialContextSetup)

		// Forward NAS message to UE
		log.Println("[RAN] DEBUG: Forwarding NAS message to UE...")
		if err := util.WriteMessage(h.conn, nasMessage); err != nil {
			return fmt.Errorf("failed to forward NAS message to UE: %w", err)
		}
		log.Println("[RAN->UE] Forwarded NAS message")

		// Receive response from UE
		log.Println("[RAN] DEBUG: Waiting for response from UE...")
		ueResponse, err := util.ReadMessage(h.conn)
		if err != nil {
			return fmt.Errorf("failed to receive response from UE: %w", err)
		}
		log.Printf("[RAN<-UE] Received %d bytes from UE\n", len(ueResponse))

		// Forward response to AMF
		log.Println("[RAN] DEBUG: Forwarding UE response to AMF...")
		if err := h.ngapClient.SendUplinkNASTransport(h.amfUeNgapID, h.ranUeNgapID, ueResponse); err != nil {
			return fmt.Errorf("failed to forward UE response to AMF: %w", err)
		}
		log.Println("[RAN->AMF] Forwarded UE response")

		// If this was Initial Context Setup Request (Registration Accept), we're done
		if isInitialContextSetup {
			log.Println("[RAN] DEBUG: Initial Context Setup Request received, exiting message loop")
			break
		}

		log.Println("[RAN] DEBUG: Downlink NAS Transport received, continuing message loop...")
	}

	// Step 7: Send Initial Context Setup Response to AMF
	if err := h.ngapClient.SendInitialContextSetupResponse(h.amfUeNgapID, h.ranUeNgapID); err != nil {
		return fmt.Errorf("failed to send initial context setup response: %w", err)
	}
	log.Println("[RAN->AMF] Sent InitialContextSetupResponse")

	log.Println("✓ UE Registration completed successfully")

	// Step 8: Continue handling post-registration NAS messages (Configuration Update, etc.)
	log.Println("[RAN] Continuing to handle post-registration messages...")

	// Receive Configuration Update Command from AMF
	log.Println("[RAN] DEBUG: Waiting for Configuration Update Command...")
	nasConfigUpdate, _, _, err := h.ngapClient.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive configuration update: %w", err)
	}
	log.Printf("[RAN<-AMF] Received %d bytes (Configuration Update Command)\n", len(nasConfigUpdate))

	// Forward to UE
	if err := util.WriteMessage(h.conn, nasConfigUpdate); err != nil {
		return fmt.Errorf("failed to send configuration update to UE: %w", err)
	}
	log.Println("[RAN->UE] Forwarded Configuration Update Command")

	// Receive Configuration Update Complete from UE
	ueConfigUpdateComplete, err := util.ReadMessage(h.conn)
	if err != nil {
		return fmt.Errorf("failed to receive configuration update complete from UE: %w", err)
	}
	log.Printf("[RAN<-UE] Received %d bytes (Configuration Update Complete)\n", len(ueConfigUpdateComplete))

	// Forward to AMF
	if err := h.ngapClient.SendUplinkNASTransport(h.amfUeNgapID, h.ranUeNgapID, ueConfigUpdateComplete); err != nil {
		return fmt.Errorf("failed to send configuration update complete to AMF: %w", err)
	}
	log.Println("[RAN->AMF] Forwarded Configuration Update Complete")

	log.Println("✓ Configuration Update completed")

	// Step 9: Handle PDU Session Establishment
	log.Println("\n[RAN] Starting PDU Session Establishment flow...")

	// Receive PDU Session Establishment Request from UE (wrapped in UL NAS Transport)
	log.Println("[RAN] DEBUG: Waiting for PDU Session Establishment Request from UE...")
	uePDUSessionReq, err := util.ReadMessage(h.conn)
	if err != nil {
		return fmt.Errorf("failed to receive PDU session establishment request from UE: %w", err)
	}
	log.Printf("[RAN<-UE] Received %d bytes (PDU Session Establishment Request)\n", len(uePDUSessionReq))

	// Forward to AMF via Uplink NAS Transport
	if err := h.ngapClient.SendUplinkNASTransport(h.amfUeNgapID, h.ranUeNgapID, uePDUSessionReq); err != nil {
		return fmt.Errorf("failed to forward PDU session establishment request to AMF: %w", err)
	}
	log.Println("[RAN->AMF] Forwarded PDU Session Establishment Request")

	// Receive PDU Session Resource Setup Request from AMF
	log.Println("[RAN] DEBUG: Waiting for PDU Session Resource Setup Request from AMF...")
	pduSessionSetupInfo, err := h.ngapClient.ReceivePDUSessionResourceSetupRequest()
	if err != nil {
		return fmt.Errorf("failed to receive PDU session resource setup request: %w", err)
	}
	log.Printf("[RAN<-AMF] Received PDU Session Resource Setup Request (UE IP: %s, UPF TEID: %d)\n",
		pduSessionSetupInfo.UEIPAddress, pduSessionSetupInfo.UPFTEID)

	// Allocate RAN's own TEID for uplink (RAN->UPF)
	// The TEID from UPF is for downlink (UPF->RAN)
	ranUplinkTEID := uint32(h.ranUeNgapID) // Simple allocation: use RAN UE NGAP ID as TEID

	// Send PDU Session Resource Setup Response to AMF with RAN's uplink TEID
	// Use RAN's N3 IP (user plane IP) not N2 IP (control plane IP)
	log.Println("[RAN] DEBUG: Sending PDU Session Resource Setup Response...")
	if err := h.ngapClient.SendPDUSessionResourceSetupResponseWithIP(h.amfUeNgapID, h.ranUeNgapID, pduSessionSetupInfo.PDUSessionID, ranUplinkTEID, h.ranConfig.GNB.RANN3IP); err != nil {
		return fmt.Errorf("failed to send PDU session resource setup response: %w", err)
	}
	log.Println("[RAN->AMF] Sent PDU Session Resource Setup Response")

	// Forward PDU Session Establishment Accept to UE (if NAS PDU present)
	if pduSessionSetupInfo.NASPdu != nil && len(pduSessionSetupInfo.NASPdu) > 0 {
		log.Println("[RAN] DEBUG: Forwarding PDU Session Establishment Accept to UE...")
		if err := util.WriteMessage(h.conn, pduSessionSetupInfo.NASPdu); err != nil {
			return fmt.Errorf("failed to forward PDU session establishment accept to UE: %w", err)
		}
		log.Println("[RAN->UE] Forwarded PDU Session Establishment Accept")
	}

	log.Println("✓ PDU Session Establishment completed")
	log.Printf("  UE IP: %s\n", pduSessionSetupInfo.UEIPAddress)
	log.Printf("  UPF Address: %s:%d\n", pduSessionSetupInfo.UPFAddress, pduSessionSetupInfo.UPFPort)
	log.Printf("  UPF TEID (ulTEID, RAN->UPF): %d\n", pduSessionSetupInfo.UPFTEID)
	log.Printf("  RAN TEID (dlTEID, UPF->RAN): %d\n", ranUplinkTEID)

	// Step 10: Start RAN Data Plane for this UE
	log.Println("\n[RAN] Starting Data Plane...")
	upfAddr := fmt.Sprintf("%s:%d", pduSessionSetupInfo.UPFAddress, pduSessionSetupInfo.UPFPort)

	dataPlane, err := ranlink.NewRANDataPlane(
		h.ranConfig.GNB.RANDataPlaneIP,
		h.ranConfig.GNB.RANDataPlanePort,
		h.ranConfig.GNB.RANN3IP,
		h.ranConfig.GNB.RANN3Port,
		upfAddr,
		pduSessionSetupInfo.UPFTEID, // ulTEID: UPF's allocated TEID, placed in GTP header when RAN sends to UPF
		ranUplinkTEID,               // dlTEID: RAN-advertised TEID, UPF places this in GTP header when sending to RAN
		h.mobileIMSI,
		"ntn_state.json",
	)
	if err != nil {
		return fmt.Errorf("failed to create data plane: %w", err)
	}

	if err := dataPlane.Start(); err != nil {
		return fmt.Errorf("failed to start data plane: %w", err)
	}
	defer dataPlane.Stop()

	log.Println("✓ RAN Data Plane started successfully")
	log.Println("\n========================================")
	log.Println("✅ UE Session Active - Data Plane Running")
	log.Println("========================================")
	log.Println("  The UE can now send/receive data packets")
	log.Println("  Press Ctrl+C to stop or wait for UE disconnect")
	log.Println("========================================")

	// Keep data plane running and handle deregistration
	if err := h.handleDeregistration(); err != nil {
		log.Printf("[RAN] Deregistration error: %v\n", err)
	}

	return nil
}

// handleDeregistration waits for UE deregistration request and completes the flow
func (h *UEHandler) handleDeregistration() error {
	log.Println("[RAN] Waiting for UE deregistration...")

	// Step 1: Receive Deregistration Request from UE (plain NAS via TCP)
	ueDeregRequest, err := util.ReadMessage(h.conn)
	if err != nil {
		// Connection closed — UE disconnected without deregistering (e.g. switch-off)
		log.Printf("[RAN] UE connection closed (switch-off or crash): %v\n", err)
		return nil
	}
	log.Printf("[RAN<-UE] Received %d bytes of deregistration request\n", len(ueDeregRequest))

	// Step 2: Forward to AMF via UplinkNASTransport
	if err := h.ngapClient.SendUplinkNASTransport(h.amfUeNgapID, h.ranUeNgapID, ueDeregRequest); err != nil {
		return fmt.Errorf("failed to forward deregistration request to AMF: %w", err)
	}
	log.Println("[RAN->AMF] Forwarded Deregistration Request")

	// Step 3: Receive Deregistration Accept from AMF (DownlinkNASTransport)
	nasDeregAccept, _, _, err := h.ngapClient.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive deregistration accept from AMF: %w", err)
	}
	log.Printf("[RAN<-AMF] Received %d bytes of deregistration accept\n", len(nasDeregAccept))

	// Step 4: Forward Deregistration Accept to UE
	if err := util.WriteMessage(h.conn, nasDeregAccept); err != nil {
		log.Printf("[RAN] Could not forward deregistration accept to UE (may have disconnected): %v\n", err)
	} else {
		log.Println("[RAN->UE] Forwarded Deregistration Accept")
	}

	// Step 6: Receive UEContextReleaseCommand from AMF
	amfID, ranID, err := h.ngapClient.ReceiveUEContextReleaseCommand()
	if err != nil {
		return fmt.Errorf("failed to receive UEContextReleaseCommand: %w", err)
	}
	log.Println("[RAN<-AMF] Received UEContextReleaseCommand")

	// Step 7: Send UEContextReleaseComplete to AMF
	if err := h.ngapClient.SendUEContextReleaseComplete(amfID, ranID); err != nil {
		return fmt.Errorf("failed to send UEContextReleaseComplete: %w", err)
	}
	log.Println("[RAN->AMF] Sent UEContextReleaseComplete")
	log.Println("✓ UE deregistered successfully")

	return nil
}

// extractIMSIFromMobileIdentity extracts IMSI string from Mobile Identity 5GS
func extractIMSIFromMobileIdentity(mobileID nasType.MobileIdentity5GS) string {
	if len(mobileID.Buffer) < 1 {
		return ""
	}

	// First byte contains type and odd/even indicator
	// Remaining bytes contain BCD-encoded IMSI digits
	digits := ""
	for i := 1; i < len(mobileID.Buffer); i++ {
		digit1 := mobileID.Buffer[i] & 0x0F
		digit2 := (mobileID.Buffer[i] >> 4) & 0x0F

		if digit1 <= 9 {
			digits += fmt.Sprintf("%d", digit1)
		}
		if digit2 <= 9 {
			digits += fmt.Sprintf("%d", digit2)
		}
	}

	return digits
}
