package nas

import (
	"bytes"
	"errors"
	"fmt"

	"ntn-emulator/ue"

	"test"

	"github.com/free-ran-ue/util"
	"github.com/free5gc/nas"
	"github.com/free5gc/nas/nasMessage"
	"github.com/free5gc/nas/nasType"
	"github.com/free5gc/nas/security"
)

// NASCodec handles NAS message encoding and decoding
type NASCodec struct {
	ue *ue.UEContext
}

// NewNASCodec creates a new NAS codec for the given UE context
func NewNASCodec(uectx *ue.UEContext) *NASCodec {
	return &NASCodec{ue: uectx}
}

// Decode decodes a NAS message with security
func (nc *NASCodec) Decode(securityHeaderType uint8, payload []byte) (*nas.Message, error) {
	// Use test package's NASDecode to handle security
	return test.NASDecode(nc.ue.RanUeContext, securityHeaderType, payload)
}

// Encode encodes a NAS message with security
func (nc *NASCodec) Encode(nasMessage *nas.Message, securityHeaderType uint8,
	securityContextAvailable, newSecurityContext bool) ([]byte, error) {
	if nasMessage == nil {
		return nil, errors.New("NAS message is nil")
	}

	// Plain NAS encoding (no security)
	if !securityContextAvailable {
		return nasMessage.PlainNasEncode()
	}

	// Use test package's EncodeNasPduWithSecurity for consistency
	payload, err := nasMessage.PlainNasEncode()
	if err != nil {
		return nil, err
	}

	return test.EncodeNasPduWithSecurity(nc.ue.RanUeContext, payload, securityHeaderType, true, newSecurityContext)
}

// BuildMobileIdentity5GS builds Mobile Identity 5GS from SUPI
func BuildMobileIdentity5GS(supi string) nasType.MobileIdentity5GS {
	// util.SupiToBytes expects pure digits without "imsi-" prefix
	// supi format: "imsi-208930000000001" -> need "208930000000001"
	imsi := supi
	if len(supi) > 5 && supi[:5] == "imsi-" {
		imsi = supi[5:]
	}
	supiBytes := util.SupiToBytes(imsi)
	return nasType.MobileIdentity5GS{
		Len:    uint16(len(supiBytes)),
		Buffer: supiBytes,
	}
}

// BuildUESecurityCapability builds UE Security Capability
func BuildUESecurityCapability(cipheringAlg, integrityAlg uint8) nasType.UESecurityCapability {
	ueSecurityCapability := nasType.UESecurityCapability{
		Iei:    nasMessage.RegistrationRequestUESecurityCapabilityType,
		Len:    2,
		Buffer: []byte{0x00, 0x00},
	}

	// Set ciphering algorithm
	switch cipheringAlg {
	case security.AlgCiphering128NEA0:
		ueSecurityCapability.SetEA0_5G(1)
	case security.AlgCiphering128NEA1:
		ueSecurityCapability.SetEA1_128_5G(1)
	case security.AlgCiphering128NEA2:
		ueSecurityCapability.SetEA2_128_5G(1)
	case security.AlgCiphering128NEA3:
		ueSecurityCapability.SetEA3_128_5G(1)
	}

	// Set integrity algorithm
	switch integrityAlg {
	case security.AlgIntegrity128NIA0:
		ueSecurityCapability.SetIA0_5G(1)
	case security.AlgIntegrity128NIA1:
		ueSecurityCapability.SetIA1_128_5G(1)
	case security.AlgIntegrity128NIA2:
		ueSecurityCapability.SetIA2_128_5G(1)
	case security.AlgIntegrity128NIA3:
		ueSecurityCapability.SetIA3_128_5G(1)
	}

	return ueSecurityCapability
}

// BuildRegistrationRequest builds a Registration Request NAS message
func BuildRegistrationRequest(registrationType uint8, mobileIdentity5GS *nasType.MobileIdentity5GS,
	ueSecurityCapability *nasType.UESecurityCapability, capability5GMM *nasType.Capability5GMM) ([]byte, error) {

	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeRegistrationRequest)

	registrationRequest := nasMessage.NewRegistrationRequest(0)
	registrationRequest.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	registrationRequest.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	registrationRequest.SpareHalfOctetAndSecurityHeaderType.SetSpareHalfOctet(0x00)
	registrationRequest.RegistrationRequestMessageIdentity.SetMessageType(nas.MsgTypeRegistrationRequest)
	registrationRequest.NgksiAndRegistrationType5GS.SetTSC(nasMessage.TypeOfSecurityContextFlagNative)
	registrationRequest.NgksiAndRegistrationType5GS.SetNasKeySetIdentifiler(0x7)
	registrationRequest.NgksiAndRegistrationType5GS.SetFOR(1)
	registrationRequest.NgksiAndRegistrationType5GS.SetRegistrationType5GS(registrationType)
	registrationRequest.MobileIdentity5GS = *mobileIdentity5GS

	if ueSecurityCapability != nil {
		registrationRequest.UESecurityCapability = ueSecurityCapability
	}

	if capability5GMM != nil {
		registrationRequest.Capability5GMM = capability5GMM
	}

	m.GmmMessage.RegistrationRequest = registrationRequest

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode registration request: %w", err)
	}

	return buf.Bytes(), nil
}

// BuildAuthenticationResponse builds an Authentication Response NAS message
func BuildAuthenticationResponse(resStar []byte) ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeAuthenticationResponse)

	authResponse := nasMessage.NewAuthenticationResponse(0)
	authResponse.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	authResponse.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	authResponse.SpareHalfOctetAndSecurityHeaderType.SetSpareHalfOctet(0x00)
	authResponse.AuthenticationResponseMessageIdentity.SetMessageType(nas.MsgTypeAuthenticationResponse)
	authResponse.AuthenticationResponseParameter = nasType.NewAuthenticationResponseParameter(
		nasMessage.AuthenticationResponseAuthenticationResponseParameterType)
	authResponse.AuthenticationResponseParameter.SetLen(uint8(len(resStar)))
	if len(resStar) <= len(authResponse.AuthenticationResponseParameter.Octet) {
		copy(authResponse.AuthenticationResponseParameter.Octet[:], resStar)
	}

	m.GmmMessage.AuthenticationResponse = authResponse

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode authentication response: %w", err)
	}

	return buf.Bytes(), nil
}

// BuildSecurityModeComplete builds a Security Mode Complete NAS message
func BuildSecurityModeComplete(registrationRequest []byte) ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeSecurityModeComplete)

	securityModeComplete := nasMessage.NewSecurityModeComplete(0)
	securityModeComplete.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	securityModeComplete.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	securityModeComplete.SpareHalfOctetAndSecurityHeaderType.SetSpareHalfOctet(0x00)
	securityModeComplete.SecurityModeCompleteMessageIdentity.SetMessageType(nas.MsgTypeSecurityModeComplete)

	if registrationRequest != nil {
		securityModeComplete.NASMessageContainer = nasType.NewNASMessageContainer(
			nasMessage.SecurityModeCompleteNASMessageContainerType)
		securityModeComplete.NASMessageContainer.SetLen(uint16(len(registrationRequest)))
		securityModeComplete.NASMessageContainer.SetNASMessageContainerContents(registrationRequest)
	}

	m.GmmMessage.SecurityModeComplete = securityModeComplete

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode security mode complete: %w", err)
	}

	return buf.Bytes(), nil
}

// BuildRegistrationComplete builds a Registration Complete NAS message
func BuildRegistrationComplete() ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeRegistrationComplete)

	registrationComplete := nasMessage.NewRegistrationComplete(0)
	registrationComplete.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	registrationComplete.SpareHalfOctetAndSecurityHeaderType.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	registrationComplete.SpareHalfOctetAndSecurityHeaderType.SetSpareHalfOctet(0x00)
	registrationComplete.RegistrationCompleteMessageIdentity.SetMessageType(nas.MsgTypeRegistrationComplete)

	m.GmmMessage.RegistrationComplete = registrationComplete

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode registration complete: %w", err)
	}

	return buf.Bytes(), nil
}

// BuildIdentityResponse builds Identity Response message with Mobile Identity
func BuildIdentityResponse(supi string) ([]byte, error) {
	return BuildIdentityResponseWithType(supi, nasMessage.MobileIdentity5GSTypeSuci)
}

// BuildIdentityResponseWithType builds Identity Response with specific identity type
func BuildIdentityResponseWithType(supi string, identityType uint8) ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeIdentityResponse)

	identityResponse := nasMessage.NewIdentityResponse(0)
	identityResponse.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	identityResponse.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	identityResponse.SetMessageType(nas.MsgTypeIdentityResponse)

	// Build Mobile Identity based on requested type
	var mobileIdentity nasType.MobileIdentity

	switch identityType {
	case nasMessage.MobileIdentity5GSTypeSuci: // Type 1: SUCI (SUPI)
		mobileIdentity5GS := BuildMobileIdentity5GS(supi)
		mobileIdentity = nasType.MobileIdentity{
			Len:    mobileIdentity5GS.Len,
			Buffer: mobileIdentity5GS.Buffer,
		}
	case nasMessage.MobileIdentity5GSTypeImei: // Type 3: IMEI
		// Build IMEI (15 digits): 490154203237518 (valid checksum)
		// Format: byte0 = type(低3位) | odd(bit3) | digit1(高4位)
		//         remaining bytes in BCD (low nibble first)
		imeiBytes := make([]byte, 8)
		imeiBytes[0] = 0x4B // type=3(011), odd=1, digit1=4 → 0100 1011
		// Remaining 14 digits in BCD: 9,0,1,5,4,2,0,3,2,3,7,5,1,8
		imeiBytes[1] = 0x09 // 9, 0
		imeiBytes[2] = 0x51 // 1, 5
		imeiBytes[3] = 0x24 // 4, 2
		imeiBytes[4] = 0x30 // 0, 3
		imeiBytes[5] = 0x32 // 2, 3
		imeiBytes[6] = 0x57 // 7, 5
		imeiBytes[7] = 0x81 // 1, 8
		mobileIdentity = nasType.MobileIdentity{
			Len:    uint16(len(imeiBytes)),
			Buffer: imeiBytes,
		}
	case nasMessage.MobileIdentity5GSTypeImeisv: // Type 5: IMEISV
		// Build IMEISV if requested (use dummy for now)
		imeisvBytes := make([]byte, 10)
		imeisvBytes[0] = 0x03 // Type of identity = IMEISV (bits 0-2), odd/even = 1 (bit 3)
		// Fill with dummy IMEISV: 35609204079301 (14 digits + software version)
		copy(imeisvBytes[1:], []byte{0x53, 0x06, 0x92, 0x40, 0x07, 0x93, 0x10, 0xff, 0xff})
		mobileIdentity = nasType.MobileIdentity{
			Len:    uint16(len(imeisvBytes)),
			Buffer: imeisvBytes,
		}
	default:
		// For other types, use SUCI as fallback
		fmt.Printf("WARNING: Unsupported identity type %d requested, using SUCI\n", identityType)
		mobileIdentity5GS := BuildMobileIdentity5GS(supi)
		mobileIdentity = nasType.MobileIdentity{
			Len:    mobileIdentity5GS.Len,
			Buffer: mobileIdentity5GS.Buffer,
		}
	}

	identityResponse.MobileIdentity = mobileIdentity

	m.GmmMessage.IdentityResponse = identityResponse

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode identity response: %w", err)
	}

	return buf.Bytes(), nil
}

// BuildConfigurationUpdateComplete builds Configuration Update Complete message
func BuildConfigurationUpdateComplete() ([]byte, error) {
	m := nas.NewMessage()
	m.GmmMessage = nas.NewGmmMessage()
	m.GmmHeader.SetMessageType(nas.MsgTypeConfigurationUpdateComplete)

	configUpdateComplete := nasMessage.NewConfigurationUpdateComplete(0)
	configUpdateComplete.SetExtendedProtocolDiscriminator(nasMessage.Epd5GSMobilityManagementMessage)
	configUpdateComplete.SetSecurityHeaderType(nas.SecurityHeaderTypePlainNas)
	configUpdateComplete.SetMessageType(nas.MsgTypeConfigurationUpdateComplete)

	m.GmmMessage.ConfigurationUpdateComplete = configUpdateComplete

	buf := new(bytes.Buffer)
	if err := m.GmmMessageEncode(buf); err != nil {
		return nil, fmt.Errorf("failed to encode configuration update complete: %w", err)
	}

	return buf.Bytes(), nil
}
