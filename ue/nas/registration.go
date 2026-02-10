package nas

import (
	"fmt"

	"ntn-emulator/ran/ngap"
	"ntn-emulator/ue"

	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasMessage"
)

// RegistrationHandler handles UE Registration procedure
type RegistrationHandler struct {
	ue     *ue.UEContext
	codec  *NASCodec
	client *ngap.NGAPClient
}

// NewRegistrationHandler creates a new Registration handler
func NewRegistrationHandler(uectx *ue.UEContext, codec *NASCodec, client *ngap.NGAPClient) *RegistrationHandler {
	return &RegistrationHandler{
		ue:     uectx,
		codec:  codec,
		client: client,
	}
}

// PerformRegistration performs the complete UE Registration procedure
func (h *RegistrationHandler) PerformRegistration() error {
	h.ue.SetState(ue.UEStateRegistering)

	// Step 1: Send Registration Request
	if err := h.sendRegistrationRequest(); err != nil {
		return fmt.Errorf("failed to send registration request: %w", err)
	}

	// Step 2: Receive and handle Authentication Request
	if err := h.handleAuthenticationRequest(); err != nil {
		return fmt.Errorf("failed to handle authentication request: %w", err)
	}

	// Step 3: Receive and handle Security Mode Command
	if err := h.handleSecurityModeCommand(); err != nil {
		return fmt.Errorf("failed to handle security mode command: %w", err)
	}

	// Step 4: Receive Registration Accept and send Registration Complete
	if err := h.handleRegistrationAccept(); err != nil {
		return fmt.Errorf("failed to handle registration accept: %w", err)
	}

	// Step 5: Receive Configuration Update Command (optional)
	if err := h.handleConfigurationUpdate(); err != nil {
		// Configuration update is optional, just log warning
		fmt.Printf("Warning: Configuration update handling: %v\n", err)
	}

	h.ue.SetState(ue.UEStateRegistered)
	return nil
}

// sendRegistrationRequest sends initial Registration Request to RAN
func (h *RegistrationHandler) sendRegistrationRequest() error {
	// Build Mobile Identity 5GS
	mobileIdentity := BuildMobileIdentity5GS(h.ue.Supi)

	// Build UE Security Capability
	ueSecurityCapability := BuildUESecurityCapability(h.ue.CipheringAlg, h.ue.IntegrityAlg)

	// Build Registration Request (initial, without 5GMM capability)
	regRequest, err := BuildRegistrationRequest(
		nasMessage.RegistrationType5GSInitialRegistration,
		&mobileIdentity,
		&ueSecurityCapability,
		nil, // No 5GMM capability yet
	)
	if err != nil {
		return fmt.Errorf("failed to build registration request: %w", err)
	}

	// Send Initial UE Message (first NGAP message for this UE)
	err = h.client.SendInitialUEMessage(h.ue.RanUeNgapId, regRequest)
	if err != nil {
		return fmt.Errorf("failed to send initial UE message: %w", err)
	}

	return nil
}

// handleAuthenticationRequest receives and responds to Authentication Request
func (h *RegistrationHandler) handleAuthenticationRequest() error {
	// Receive NGAP message and extract NAS PDU
	nasPduBytes, amfUeNgapID, _, err := h.client.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive authentication request: %w", err)
	}

	// Save AMF UE NGAP ID for future messages
	if amfUeNgapID != nil {
		h.ue.AmfUeNgapId = *amfUeNgapID
	}

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Verify message type
	if nasPdu.GmmHeader.GetMessageType() != nas.MsgTypeAuthenticationRequest {
		return fmt.Errorf("expected Authentication Request, got message type %d", nasPdu.GmmHeader.GetMessageType())
	}

	// Extract RAND and AUTN
	rand := nasPdu.AuthenticationRequest.GetRANDValue()

	// Calculate RES* and derive keys
	resStar := h.ue.DeriveRESstarAndSetKey(h.ue.AuthenticationSubs, rand[:], "5G:mnc093.mcc208.3gppnetwork.org")

	// Update UE context - SQN is already updated by DeriveRESstarAndSetKey

	// Build and send Authentication Response
	authResponse, err := BuildAuthenticationResponse(resStar)
	if err != nil {
		return fmt.Errorf("failed to build authentication response: %w", err)
	}

	// Send via Uplink NAS Transport (we now have AMF UE NGAP ID)
	err = h.client.SendUplinkNASTransport(h.ue.AmfUeNgapId, h.ue.RanUeNgapId, authResponse)
	if err != nil {
		return fmt.Errorf("failed to send authentication response: %w", err)
	}

	return nil
}

// handleSecurityModeCommand receives Security Mode Command and responds with Complete
func (h *RegistrationHandler) handleSecurityModeCommand() error {
	// Receive NGAP message and extract NAS PDU
	nasPduBytes, _, _, err := h.client.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive security mode command: %w", err)
	}

	// Debug: print first few bytes
	if len(nasPduBytes) > 0 {
		fmt.Printf("DEBUG: Received NAS PDU, first bytes: %02x\n", nasPduBytes[:min(16, len(nasPduBytes))])
	}

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Verify message type
	if nasPdu.GmmHeader.GetMessageType() != nas.MsgTypeSecurityModeCommand {
		return fmt.Errorf("expected Security Mode Command, got message type %d", nasPdu.GmmHeader.GetMessageType())
	}

	// Build Registration Request with 5GMM capability
	mobileIdentity := BuildMobileIdentity5GS(h.ue.Supi)
	ueSecurityCapability := BuildUESecurityCapability(h.ue.CipheringAlg, h.ue.IntegrityAlg)
	capability5GMM := h.ue.Get5GMMCapability()

	regRequestWith5GMM, err := BuildRegistrationRequest(
		nasMessage.RegistrationType5GSInitialRegistration,
		&mobileIdentity,
		&ueSecurityCapability,
		capability5GMM,
	)
	if err != nil {
		return fmt.Errorf("failed to build registration request with 5GMM: %w", err)
	}

	// Build Security Mode Complete with embedded Registration Request
	secModeComplete, err := BuildSecurityModeComplete(regRequestWith5GMM)
	if err != nil {
		return fmt.Errorf("failed to build security mode complete: %w", err)
	}

	// Encode with security (new security context)
	m := new(nas.Message)
	if err := m.PlainNasDecode(&secModeComplete); err != nil {
		return fmt.Errorf("failed to decode security mode complete: %w", err)
	}

	encodedMsg, err := h.codec.Encode(m,
		nas.SecurityHeaderTypeIntegrityProtectedAndCipheredWithNew5gNasSecurityContext, true, true)
	if err != nil {
		return fmt.Errorf("failed to encode security mode complete: %w", err)
	}

	// Send Security Mode Complete via Uplink NAS Transport
	err = h.client.SendUplinkNASTransport(h.ue.AmfUeNgapId, h.ue.RanUeNgapId, encodedMsg)
	if err != nil {
		return fmt.Errorf("failed to send security mode complete: %w", err)
	}

	return nil
}

// handleRegistrationAccept receives Registration Accept and sends Registration Complete
func (h *RegistrationHandler) handleRegistrationAccept() error {
	// Receive NGAP message and extract NAS PDU
	nasPduBytes, _, _, err := h.client.ReceiveNASPDU()
	if err != nil {
		return fmt.Errorf("failed to receive registration accept: %w", err)
	}

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Check if we received Identity Request first (message type 0x5B = 91)
	msgType := nasPdu.GmmHeader.GetMessageType()
	fmt.Printf("DEBUG: Received message type: %d (0x%02X)\n", msgType, msgType)

	if msgType == 91 { // Identity Request (0x5B)
		// Check what type of identity is requested
		var requestedType uint8
		if nasPdu.IdentityRequest != nil {
			requestedType = nasPdu.IdentityRequest.SpareHalfOctetAndIdentityType.GetTypeOfIdentity()
			fmt.Printf("Identity Request: Type requested = %d\n", requestedType)
		} else {
			return fmt.Errorf("Identity Request message is nil")
		}

		fmt.Println("Received Identity Request, sending Identity Response...")

		// Build and send Identity Response with requested identity type
		identityResponse, err := BuildIdentityResponseWithType(h.ue.Supi, requestedType)
		if err != nil {
			return fmt.Errorf("failed to build identity response: %w", err)
		}

		fmt.Printf("DEBUG: Identity Response payload (first 32 bytes): %02x\n", identityResponse[:min(32, len(identityResponse))])

		// Encode with security
		m := new(nas.Message)
		if err := m.PlainNasDecode(&identityResponse); err != nil {
			return fmt.Errorf("failed to decode identity response: %w", err)
		}

		encodedMsg, err := h.codec.Encode(m,
			nas.SecurityHeaderTypeIntegrityProtectedAndCiphered, true, false)
		if err != nil {
			return fmt.Errorf("failed to encode identity response: %w", err)
		}

		fmt.Printf("DEBUG: Encoded Identity Response (first 32 bytes): %02x\n", encodedMsg[:min(32, len(encodedMsg))])

		err = h.client.SendUplinkNASTransport(h.ue.AmfUeNgapId, h.ue.RanUeNgapId, encodedMsg)
		if err != nil {
			return fmt.Errorf("failed to send identity response: %w", err)
		}

		fmt.Println("Identity Response sent successfully")

		// Now receive the actual Registration Accept (in Initial Context Setup Request)
		nasPduBytes, _, _, err = h.client.ReceiveNASPDU()
		if err != nil {
			return fmt.Errorf("failed to receive registration accept after identity: %w", err)
		}

		securityHeaderType = nas.GetSecurityHeaderType(nasPduBytes)
		nasPdu, err = h.codec.Decode(securityHeaderType, nasPduBytes)
		if err != nil {
			return fmt.Errorf("failed to decode NAS message: %w", err)
		}

		msgType = nasPdu.GmmHeader.GetMessageType()
		fmt.Printf("DEBUG: After Identity Response, received message type: %d (0x%02X)\n", msgType, msgType)
	}

	// Verify message type
	if nasPdu.GmmHeader.GetMessageType() != nas.MsgTypeRegistrationAccept {
		return fmt.Errorf("expected Registration Accept, got message type %d", nasPdu.GmmHeader.GetMessageType())
	}

	fmt.Println("✓ Registration Accept received")

	// Send Initial Context Setup Response (if received in Initial Context Setup Request)
	// This is needed to complete the NGAP procedure
	err = h.client.SendInitialContextSetupResponse(h.ue.AmfUeNgapId, h.ue.RanUeNgapId)
	if err != nil {
		return fmt.Errorf("failed to send Initial Context Setup Response: %w", err)
	}

	fmt.Println("✓ Initial Context Setup Response sent")

	// Build Registration Complete
	regComplete, err := BuildRegistrationComplete()
	if err != nil {
		return fmt.Errorf("failed to build registration complete: %w", err)
	}

	// Encode with security
	m := new(nas.Message)
	if err := m.PlainNasDecode(&regComplete); err != nil {
		return fmt.Errorf("failed to decode registration complete: %w", err)
	}

	encodedMsg, err := h.codec.Encode(m,
		nas.SecurityHeaderTypeIntegrityProtectedAndCiphered, true, false)
	if err != nil {
		return fmt.Errorf("failed to encode registration complete: %w", err)
	}

	// Send Registration Complete via Uplink NAS Transport
	err = h.client.SendUplinkNASTransport(h.ue.AmfUeNgapId, h.ue.RanUeNgapId, encodedMsg)
	if err != nil {
		return fmt.Errorf("failed to send registration complete: %w", err)
	}

	fmt.Println("✓ Registration Complete sent")

	return nil
}

// handleConfigurationUpdate receives Configuration Update Command (optional)
func (h *RegistrationHandler) handleConfigurationUpdate() error {
	// Receive NGAP message and extract NAS PDU (optional, may timeout)
	nasPduBytes, _, _, err := h.client.ReceiveNASPDU()
	if err != nil {
		return err // May timeout, which is acceptable
	}

	// Decode NAS message
	securityHeaderType := nas.GetSecurityHeaderType(nasPduBytes)
	nasPdu, err := h.codec.Decode(securityHeaderType, nasPduBytes)
	if err != nil {
		return fmt.Errorf("failed to decode NAS message: %w", err)
	}

	// Verify message type (optional, just receive and acknowledge)
	if nasPdu.GmmHeader.GetMessageType() != nas.MsgTypeConfigurationUpdateCommand {
		return fmt.Errorf("expected Configuration Update Command, got message type %d",
			nasPdu.GmmHeader.GetMessageType())
	}

	return nil
}
