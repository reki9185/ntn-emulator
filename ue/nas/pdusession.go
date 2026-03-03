package nas

import (
	"bytes"
	"fmt"
	"net"

	"ntn-emulator/common"
	"ntn-emulator/ue"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasConvert"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/openapi/models"
)

// PDUSessionHandler handles PDU Session Establishment procedure
type PDUSessionHandler struct {
	ue             *ue.UEContext
	codec          *NASCodec
	ranControlConn net.Conn
}

// NewPDUSessionHandler creates a new PDU Session handler
func NewPDUSessionHandler(uectx *ue.UEContext, codec *NASCodec, ranControlConn net.Conn) *PDUSessionHandler {
	return &PDUSessionHandler{
		ue:             uectx,
		codec:          codec,
		ranControlConn: ranControlConn,
	}
}

// EstablishPDUSession performs PDU Session Establishment procedure
func (h *PDUSessionHandler) EstablishPDUSession(pduSessionID uint8, dnn string, sNssai *models.Snssai) error {
	fmt.Printf("\n[PDU Session] Starting PDU Session Establishment (Session ID: %d, DNN: %s)\n", pduSessionID, dnn)

	// Step 1: Build and send PDU Session Establishment Request
	if err := h.sendPDUSessionEstablishmentRequest(pduSessionID, dnn, sNssai); err != nil {
		return fmt.Errorf("failed to send PDU session establishment request: %w", err)
	}

	// Step 2: Receive PDU Session Establishment Accept
	if err := h.receivePDUSessionEstablishmentAccept(); err != nil {
		return fmt.Errorf("failed to receive PDU session establishment accept: %w", err)
	}

	h.ue.PDUSessionID = pduSessionID
	fmt.Println("✓ PDU Session Establishment completed")
	return nil
}

// sendPDUSessionEstablishmentRequest sends PDU Session Establishment Request
func (h *PDUSessionHandler) sendPDUSessionEstablishmentRequest(pduSessionID uint8, dnn string, sNssai *models.Snssai) error {
	// Step 1: Build PDU Session Establishment Request message
	pduSessionBytes, err := buildPDUSessionEstablishmentRequest(pduSessionID)
	if err != nil {
		return fmt.Errorf("failed to build PDU session establishment request: %w", err)
	}

	// Step 2: Wrap it in UL NAS Transport message
	ulNasTransportBytes, err := buildULNASTransport(pduSessionBytes, pduSessionID, dnn, sNssai)
	if err != nil {
		return fmt.Errorf("failed to build UL NAS transport: %w", err)
	}

	// Step 3: Encode with security (using test.EncodeNasPduWithSecurity directly)
	encodedMsg, err := h.codec.EncodeBytes(ulNasTransportBytes, nas.SecurityHeaderTypeIntegrityProtectedAndCiphered, true, false)
	if err != nil {
		return fmt.Errorf("failed to encode UL NAS transport: %w", err)
	}

	// Step 4: Send to RAN
	if err := common.WriteMessage(h.ranControlConn, encodedMsg); err != nil {
		return fmt.Errorf("failed to send PDU session establishment request: %w", err)
	}

	fmt.Printf("✓ PDU Session Establishment Request sent (Session ID: %d)\n", pduSessionID)
	return nil
}

// receivePDUSessionEstablishmentAccept receives PDU Session Establishment Accept
func (h *PDUSessionHandler) receivePDUSessionEstablishmentAccept() error {
	// Receive NAS PDU from RAN
	nasPduBytes, err := common.ReadMessage(h.ranControlConn)
	if err != nil {
		return fmt.Errorf("failed to receive PDU session establishment accept: %w", err)
	}
	fmt.Printf("Received %d bytes from RAN (PDU Session Establishment Accept)\n", len(nasPduBytes))

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Verify it's DL NAS Transport
	if nasPdu.GmmHeader.GetMessageType() != nas.MsgTypeDLNASTransport {
		return fmt.Errorf("expected DL NAS Transport, got message type 0x%02X", nasPdu.GmmHeader.GetMessageType())
	}

	// Extract PDU Session Establishment Accept from DL NAS Transport
	dlNasTransport := nasPdu.DLNASTransport
	if dlNasTransport == nil {
		return fmt.Errorf("DL NAS Transport is nil")
	}

	// Extract payload container (contains PDU Session Establishment Accept)
	payload := dlNasTransport.PayloadContainer.GetPayloadContainerContents()
	if len(payload) == 0 {
		return fmt.Errorf("payload container is empty")
	}

	// Decode PDU Session Establishment Accept from payload
	gsmMsg := nas.NewMessage()
	if err := gsmMsg.GsmMessageDecode(&payload); err != nil {
		return fmt.Errorf("failed to decode GSM message: %w", err)
	}

	if gsmMsg.GsmHeader.GetMessageType() != nas.MsgTypePDUSessionEstablishmentAccept {
		return fmt.Errorf("expected PDU Session Establishment Accept, got message type 0x%02X", gsmMsg.GsmHeader.GetMessageType())
	}

	fmt.Println("✓ PDU Session Establishment Accept received")

	// Extract UE IP address and other info
	if err := h.extractUEInformation(gsmMsg); err != nil {
		return fmt.Errorf("failed to extract UE information: %w", err)
	}

	return nil
}

// extractUEInformation extracts UE IP address, TEIDs from PDU Session Establishment Accept
func (h *PDUSessionHandler) extractUEInformation(gsmMsg *nas.Message) error {
	pduEstabAccept := gsmMsg.PDUSessionEstablishmentAccept
	if pduEstabAccept == nil {
		return fmt.Errorf("PDU Session Establishment Accept is nil")
	}

	// Extract PDU Address (UE IP)
	if pduEstabAccept.PDUAddress != nil {
		pduAddress := pduEstabAccept.PDUAddress
		if pduAddress.GetPDUSessionTypeValue() == nasMessage.PDUSessionTypeIPv4 {
			ipBytes := pduAddress.GetPDUAddressInformation()
			if len(ipBytes) >= 4 {
				h.ue.UEIPAddress = fmt.Sprintf("%d.%d.%d.%d", ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
				fmt.Printf("✓ UE IP Address: %s\n", h.ue.UEIPAddress)
			}
		}
	}

	// Extract Authorized QoS Rules
	if len(pduEstabAccept.AuthorizedQosRules.GetQosRule()) > 0 {
		fmt.Println("✓ Authorized QoS Rules received")
	}

	// Note: TEIDs are extracted from NGAP PDU Session Resource Setup Request on the RAN side
	// and passed to UE via the data plane or stored in context

	return nil
}

// buildPDUSessionEstablishmentRequest builds PDU Session Establishment Request (like free-ran-ue)
func buildPDUSessionEstablishmentRequest(pduSessionID uint8) ([]byte, error) {
	m := nas.NewMessage()
	m.GsmMessage = nas.NewGsmMessage()
	m.GsmHeader.SetMessageType(nas.MsgTypePDUSessionEstablishmentRequest)

	pduSessionRequest := nasMessage.NewPDUSessionEstablishmentRequest(0)
	pduSessionRequest.ExtendedProtocolDiscriminator.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSSessionManagementMessage)
	pduSessionRequest.SetMessageType(nas.MsgTypePDUSessionEstablishmentRequest)
	pduSessionRequest.PDUSessionID.SetPDUSessionID(pduSessionID)
	pduSessionRequest.PTI.SetPTI(0x00)
	pduSessionRequest.IntegrityProtectionMaximumDataRate.SetMaximumDataRatePerUEForUserPlaneIntegrityProtectionForDownLink(0xff)
	pduSessionRequest.IntegrityProtectionMaximumDataRate.SetMaximumDataRatePerUEForUserPlaneIntegrityProtectionForUpLink(0xff)

	// Set PDU Session Type to IPv4
	pduSessionRequest.PDUSessionType = nasType.NewPDUSessionType(nasMessage.PDUSessionEstablishmentRequestPDUSessionTypeType)
	pduSessionRequest.PDUSessionType.SetPDUSessionTypeValue(uint8(0x01)) // IPv4

	// Set SSC Mode to SSC Mode 1
	pduSessionRequest.SSCMode = nasType.NewSSCMode(nasMessage.PDUSessionEstablishmentRequestSSCModeType)
	pduSessionRequest.SSCMode.SetSSCMode(uint8(0x01)) // SSC Mode 1

	// Set Extended Protocol Configuration Options
	pduSessionRequest.ExtendedProtocolConfigurationOptions = nasType.NewExtendedProtocolConfigurationOptions(nasMessage.PDUSessionEstablishmentRequestExtendedProtocolConfigurationOptionsType)
	protocolConfigurationOptions := nasConvert.NewProtocolConfigurationOptions()
	protocolConfigurationOptions.AddIPAddressAllocationViaNASSignallingUL()
	protocolConfigurationOptions.AddDNSServerIPv4AddressRequest()
	protocolConfigurationOptions.AddDNSServerIPv6AddressRequest()
	pcoContents := protocolConfigurationOptions.Marshal()
	pcoContentsLength := len(pcoContents)
	pduSessionRequest.ExtendedProtocolConfigurationOptions.SetLen(uint16(pcoContentsLength))
	pduSessionRequest.ExtendedProtocolConfigurationOptions.SetExtendedProtocolConfigurationOptionsContents(pcoContents)

	m.GsmMessage.PDUSessionEstablishmentRequest = pduSessionRequest

	request := new(bytes.Buffer)
	if err := m.GsmMessageEncode(request); err != nil {
		return nil, err
	}

	return request.Bytes(), nil
}

// buildULNASTransport builds UL NAS Transport message wrapping PDU Session Establishment Request (like free-ran-ue)
func buildULNASTransport(nasMessageContainer []byte, pduSessionID uint8, dnn string, sNssai *models.Snssai) ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeULNASTransport)

	ulNasTransport := nasMessage.NewULNASTransport(0)
	ulNasTransport.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	ulNasTransport.SetMessageType(nas.MsgTypeULNASTransport)
	ulNasTransport.ExtendedProtocolDiscriminator.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)

	// Set PDU Session ID
	ulNasTransport.PduSessionID2Value = new(nasType.PduSessionID2Value)
	ulNasTransport.PduSessionID2Value.SetIei(nasMessage.ULNASTransportPduSessionID2ValueType)
	ulNasTransport.PduSessionID2Value.SetPduSessionID2Value(pduSessionID)

	// Set Request Type to Initial Request
	ulNasTransport.RequestType = new(nasType.RequestType)
	ulNasTransport.RequestType.SetIei(nasMessage.ULNASTransportRequestTypeType)
	ulNasTransport.RequestType.SetRequestTypeValue(nasMessage.ULNASTransportRequestTypeInitialRequest)

	// Set DNN if provided
	if dnn != "" {
		ulNasTransport.DNN = new(nasType.DNN)
		ulNasTransport.DNN.SetIei(nasMessage.ULNASTransportDNNType)
		ulNasTransport.DNN.SetDNN(dnn)
	}

	// Set S-NSSAI if provided
	if sNssai != nil {
		var sdTemp [3]uint8
		if sNssai.Sd != "" {
			// Parse SD from hex string
			var sdBytes [3]byte
			fmt.Sscanf(sNssai.Sd, "%02x%02x%02x", &sdBytes[0], &sdBytes[1], &sdBytes[2])
			copy(sdTemp[:], sdBytes[:])
		}
		ulNasTransport.SNSSAI = nasType.NewSNSSAI(nasMessage.ULNASTransportSNSSAIType)
		ulNasTransport.SNSSAI.SetLen(4)
		ulNasTransport.SNSSAI.SetSST(uint8(sNssai.Sst))
		ulNasTransport.SNSSAI.SetSD(sdTemp)
	}

	// Set Payload Container Type and contents
	ulNasTransport.SpareHalfOctetAndPayloadContainerType.SetPayloadContainerType(nasMessage.PayloadContainerTypeN1SMInfo)
	ulNasTransport.PayloadContainer.SetLen(uint16(len(nasMessageContainer)))
	ulNasTransport.PayloadContainer.SetPayloadContainerContents(nasMessageContainer)

	m.GmmMessage.ULNASTransport = ulNasTransport

	message := new(bytes.Buffer)
	if err := m.GmmMessageEncode(message); err != nil {
		return nil, err
	}

	return message.Bytes(), nil
}
