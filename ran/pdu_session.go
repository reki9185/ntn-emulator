package ran

import (
	"bytes"
	"fmt"

	"ntn-emulator/ran/ngap"
	"ntn-emulator/ue"
	uenas "ntn-emulator/ue/nas"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/openapi/models"
)

// PDUSessionHandler handles PDU Session Establishment procedure
type PDUSessionHandler struct {
	ue     *ue.UEContext
	codec  *uenas.NASCodec
	client *ngap.NGAPClient
}

// NewPDUSessionHandler creates a new PDU Session handler
func NewPDUSessionHandler(uectx *ue.UEContext, codec *uenas.NASCodec, client *ngap.NGAPClient) *PDUSessionHandler {
	return &PDUSessionHandler{
		ue:     uectx,
		codec:  codec,
		client: client,
	}
}

// EstablishPDUSession performs PDU Session Establishment procedure
func (h *PDUSessionHandler) EstablishPDUSession(pduSessionID uint8, dnn string, sNssai *models.Snssai) error {
	h.ue.SetState(ue.UEStateEstablishingPDU)
	h.ue.PDUSessionID = pduSessionID

	// Step 1: Build and send PDU Session Establishment Request
	if err := h.sendPDUSessionEstablishmentRequest(pduSessionID, dnn, sNssai); err != nil {
		return fmt.Errorf("failed to send PDU session establishment request: %w", err)
	}

	// Step 2: Receive and handle PDU Session Resource Setup Request
	if err := h.handlePDUSessionResourceSetup(); err != nil {
		return fmt.Errorf("failed to handle PDU session resource setup: %w", err)
	}

	// Step 3: Receive PDU Session Establishment Accept
	if err := h.receivePDUSessionEstablishmentAccept(); err != nil {
		return fmt.Errorf("failed to receive PDU session establishment accept: %w", err)
	}

	h.ue.SetState(ue.UEStatePDUActive)
	return nil
}

// sendPDUSessionEstablishmentRequest sends PDU Session Establishment Request
func (h *PDUSessionHandler) sendPDUSessionEstablishmentRequest(pduSessionID uint8, dnn string, sNssai *models.Snssai) error {
	// Build PDU Session Establishment Request
	pduSessionRequest, err := h.buildPDUSessionEstablishmentRequest(pduSessionID)
	if err != nil {
		return fmt.Errorf("failed to build PDU session request: %w", err)
	}

	// Build UL NAS Transport message
	ulNasTransport, err := h.buildULNASTransport(pduSessionRequest, pduSessionID, dnn, sNssai)
	if err != nil {
		return fmt.Errorf("failed to build UL NAS transport: %w", err)
	}

	// Encode with security
	m := new(nas.Message)
	if err := m.PlainNasDecode(&ulNasTransport); err != nil {
		return fmt.Errorf("failed to decode UL NAS transport: %w", err)
	}

	encodedMsg, err := h.codec.Encode(m,
		nas.SecurityHeaderTypeIntegrityProtectedAndCiphered, true, false)
	if err != nil {
		return fmt.Errorf("failed to encode UL NAS transport: %w", err)
	}

	// Send via NGAP Uplink NAS Transport
	err = h.client.SendUplinkNASTransport(h.ue.AmfUeNgapId, h.ue.RanUeNgapId, encodedMsg)
	if err != nil {
		return fmt.Errorf("failed to send PDU session request: %w", err)
	}

	fmt.Printf("✓ PDU Session Establishment Request sent (Session ID: %d)\n", pduSessionID)
	return nil
}

// receivePDUSessionEstablishmentAccept receives PDU Session Establishment Accept
func (h *PDUSessionHandler) receivePDUSessionEstablishmentAccept() error {
	// Receive NGAP message and extract NAS PDU
	nasPduBytes, _, _, err := h.client.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive NAS PDU: %w", err)
	}

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Verify message type (should be DL NAS Transport)
	msgType := nasPdu.GmmHeader.GetMessageType()
	fmt.Printf("DEBUG: Received message type: %d (0x%02X)\n", msgType, msgType)

	if msgType != nas.MsgTypeDLNASTransport {
		// Check if it's Configuration Update Command (0xD4 = 212)
		if msgType == 0xD4 {
			fmt.Println("Received Configuration Update Command, sending Complete...")
			// Handle Configuration Update - send Configuration Update Complete
			updateComplete, err := uenas.BuildConfigurationUpdateComplete()
			if err != nil {
				return fmt.Errorf("failed to build configuration update complete: %w", err)
			}

			// Encode and send
			m := new(nas.Message)
			if err := m.PlainNasDecode(&updateComplete); err != nil {
				return fmt.Errorf("failed to decode config update complete: %w", err)
			}

			encodedMsg, err := h.codec.Encode(m, nas.SecurityHeaderTypeIntegrityProtectedAndCiphered, true, false)
			if err != nil {
				return fmt.Errorf("failed to encode config update complete: %w", err)
			}

			err = h.client.SendUplinkNASTransport(h.ue.AmfUeNgapId, h.ue.RanUeNgapId, encodedMsg)
			if err != nil {
				return fmt.Errorf("failed to send config update complete: %w", err)
			}

			fmt.Println("✓ Configuration Update Complete sent")

			// Now receive the actual PDU Session Establishment Accept
			return h.receivePDUSessionEstablishmentAccept()
		}

		return fmt.Errorf("expected DL NAS Transport, got message type %d (0x%02X)", msgType, msgType)
	}

	// Extract PDU Session Establishment Accept from DL NAS Transport
	payloadContainer := nasPdu.DLNASTransport.GetPayloadContainerContents()
	if payloadContainer == nil {
		return fmt.Errorf("DL NAS Transport has no payload container")
	}

	fmt.Printf("DEBUG: Payload container length: %d bytes\n", len(payloadContainer))
	fmt.Printf("DEBUG: Payload container (first 32 bytes): %02x\n", payloadContainer[:min(32, len(payloadContainer))])

	// Decode inner PDU Session message
	innerMsg := new(nas.Message)
	if err := innerMsg.PlainNasDecode(&payloadContainer); err != nil {
		return fmt.Errorf("failed to decode inner PDU session message: %w", err)
	}

	// Verify it's PDU Session Establishment Accept
	innerMsgType := innerMsg.GsmHeader.GetMessageType()
	fmt.Printf("DEBUG: Inner message type: %d (0x%02X)\n", innerMsgType, innerMsgType)

	if innerMsgType != nas.MsgTypePDUSessionEstablishmentAccept {
		return fmt.Errorf("expected PDU Session Establishment Accept, got message type %d",
			innerMsgType)
	}

	fmt.Println("✓ PDU Session Establishment Accept received")
	return nil
}

// buildPDUSessionEstablishmentRequest builds PDU Session Establishment Request message
func (h *PDUSessionHandler) buildPDUSessionEstablishmentRequest(pduSessionID uint8) ([]byte, error) {
	m := nas.NewMessage()
	m.GsmMessage = nas.NewGsmMessage()
	m.GsmHeader.SetMessageType(nas.MsgTypePDUSessionEstablishmentRequest)

	pduSessionEstablishmentRequest := nasMessage.NewPDUSessionEstablishmentRequest(0)
	pduSessionEstablishmentRequest.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSSessionManagementMessage)
	pduSessionEstablishmentRequest.SetPDUSessionID(pduSessionID)
	pduSessionEstablishmentRequest.SetPTI(0x00)
	pduSessionEstablishmentRequest.SetMessageType(nas.MsgTypePDUSessionEstablishmentRequest)

	// Set Integrity Protection Maximum Data Rate (optional but recommended)
	integrityMaxDataRate := nasType.NewIntegrityProtectionMaximumDataRate(0)
	integrityMaxDataRate.SetMaximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink(0xff)
	integrityMaxDataRate.SetMaximumDataRatePerUEForUserPlaneIntegrityProtectionForDownLink(0xff)
	pduSessionEstablishmentRequest.IntegrityProtectionMaximumDataRate = *integrityMaxDataRate

	m.GsmMessage.PDUSessionEstablishmentRequest = pduSessionEstablishmentRequest

	buf := new(bytes.Buffer)
	if err := m.GsmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode PDU session establishment request: %w", err)
	}

	return buf.Bytes(), nil
}

// buildULNASTransport builds UL NAS Transport message containing PDU Session Request
func (h *PDUSessionHandler) buildULNASTransport(pduSessionRequest []byte, pduSessionID uint8,
	dnn string, sNssai *models.Snssai) ([]byte, error) {

	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeULNASTransport)

	ulNasTransport := nasMessage.NewULNASTransport(0)
	ulNasTransport.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	ulNasTransport.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	ulNasTransport.SpareHalfOctetAndSecurityHeaderType.SetSpareHalfOctet(0)
	ulNasTransport.ULNASTRANSPORTMessageIdentity.SetMessageType(nas.MsgTypeULNASTransport)

	// Set Payload Container Type (N1 SM information)
	ulNasTransport.SetPayloadContainerType(nasMessage.PayloadContainerTypeN1SMInfo)

	// Set Payload Container (the PDU Session Establishment Request)
	ulNasTransport.PayloadContainer.SetLen(uint16(len(pduSessionRequest)))
	ulNasTransport.PayloadContainer.SetPayloadContainerContents(pduSessionRequest)

	// Set PDU Session ID
	ulNasTransport.PduSessionID2Value = nasType.NewPduSessionID2Value(nasMessage.ULNASTransportPduSessionID2ValueType)
	ulNasTransport.PduSessionID2Value.SetPduSessionID2Value(pduSessionID)

	// Set Request Type (Initial Request)
	ulNasTransport.RequestType = nasType.NewRequestType(nasMessage.ULNASTransportRequestTypeType)
	ulNasTransport.RequestType.SetRequestTypeValue(nasMessage.ULNASTransportRequestTypeInitialRequest)

	// Set S-NSSAI
	if sNssai != nil {
		ulNasTransport.SNSSAI = nasType.NewSNSSAI(nasMessage.ULNASTransportSNSSAIType)

		ulNasTransport.SNSSAI.SetSST(uint8(sNssai.Sst))
		if sNssai.Sd != "" {
			// SD is a hex string (6 chars = 3 bytes), convert to bytes
			sd := [3]byte{}
			fmt.Sscanf(sNssai.Sd, "%02x%02x%02x", &sd[0], &sd[1], &sd[2])
			ulNasTransport.SNSSAI.SetSD(sd)
			ulNasTransport.SNSSAI.SetLen(4) // SST (1 byte) + SD (3 bytes)
		} else {
			ulNasTransport.SNSSAI.SetLen(1) // SST only (1 byte)
		}
	}

	// Set DNN
	if dnn != "" {
		ulNasTransport.DNN = nasType.NewDNN(nasMessage.ULNASTransportDNNType)
		// SetDNN automatically encodes the DNN in RFC 1035 format (length-prefixed labels)
		ulNasTransport.DNN.SetDNN(dnn)
	}

	m.GmmMessage.ULNASTransport = ulNasTransport

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode UL NAS transport: %w", err)
	}

	return buf.Bytes(), nil
}

// handlePDUSessionResourceSetup handles PDU Session Resource Setup Request and Response
func (h *PDUSessionHandler) handlePDUSessionResourceSetup() error {
	// This will receive PDU Session Resource Setup Request from AMF
	// Extract TEID, UPF address, and UE IP
	// Send PDU Session Resource Setup Response
	// Setup GTP-U tunnel and TUN interface

	fmt.Println("Waiting for PDU Session Resource Setup Request...")

	// Receive NGAP PDU Session Resource Setup Request
	pduSessionSetup, err := h.client.ReceivePDUSessionResourceSetupRequest()
	if err != nil {
		return fmt.Errorf("failed to receive PDU session resource setup request: %w", err)
	}

	fmt.Println("✓ Received PDU Session Resource Setup Request")
	fmt.Printf("  UPF TEID: 0x%08x\n", pduSessionSetup.UPFTEID)
	fmt.Printf("  UPF Address: %s:%d\n", pduSessionSetup.UPFAddress, pduSessionSetup.UPFPort)

	// Extract UE IP from NAS PDU (PDU Session Establishment Accept)
	if len(pduSessionSetup.NASPdu) > 0 {
		ueIP, err := h.extractUEIPFromNASPdu(pduSessionSetup.NASPdu)
		if err != nil {
			fmt.Printf("  Warning: Failed to extract UE IP from NAS PDU: %v\n", err)
		} else if ueIP != "" {
			pduSessionSetup.UEIPAddress = ueIP
		}
	}

	if pduSessionSetup.UEIPAddress == "" {
		return fmt.Errorf("no UE IP address found in PDU Session Resource Setup Request")
	}

	fmt.Printf("  UE IP Address: %s\n", pduSessionSetup.UEIPAddress)

	// Store UE IP address
	h.ue.UEIPAddress = pduSessionSetup.UEIPAddress

	// Generate TEIDs
	// Uplink TEID: RAN → UPF (RAN uses this when forwarding to UPF)
	uplinkTEID := pduSessionSetup.UPFTEID
	// Downlink TEID: UPF → RAN (UPF uses this when sending to RAN)
	downlinkTEID := uint32(h.ue.RanUeNgapId)

	// Store TEIDs in UE context (for separated architecture)
	h.ue.UPFTEID = uplinkTEID
	h.ue.RANTEID = downlinkTEID

	// Note: In the new free-ran-ue architecture:
	// - RAN process handles data plane server and GTP-U encapsulation
	// - UE process creates TUN and connects to RAN via simple UDP (no GTP at UE side)
	// - RAN should NOT create TUN interface

	fmt.Printf("✓ PDU Session established: UE IP=%s, UPF TEID=0x%08x, RAN TEID=0x%08x\n",
		pduSessionSetup.UEIPAddress, uplinkTEID, downlinkTEID)

	return nil
}

// extractUEIPFromNASPdu extracts UE IP address from PDU Session Establishment Accept NAS PDU
func (h *PDUSessionHandler) extractUEIPFromNASPdu(nasPdu []byte) (string, error) {
	// Decode NAS PDU
	securityHeaderType := nas.GetSecurityHeaderType(nasPdu)
	decodedNas, err := h.codec.Decode(securityHeaderType, nasPdu)
	if err != nil {
		return "", fmt.Errorf("failed to decode NAS PDU: %w", err)
	}

	// Check if it's DL NAS Transport
	if decodedNas.GmmHeader.GetMessageType() != nas.MsgTypeDLNASTransport {
		return "", fmt.Errorf("not a DL NAS Transport message")
	}

	// Extract payload container (PDU Session Establishment Accept)
	payloadContainer := decodedNas.DLNASTransport.GetPayloadContainerContents()
	if payloadContainer == nil {
		return "", fmt.Errorf("no payload container")
	}

	// Decode PDU Session Establishment Accept
	innerMsg := new(nas.Message)
	if err := innerMsg.PlainNasDecode(&payloadContainer); err != nil {
		return "", fmt.Errorf("failed to decode inner message: %w", err)
	}

	if innerMsg.GsmHeader.GetMessageType() != nas.MsgTypePDUSessionEstablishmentAccept {
		return "", fmt.Errorf("not a PDU Session Establishment Accept")
	}

	// Extract PDU Address from PDU Session Establishment Accept
	pduEstabAccept := innerMsg.GsmMessage.PDUSessionEstablishmentAccept
	if pduEstabAccept == nil {
		return "", fmt.Errorf("PDU Session Establishment Accept is nil")
	}

	// Get PDU Address
	if pduEstabAccept.PDUAddress != nil {
		pduAddrInfo := pduEstabAccept.PDUAddress.GetPDUAddressInformation()
		if len(pduAddrInfo) >= 4 {
			// IPv4 address (4 bytes)
			return fmt.Sprintf("%d.%d.%d.%d",
				pduAddrInfo[0], pduAddrInfo[1], pduAddrInfo[2], pduAddrInfo[3]), nil
		}
	}

	return "", fmt.Errorf("no PDU address found")
}

// EstablishPDUSessionForSeparateUE performs PDU Session Establishment for separated UE/RAN architecture
// This is used when UE runs as a separate process
func (h *PDUSessionHandler) EstablishPDUSessionForSeparateUE(pduSessionID uint8, dnn string, sNssai *models.Snssai, ueN3IP string) error {
	h.ue.SetState(ue.UEStateEstablishingPDU)
	h.ue.PDUSessionID = pduSessionID

	// Step 1: Build and send PDU Session Establishment Request
	if err := h.sendPDUSessionEstablishmentRequest(pduSessionID, dnn, sNssai); err != nil {
		return fmt.Errorf("failed to send PDU session establishment request: %w", err)
	}

	// Step 2: Receive and handle PDU Session Resource Setup Request
	if err := h.handlePDUSessionResourceSetupForSeparateUE(ueN3IP); err != nil {
		return fmt.Errorf("failed to handle PDU session resource setup: %w", err)
	}

	// Note: In separated architecture, RAN doesn't wait for PDU Session Establishment Accept
	// The UE process will handle that separately when it connects

	h.ue.SetState(ue.UEStatePDUActive)
	return nil
}

// handlePDUSessionResourceSetupForSeparateUE handles PDU session setup for separated UE/RAN
// In this architecture:
// - RAN only handles control plane (NGAP/NAS)
// - UE will run as separate process with its own GTP-U tunnel directly to UPF
func (h *PDUSessionHandler) handlePDUSessionResourceSetupForSeparateUE(ueN3IP string) error {
	fmt.Println("Waiting for PDU Session Resource Setup Request...")

	// Receive NGAP PDU Session Resource Setup Request
	pduSessionSetup, err := h.client.ReceivePDUSessionResourceSetupRequest()
	if err != nil {
		return fmt.Errorf("failed to receive PDU session resource setup request: %w", err)
	}

	fmt.Println("✓ Received PDU Session Resource Setup Request")
	fmt.Printf("  UPF TEID: 0x%08x\n", pduSessionSetup.UPFTEID)
	fmt.Printf("  UPF Address: %s:%d\n", pduSessionSetup.UPFAddress, pduSessionSetup.UPFPort)

	// Extract UE IP from NAS PDU
	if len(pduSessionSetup.NASPdu) > 0 {
		ueIP, err := h.extractUEIPFromNASPdu(pduSessionSetup.NASPdu)
		if err != nil {
			fmt.Printf("  Warning: Failed to extract UE IP from NAS PDU: %v\n", err)
		} else if ueIP != "" {
			pduSessionSetup.UEIPAddress = ueIP
		}
	}

	if pduSessionSetup.UEIPAddress == "" {
		return fmt.Errorf("no UE IP address found in PDU Session Resource Setup Request")
	}

	fmt.Printf("  UE IP Address: %s\n", pduSessionSetup.UEIPAddress)

	// Store session information in UE context
	h.ue.UEIPAddress = pduSessionSetup.UEIPAddress
	h.ue.UPFTEID = pduSessionSetup.UPFTEID
	h.ue.RANTEID = uint32(h.ue.RanUeNgapId) // Use RAN UE NGAP ID as downlink TEID

	// Send PDU Session Resource Setup Response
	// Tell SMF/UPF to send downlink GTP-U packets to ueN3IP:2152
	err = h.client.SendPDUSessionResourceSetupResponseWithIP(
		h.ue.AmfUeNgapId,
		h.ue.RanUeNgapId,
		h.ue.PDUSessionID,
		h.ue.RANTEID,
		ueN3IP, // UE's N3 IP address (where UE will listen for GTP-U)
	)
	if err != nil {
		return fmt.Errorf("failed to send PDU session resource setup response: %w", err)
	}

	fmt.Println("✓ PDU Session Resource Setup Response sent")
	fmt.Printf("  Told UPF to send downlink to: %s:2152 with TEID 0x%08x\n", ueN3IP, h.ue.RANTEID)

	return nil
}
