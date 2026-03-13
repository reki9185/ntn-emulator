package ngap

import (
	"fmt"

	"github.com/free5gc/aper"
	"github.com/free5gc/ngap"
	"github.com/free5gc/ngap/ngapConvert"
	"github.com/free5gc/ngap/ngapType"
)

// PathSwitchHandler handles Path Switch Request procedure (for handover simulation)
type PathSwitchHandler struct {
	client *NGAPClient
}

// NewPathSwitchHandler creates a new Path Switch handler
func NewPathSwitchHandler(client *NGAPClient) *PathSwitchHandler {
	return &PathSwitchHandler{
		client: client,
	}
}

// SendPathSwitchRequest sends a Path Switch Request to AMF
// This is used by the target RAN (RAN-B) to inform AMF about the new path
//
// Parameters:
//   - amfUeNgapID: The AMF UE NGAP ID (from the UE context)
//   - ranUeNgapID: The new RAN UE NGAP ID (assigned by RAN-B)
//   - pduSessionID: The PDU Session ID to switch
//   - newRanN3IP: The new RAN's N3 interface IP address
//   - newDLTEID: The new downlink TEID for this RAN
//   - plmnID: PLMN Identity
//   - nrCellID: NR Cell Identity (36 bits)
//   - tac: Tracking Area Code
//
// Returns error if the request fails
func (h *PathSwitchHandler) SendPathSwitchRequest(
	amfUeNgapID, ranUeNgapID int64,
	pduSessionID uint8,
	newRanN3IP string,
	newDLTEID uint32,
	plmnID ngapType.PLMNIdentity,
	nrCellID []byte, // 5 bytes for 36-bit NR Cell ID
	tac []byte, // 3 bytes for TAC
) error {
	// Build Path Switch Request message
	pdu := h.buildPathSwitchRequest(
		amfUeNgapID,
		ranUeNgapID,
		pduSessionID,
		newRanN3IP,
		newDLTEID,
		plmnID,
		nrCellID,
		tac,
	)

	// Encode NGAP message
	ngapMsg, err := ngap.Encoder(pdu)
	if err != nil {
		return fmt.Errorf("failed to encode Path Switch Request: %w", err)
	}

	// Send to AMF
	_, err = h.client.Send(ngapMsg)
	if err != nil {
		return fmt.Errorf("failed to send Path Switch Request: %w", err)
	}

	return nil
}

// ReceivePathSwitchRequestAcknowledge receives and processes Path Switch Request Acknowledge
// Returns the UL TEID that UPF expects for uplink packets, or error
func (h *PathSwitchHandler) ReceivePathSwitchRequestAcknowledge() (ulTEID uint32, upfN3IP string, err error) {
	// Receive NGAP message
	recvBuf := make([]byte, 65535)
	n, err := h.client.Receive(recvBuf)
	if err != nil {
		return 0, "", fmt.Errorf("failed to receive Path Switch Request Acknowledge: %w", err)
	}

	// Decode NGAP PDU
	ngapPdu, err := ngap.Decoder(recvBuf[:n])
	if err != nil {
		return 0, "", fmt.Errorf("failed to decode Path Switch Request Acknowledge: %w", err)
	}

	// Verify it's a successful outcome
	if ngapPdu.Present != ngapType.NGAPPDUPresentSuccessfulOutcome {
		return 0, "", fmt.Errorf("expected successful outcome, got %d", ngapPdu.Present)
	}

	if ngapPdu.SuccessfulOutcome.ProcedureCode.Value != ngapType.ProcedureCodePathSwitchRequest {
		return 0, "", fmt.Errorf("expected Path Switch procedure, got %d", ngapPdu.SuccessfulOutcome.ProcedureCode.Value)
	}

	// Extract Path Switch Request Acknowledge
	ack := ngapPdu.SuccessfulOutcome.Value.PathSwitchRequestAcknowledge
	if ack == nil {
		return 0, "", fmt.Errorf("Path Switch Request Acknowledge is nil")
	}

	// Extract UL GTP-U tunnel info from PDU Session Resource Switched List
	for _, ie := range ack.ProtocolIEs.List {
		if ie.Id.Value == ngapType.ProtocolIEIDPDUSessionResourceSwitchedList {
			if ie.Value.PDUSessionResourceSwitchedList != nil {
				for _, item := range ie.Value.PDUSessionResourceSwitchedList.List {
					// Decode PathSwitchRequestAcknowledgeTransfer
					transfer := ngapType.PathSwitchRequestAcknowledgeTransfer{}
					err := aper.UnmarshalWithParams(item.PathSwitchRequestAcknowledgeTransfer, &transfer, "valueExt")
					if err != nil {
						return 0, "", fmt.Errorf("failed to decode PathSwitchRequestAcknowledgeTransfer: %w", err)
					}

					// Extract UL NG-U UP TNL Information (UPF's uplink tunnel info)
					if transfer.ULNGUUPTNLInformation.Present == ngapType.UPTransportLayerInformationPresentGTPTunnel {
						gtpTunnel := transfer.ULNGUUPTNLInformation.GTPTunnel
						if gtpTunnel != nil {
							// Extract TEID
							if len(gtpTunnel.GTPTEID.Value) == 4 {
								ulTEID = uint32(gtpTunnel.GTPTEID.Value[0])<<24 |
									uint32(gtpTunnel.GTPTEID.Value[1])<<16 |
									uint32(gtpTunnel.GTPTEID.Value[2])<<8 |
									uint32(gtpTunnel.GTPTEID.Value[3])
							}

							// Extract UPF N3 IP address
							upfN3IP, _ = ngapConvert.IPAddressToString(gtpTunnel.TransportLayerAddress)

							return ulTEID, upfN3IP, nil
						}
					}
				}
			}
		}
	}

	return 0, "", fmt.Errorf("no UL tunnel information found in Path Switch Request Acknowledge")
}

// buildPathSwitchRequest constructs the Path Switch Request NGAP message
func (h *PathSwitchHandler) buildPathSwitchRequest(
	amfUeNgapID, ranUeNgapID int64,
	pduSessionID uint8,
	newRanN3IP string,
	newDLTEID uint32,
	plmnID ngapType.PLMNIdentity,
	nrCellID []byte,
	tac []byte,
) ngapType.NGAPPDU {
	pdu := ngapType.NGAPPDU{
		Present:           ngapType.NGAPPDUPresentInitiatingMessage,
		InitiatingMessage: &ngapType.InitiatingMessage{},
	}

	initiatingMessage := pdu.InitiatingMessage
	initiatingMessage.ProcedureCode.Value = ngapType.ProcedureCodePathSwitchRequest
	initiatingMessage.Criticality.Value = ngapType.CriticalityPresentReject

	initiatingMessage.Value.Present = ngapType.InitiatingMessagePresentPathSwitchRequest
	initiatingMessage.Value.PathSwitchRequest = &ngapType.PathSwitchRequest{}

	pathSwitchRequest := initiatingMessage.Value.PathSwitchRequest
	pathSwitchRequestIEs := &pathSwitchRequest.ProtocolIEs

	// 1. RAN UE NGAP ID
	ie := ngapType.PathSwitchRequestIEs{}
	ie.Id.Value = ngapType.ProtocolIEIDRANUENGAPID
	ie.Criticality.Value = ngapType.CriticalityPresentReject
	ie.Value.Present = ngapType.PathSwitchRequestIEsPresentRANUENGAPID
	ie.Value.RANUENGAPID = &ngapType.RANUENGAPID{Value: ranUeNgapID}
	pathSwitchRequestIEs.List = append(pathSwitchRequestIEs.List, ie)

	// 2. Source AMF UE NGAP ID (this is the AMF UE NGAP ID from the source RAN)
	ie = ngapType.PathSwitchRequestIEs{}
	ie.Id.Value = ngapType.ProtocolIEIDSourceAMFUENGAPID
	ie.Criticality.Value = ngapType.CriticalityPresentReject
	ie.Value.Present = ngapType.PathSwitchRequestIEsPresentSourceAMFUENGAPID
	ie.Value.SourceAMFUENGAPID = &ngapType.AMFUENGAPID{Value: amfUeNgapID}
	pathSwitchRequestIEs.List = append(pathSwitchRequestIEs.List, ie)

	// 3. User Location Information (new RAN's location)
	ie = ngapType.PathSwitchRequestIEs{}
	ie.Id.Value = ngapType.ProtocolIEIDUserLocationInformation
	ie.Criticality.Value = ngapType.CriticalityPresentIgnore
	ie.Value.Present = ngapType.PathSwitchRequestIEsPresentUserLocationInformation
	ie.Value.UserLocationInformation = &ngapType.UserLocationInformation{
		Present: ngapType.UserLocationInformationPresentUserLocationInformationNR,
		UserLocationInformationNR: &ngapType.UserLocationInformationNR{
			NRCGI: ngapType.NRCGI{
				PLMNIdentity:   plmnID,
				NRCellIdentity: ngapType.NRCellIdentity{Value: aper.BitString{Bytes: nrCellID, BitLength: 36}},
			},
			TAI: ngapType.TAI{
				PLMNIdentity: plmnID,
				TAC:          ngapType.TAC{Value: aper.OctetString(tac)},
			},
		},
	}
	pathSwitchRequestIEs.List = append(pathSwitchRequestIEs.List, ie)

	// 4. UE Security Capabilities (copied from UE context)
	ie = ngapType.PathSwitchRequestIEs{}
	ie.Id.Value = ngapType.ProtocolIEIDUESecurityCapabilities
	ie.Criticality.Value = ngapType.CriticalityPresentIgnore
	ie.Value.Present = ngapType.PathSwitchRequestIEsPresentUESecurityCapabilities
	ie.Value.UESecurityCapabilities = &ngapType.UESecurityCapabilities{
		NRencryptionAlgorithms:             ngapType.NRencryptionAlgorithms{Value: aper.BitString{Bytes: []byte{0xff, 0xff}, BitLength: 16}},
		NRintegrityProtectionAlgorithms:    ngapType.NRintegrityProtectionAlgorithms{Value: aper.BitString{Bytes: []byte{0xff, 0xff}, BitLength: 16}},
		EUTRAencryptionAlgorithms:          ngapType.EUTRAencryptionAlgorithms{Value: aper.BitString{Bytes: []byte{0xff, 0xff}, BitLength: 16}},
		EUTRAintegrityProtectionAlgorithms: ngapType.EUTRAintegrityProtectionAlgorithms{Value: aper.BitString{Bytes: []byte{0xff, 0xff}, BitLength: 16}},
	}
	pathSwitchRequestIEs.List = append(pathSwitchRequestIEs.List, ie)

	// 5. PDU Session Resource To Be Switched DL List
	ie = ngapType.PathSwitchRequestIEs{}
	ie.Id.Value = ngapType.ProtocolIEIDPDUSessionResourceToBeSwitchedDLList
	ie.Criticality.Value = ngapType.CriticalityPresentReject
	ie.Value.Present = ngapType.PathSwitchRequestIEsPresentPDUSessionResourceToBeSwitchedDLList
	ie.Value.PDUSessionResourceToBeSwitchedDLList = &ngapType.PDUSessionResourceToBeSwitchedDLList{
		List: []ngapType.PDUSessionResourceToBeSwitchedDLItem{
			{
				PDUSessionID:              ngapType.PDUSessionID{Value: int64(pduSessionID)},
				PathSwitchRequestTransfer: h.buildPathSwitchRequestTransfer(newRanN3IP, newDLTEID),
			},
		},
	}
	pathSwitchRequestIEs.List = append(pathSwitchRequestIEs.List, ie)

	return pdu
}

// buildPathSwitchRequestTransfer creates the PathSwitchRequestTransfer IE
// This contains the new RAN's GTP-U tunnel information for downlink
func (h *PathSwitchHandler) buildPathSwitchRequestTransfer(newRanN3IP string, newDLTEID uint32) []byte {
	// Build the transfer structure
	transfer := ngapType.PathSwitchRequestTransfer{}

	// DL NG-U UP TNL Information (where UPF should send downlink packets)
	transfer.DLNGUUPTNLInformation.Present = ngapType.UPTransportLayerInformationPresentGTPTunnel
	transfer.DLNGUUPTNLInformation.GTPTunnel = &ngapType.GTPTunnel{
		TransportLayerAddress: ngapConvert.IPAddressToNgap(newRanN3IP, ""),
		GTPTEID: ngapType.GTPTEID{
			Value: aper.OctetString([]byte{
				byte(newDLTEID >> 24),
				byte(newDLTEID >> 16),
				byte(newDLTEID >> 8),
				byte(newDLTEID),
			}),
		},
	}

	// QoS Flow Accepted List (typically QFI 1 for default bearer)
	transfer.QosFlowAcceptedList.List = []ngapType.QosFlowAcceptedItem{
		{
			QosFlowIdentifier: ngapType.QosFlowIdentifier{Value: 1},
		},
	}

	// Encode the transfer
	encoded, err := aper.MarshalWithParams(transfer, "valueExt")
	if err != nil {
		panic(fmt.Sprintf("failed to encode PathSwitchRequestTransfer: %v", err))
	}

	return encoded
}
