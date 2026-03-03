package nas

import (
	"bytes"
	"fmt"
	"net"

	"ntn-emulator/common"
	"ntn-emulator/ue"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/nas/nasType"
)

// DeregistrationHandler handles UE Deregistration procedure
type DeregistrationHandler struct {
	ue             *ue.UEContext
	codec          *NASCodec
	ranControlConn net.Conn
}

// NewDeregistrationHandler creates a new Deregistration handler
func NewDeregistrationHandler(uectx *ue.UEContext, codec *NASCodec, ranControlConn net.Conn) *DeregistrationHandler {
	return &DeregistrationHandler{
		ue:             uectx,
		codec:          codec,
		ranControlConn: ranControlConn,
	}
}

// PerformDeregistration performs the complete UE Deregistration procedure
func (h *DeregistrationHandler) PerformDeregistration(switchOff bool) error {
	h.ue.SetState(ue.UEStateDeregistering)

	fmt.Println("\n[Deregistration] Starting UE-initiated deregistration...")

	// Step 1: Send Deregistration Request
	if err := h.sendDeregistrationRequest(switchOff); err != nil {
		return fmt.Errorf("failed to send deregistration request: %w", err)
	}

	// Step 2: Receive Deregistration Accept
	if err := h.handleDeregistrationAccept(); err != nil {
		return fmt.Errorf("failed to handle deregistration accept: %w", err)
	}

	h.ue.SetState(ue.UEStateIdle)
	fmt.Println("✓ Deregistration completed successfully")

	return nil
}

// sendDeregistrationRequest sends UE-originated Deregistration Request
func (h *DeregistrationHandler) sendDeregistrationRequest(switchOff bool) error {
	// Build Mobile Identity 5GS
	mobileIdentity := BuildMobileIdentity5GS(h.ue.Supi)

	// Build Deregistration Request
	var switchOffValue uint8
	if switchOff {
		switchOffValue = 0x01 // Switch off
	} else {
		switchOffValue = 0x00 // Normal detach
	}

	deregRequest, err := BuildDeregistrationRequest(
		nasMessage.AccessType3GPP,
		switchOffValue,
		0x07, // ngKSI (same as used in registration)
		&mobileIdentity,
	)
	if err != nil {
		return fmt.Errorf("failed to build deregistration request: %w", err)
	}

	// Encode with security
	m := new(nas.Message)
	if err := m.PlainNasDecode(&deregRequest); err != nil {
		return fmt.Errorf("failed to decode deregistration request: %w", err)
	}

	encodedMsg, err := h.codec.Encode(m,
		nas.SecurityHeaderTypeIntegrityProtectedAndCiphered, true, false)
	if err != nil {
		return fmt.Errorf("failed to encode deregistration request: %w", err)
	}

	// Send via Uplink NAS Transport
	// Send plain NAS PDU to RAN control plane via TCP
	if err := common.WriteMessage(h.ranControlConn, encodedMsg); err != nil {
		return fmt.Errorf("failed to send deregistration request: %w", err)
	}
	fmt.Printf("[Deregistration] Sent %d bytes to RAN\n", len(encodedMsg))

	fmt.Println("✓ Deregistration Request sent")
	return nil
}

// handleDeregistrationAccept receives Deregistration Accept from network
func (h *DeregistrationHandler) handleDeregistrationAccept() error {
	// Receive plain NAS PDU from RAN control plane via TCP
	nasPduBytes, err := common.ReadMessage(h.ranControlConn)
	if err != nil {
		// Check if this is a timeout (AMF might have processed deregistration but not sent accept)
		return fmt.Errorf("failed to receive deregistration accept: %w (Note: deregistration may still have succeeded on network side)", err)
	}
	fmt.Printf("[Deregistration] Received %d bytes from RAN\n", len(nasPduBytes))

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Verify message type
	msgType := nasPdu.GmmHeader.GetMessageType()
	if msgType != nas.MsgTypeDeregistrationAcceptUEOriginatingDeregistration {
		// Check if we got a reject or other error
		fmt.Printf("⚠️  Warning: Expected Deregistration Accept (0x46), got message type 0x%02x\n", msgType)
		return fmt.Errorf("expected Deregistration Accept (UE-originating), got message type %d (0x%02x)",
			msgType, msgType)
	}

	fmt.Println("✓ Deregistration Accept received")
	return nil
}

// BuildDeregistrationRequest builds a Deregistration Request NAS message (UE-originated)
func BuildDeregistrationRequest(accessType, switchOff, ngKsi uint8, mobileIdentity5GS *nasType.MobileIdentity5GS) ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeDeregistrationRequestUEOriginatingDeregistration)

	deregRequest := nasMessage.NewDeregistrationRequestUEOriginatingDeregistration(0)
	deregRequest.ExtendedProtocolDiscriminator.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	deregRequest.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	deregRequest.SpareHalfOctetAndSecurityHeaderType.SetSpareHalfOctet(0x00)
	deregRequest.DeregistrationRequestMessageIdentity.SetMessageType(nas.MsgTypeDeregistrationRequestUEOriginatingDeregistration)

	// Set deregistration type
	deregRequest.NgksiAndDeregistrationType.SetAccessType(accessType)
	deregRequest.NgksiAndDeregistrationType.SetSwitchOff(switchOff)
	deregRequest.NgksiAndDeregistrationType.SetReRegistrationRequired(0)
	deregRequest.NgksiAndDeregistrationType.SetTSC(ngKsi)
	deregRequest.NgksiAndDeregistrationType.SetNasKeySetIdentifiler(ngKsi)

	// Set mobile identity
	deregRequest.MobileIdentity5GS.SetLen(mobileIdentity5GS.GetLen())
	deregRequest.MobileIdentity5GS.SetMobileIdentity5GSContents(mobileIdentity5GS.GetMobileIdentity5GSContents())

	m.GmmMessage.DeregistrationRequestUEOriginatingDeregistration = deregRequest

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode deregistration request: %w", err)
	}

	return buf.Bytes(), nil
}
