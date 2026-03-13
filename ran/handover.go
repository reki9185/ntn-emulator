package ran

import (
	"fmt"
	"log"

	"ntn-emulator/config"
	"ntn-emulator/ran/gtp"
	"ntn-emulator/ran/ngap"

	"github.com/free5gc/ngap/ngapType"
)

// HandoverContext stores information needed during handover
type HandoverContext struct {
	// UE context information
	IMSI         string
	AmfUeNgapID  int64
	RanUeNgapID  int64 // This will be different on target RAN
	PDUSessionID uint8

	// Network configuration
	PLMNID   ngapType.PLMNIdentity
	TAC      []byte // 3 bytes
	NRCellID []byte // 5 bytes for 36-bit cell ID

	// GTP-U tunnel information
	CurrentULTEID uint32 // Current uplink TEID (to UPF)
	CurrentDLTEID uint32 // Current downlink TEID (from UPF)
	UPFN3IP       string // UPF N3 interface IP

	// New RAN's tunnel information
	NewRanN3IP string // Target RAN's N3 IP address
	NewDLTEID  uint32 // Target RAN's new downlink TEID
}

// HandoverManager manages the simplified handover procedure using Path Switch
type HandoverManager struct {
	ngapClient        *ngap.NGAPClient
	pathSwitchHandler *ngap.PathSwitchHandler
	ranConfig         *config.RANConfig
}

// NewHandoverManager creates a new handover manager
func NewHandoverManager(ngapClient *ngap.NGAPClient, ranConfig *config.RANConfig) *HandoverManager {
	return &HandoverManager{
		ngapClient:        ngapClient,
		pathSwitchHandler: ngap.NewPathSwitchHandler(ngapClient),
		ranConfig:         ranConfig,
	}
}

// PerformPathSwitch executes the Path Switch procedure (target RAN side)
//
// This is called by RAN-B (target gNB) after the UE has moved from RAN-A (source gNB).
// Instead of implementing full Xn handover signaling, we directly trigger the path switch.
//
// Flow:
//  1. RAN-B sends PathSwitchRequest to AMF with new tunnel info
//  2. AMF updates the UPF with new downlink tunnel (RAN-B's N3 IP + DL TEID)
//  3. AMF responds with PathSwitchRequestAcknowledge containing uplink tunnel info
//  4. RAN-B updates its GTP-U tunnel with the UL TEID
//  5. Data path is now: UE <-> RAN-B <-> UPF
//
// Returns: New GTP-U tunnel, or error
func (hm *HandoverManager) PerformPathSwitch(ctx *HandoverContext) (*gtp.GTPTunnel, error) {
	log.Printf("🔄 [Path Switch] Starting path switch for UE IMSI=%s", ctx.IMSI)
	log.Printf("   AmfUeNgapID=%d, RanUeNgapID=%d, PDUSessionID=%d",
		ctx.AmfUeNgapID, ctx.RanUeNgapID, ctx.PDUSessionID)
	log.Printf("   New RAN N3 IP=%s, New DL TEID=0x%08x", ctx.NewRanN3IP, ctx.NewDLTEID)

	// Step 1: Send Path Switch Request to AMF
	err := hm.pathSwitchHandler.SendPathSwitchRequest(
		ctx.AmfUeNgapID,
		ctx.RanUeNgapID,
		ctx.PDUSessionID,
		ctx.NewRanN3IP,
		ctx.NewDLTEID,
		ctx.PLMNID,
		ctx.NRCellID,
		ctx.TAC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send Path Switch Request: %w", err)
	}
	log.Println("✓ [Path Switch] Path Switch Request sent to AMF")

	// Step 2: Receive Path Switch Request Acknowledge
	// This contains the UL TEID that UPF expects for uplink traffic
	ulTEID, upfN3IP, err := hm.pathSwitchHandler.ReceivePathSwitchRequestAcknowledge()
	if err != nil {
		return nil, fmt.Errorf("failed to receive Path Switch Request Acknowledge: %w", err)
	}
	log.Printf("✓ [Path Switch] Path Switch Request Acknowledge received")
	log.Printf("   UPF N3 IP=%s, UL TEID=0x%08x (for uplink to UPF)", upfN3IP, ulTEID)

	// Step 3: Create new GTP-U tunnel with updated information
	// Note: The UPF now knows to send downlink to ctx.NewRanN3IP with ctx.NewDLTEID
	// We need to send uplink to UPF using the ulTEID returned in the acknowledge
	gtpTunnel, err := gtp.NewGTPTunnelWithN3IP(
		ctx.NewDLTEID,                       // Local TEID (for receiving downlink)
		ulTEID,                              // Remote TEID (for sending uplink to UPF)
		fmt.Sprintf("%s:%d", upfN3IP, 2152), // UPF address
		ctx.NewRanN3IP,                      // Bind to new RAN's N3 IP
		nil,                                 // TUN interface (set this appropriately)
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create GTP tunnel: %w", err)
	}

	log.Println("✓ [Path Switch] New GTP-U tunnel established")
	log.Printf("   Uplink:   RAN -> UPF (Remote TEID: 0x%08x)", ulTEID)
	log.Printf("   Downlink: UPF -> RAN (Local TEID: 0x%08x)", ctx.NewDLTEID)

	return gtpTunnel, nil
}

// Example usage function showing the complete handover flow
func ExampleHandoverFlow() {
	// This example shows how RAN-B would trigger path switch after UE moves from RAN-A

	// Assume UE was registered via RAN-A and now moves to RAN-B
	// RAN-B receives UE attachment (in your case, perhaps via the data plane "INIT" packet)

	// Step 1: RAN-B needs to know the UE context from RAN-A
	// In a real system, this would come via Xn interface or from the UE's RRC message
	// For your simplified approach, you might maintain this in a shared database or
	// have the UE send its context
	_ = &HandoverContext{
		IMSI:         "imsi-208930000007487",
		AmfUeNgapID:  1,   // From the original registration via RAN-A
		RanUeNgapID:  100, // NEW ID assigned by RAN-B
		PDUSessionID: 10,

		// Location of RAN-B
		PLMNID:   ngapType.PLMNIdentity{Value: []byte{0x02, 0xf8, 0x39}},
		TAC:      []byte{0x00, 0x00, 0x11},             // RAN-B's TAC
		NRCellID: []byte{0x00, 0x00, 0x00, 0x00, 0x20}, // RAN-B's Cell ID

		// New RAN-B's tunnel info
		NewRanN3IP: "10.200.200.2", // RAN-B's N3 interface IP
		NewDLTEID:  0x00000003,     // RAN-B assigns a new DL TEID

		// UPF info (from original session)
		UPFN3IP: "10.200.200.102",
	}

	// Step 2: Create handover manager (already connected to AMF via NGAP)
	// var ngapClient *ngap.NGAPClient  // Assume this is already connected
	// var ranConfig *config.RANConfig  // Assume this is loaded
	// hm := NewHandoverManager(ngapClient, ranConfig)

	// Step 3: Perform path switch
	// newTunnel, err := hm.PerformPathSwitch(handoverCtx)
	// if err != nil {
	// 	log.Fatalf("Path switch failed: %v", err)
	// }

	// Step 4: Start the new GTP-U tunnel
	// newTunnel.Start()

	// Step 5: Data path is now switched
	// UE -> RAN-B -> UPF (using new uplink TEID)
	// UPF -> RAN-B -> UE (using new downlink TEID)

	log.Println("✓ Handover complete - data path switched to RAN-B")
}

// Important Notes:
//
//  1. GTP End Markers:
//     After path switch, UPF may send GTP End Marker packets on the old path (to RAN-A).
//     These indicate the end of data on the old path. You can log and ignore them.
//
//  2. UE Context Transfer:
//     In your simplified approach, RAN-B needs to know:
//     - AmfUeNgapID (from original registration)
//     - PDU Session ID
//     - UE's IMSI
//     You might have the UE send this in the initial "INIT" packet to RAN-B.
//
//  3. Data Plane Update:
//     After path switch:
//     - Stop the old GTP tunnel on RAN-A
//     - Start the new GTP tunnel on RAN-B
//     - Update UE data plane handler to use new tunnel
//
//  4. Timing:
//     The path switch should be quick (< 100ms typically) to minimize packet loss.
//     During the switch, some packets may be lost or buffered.
func PathSwitchInstructions() {
	fmt.Println(`
Path Switch Implementation Guide:
==================================

What Happens During Path Switch:
---------------------------------
1. UE moves from RAN-A to RAN-B
2. RAN-B sends PathSwitchRequest to AMF:
   - Includes RAN-B's N3 IP and new DL TEID
   - Includes UE's AmfUeNgapID (from original registration)
   - Includes new location information (Cell ID, TAC)

3. AMF processes the request:
   - Sends N4 Session Modification to UPF
   - UPF updates its downlink forwarding rule:
     OLD: Send to RAN-A's IP with TEID_A
     NEW: Send to RAN-B's IP with TEID_B
   - UPF may send End Marker to old path

4. AMF sends PathSwitchRequestAcknowledge to RAN-B:
   - Includes the UL TEID for sending to UPF
   - RAN-B updates its GTP-U tunnel

5. Data path is switched:
   UE <--> RAN-B <--> UPF

What Must Be Updated in RAN Data Plane:
----------------------------------------
After receiving PathSwitchRequestAcknowledge:

1. Create new GTP-U tunnel:
   - Local TEID: newDLTEID (for receiving from UPF)
   - Remote TEID: ulTEID from acknowledge (for sending to UPF)
   - Remote Address: UPF N3 IP
   - Bind to: RAN-B's N3 IP

2. Update packet forwarding:
   - Uplink: UE -> RAN-B -> [GTP-U with ulTEID] -> UPF
   - Downlink: UPF -> [GTP-U with newDLTEID] -> RAN-B -> UE

3. Handle GTP End Markers:
   - These come on the old path from UPF
   - Simply log them: "Received End Marker on old path"
   - No action needed - they just signal old path closure

Key Fields in PathSwitchRequestTransfer:
-----------------------------------------
DLNGUUPTNLInformation:
  - TransportLayerAddress: RAN-B's N3 IP (where UPF sends DL)
  - GTPTEID: RAN-B's DL TEID (new tunnel identifier)

QosFlowAcceptedList:
  - QosFlowIdentifier: 1 (for default bearer)
  - Add more QFIs if multiple QoS flows exist

Implementation Steps for Your RAN:
-----------------------------------
1. On RAN-B startup:
   - Connect to AMF via NGAP (NG Setup)
   - Open N3 interface (UDP port 2152 for GTP-U)

2. When UE attaches to RAN-B:
   - UE must provide: AmfUeNgapID, PDUSessionID, IMSI
   - RAN-B assigns new RanUeNgapID
   - RAN-B assigns new DL TEID

3. Call PerformPathSwitch():
   - Send PathSwitchRequest
   - Receive PathSwitchRequestAcknowledge
   - Get UL TEID from acknowledge

4. Update data plane:
   - Create GTP tunnel with new TEIDs
   - Start tunnel forwarding
   - Data flows through RAN-B

Testing the Implementation:
----------------------------
1. Register UE via RAN-A
2. Establish PDU session
3. Test ping: UE -> 8.8.8.8 (should work via RAN-A)
4. Trigger handover (move UE to RAN-B)
5. RAN-B performs path switch
6. Test ping: UE -> 8.8.8.8 (should work via RAN-B)

Debugging Tips:
---------------
- Use tcpdump on N3 interface to see GTP-U packets
- Check TEID in GTP-U header matches expected values
- AMF logs will show path switch procedure progress
- UPF logs will show N4 session modification
	`)
}
