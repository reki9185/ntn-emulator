package ue

import (
	"ntn-emulator/ran/gtp"
	"ntn-emulator/ue/tun"

	"test"

	"github.com/free5gc/nas/security"
	"github.com/free5gc/openapi/models"
)

// UEContext wraps test.RanUeContext for compatibility
type UEContext struct {
	*test.RanUeContext

	// User plane related fields
	TunInterface *tun.TUNInterface
	GTPTunnel    *gtp.GTPTunnel // OLD: direct GTP tunnel to UPF (deprecated for free-ran-ue pattern)
	UEIPAddress  string
	PDUSessionID uint8

	// TEIDs for separated UE/RAN architecture
	UPFTEID uint32 // TEID for uplink (RAN→UPF)
	RANTEID uint32 // TEID for downlink (UPF→RAN)
}

// AuthenticationSubscription stores UE authentication parameters
type AuthenticationSubscription struct {
	AuthMethod models.AuthMethod
	K          string // Permanent Key (hex string)
	OPC        string // Operator Code (hex string)
	OP         string // Operator variant (hex string)
	AMF        string // Authentication Management Field
	SQN        string // Sequence Number
}

// NewUEContext creates a new UE context with default values
func NewUEContext(supi string, ranUeNgapId int64) *UEContext {
	// Create underlying RanUeContext using test package
	ranUe := test.NewRanUeContext(
		supi,
		ranUeNgapId,
		security.AlgCiphering128NEA0,
		security.AlgIntegrity128NIA2,
		models.AccessType__3_GPP_ACCESS,
	)

	return &UEContext{
		RanUeContext: ranUe,
	}
}

// SetState updates the UE state
func (ue *UEContext) SetState(state UEState) {
	// State management can be added here if needed
}

// GetState returns the current UE state
func (ue *UEContext) GetState() UEState {
	// Can return based on RanUeContext state
	return UEStateRegistered // Simplified for now
}

// Cleanup cleans up user plane resources (GTP tunnel and TUN interface)
func (ue *UEContext) Cleanup() {
	if ue.GTPTunnel != nil {
		ue.GTPTunnel.Stop()
		ue.GTPTunnel = nil
	}
	if ue.TunInterface != nil {
		ue.TunInterface.Close()
		ue.TunInterface = nil
	}
}

// UEState represents the state of the UE in its lifecycle
type UEState int

const (
	UEStateIdle UEState = iota
	UEStateRegistering
	UEStateRegistered
	UEStateEstablishingPDU
	UEStatePDUActive
	UEStateDeregistering
)
