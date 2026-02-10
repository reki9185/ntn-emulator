package ngap

import (
	"fmt"

	"github.com/free5gc/aper"
	"github.com/free5gc/ngap"
	"github.com/free5gc/ngap/ngapType"
)

// NGSetupHandler handles NG Setup procedure with AMF
type NGSetupHandler struct {
	client  *NGAPClient
	gnbID   []byte
	gnbName string
	tac     int
}

// NewNGSetupHandler creates a new NG Setup handler
func NewNGSetupHandler(client *NGAPClient, gnbID []byte, gnbName string, tac int) *NGSetupHandler {
	return &NGSetupHandler{
		client:  client,
		gnbID:   gnbID,
		gnbName: gnbName,
		tac:     tac,
	}
}

// PerformNGSetup performs the NG Setup procedure
func (h *NGSetupHandler) PerformNGSetup() error {
	// Build NG Setup Request
	ngSetupRequest, err := h.buildNGSetupRequest()
	if err != nil {
		return fmt.Errorf("failed to build NG Setup Request: %w", err)
	}

	// Send NG Setup Request
	_, err = h.client.Send(ngSetupRequest)
	if err != nil {
		return fmt.Errorf("failed to send NG Setup Request: %w", err)
	}

	// Receive NG Setup Response
	recvBuf := make([]byte, 2048)
	n, err := h.client.Receive(recvBuf)
	if err != nil {
		return fmt.Errorf("failed to receive NG Setup Response: %w", err)
	}

	// Decode NG Setup Response
	ngapPdu, err := ngap.Decoder(recvBuf[:n])
	if err != nil {
		return fmt.Errorf("failed to decode NG Setup Response: %w", err)
	}

	// Verify it's a successful response
	if ngapPdu.Present != ngapType.NGAPPDUPresentSuccessfulOutcome {
		return fmt.Errorf("NG Setup failed: unexpected NGAP PDU type %d", ngapPdu.Present)
	}

	if ngapPdu.SuccessfulOutcome.ProcedureCode.Value != ngapType.ProcedureCodeNGSetup {
		return fmt.Errorf("NG Setup failed: unexpected procedure code %d", ngapPdu.SuccessfulOutcome.ProcedureCode.Value)
	}

	return nil
}

// buildNGSetupRequest builds an NG Setup Request NGAP message
func (h *NGSetupHandler) buildNGSetupRequest() ([]byte, error) {
	// Create NGAP PDU
	pdu := ngapType.NGAPPDU{
		Present: ngapType.NGAPPDUPresentInitiatingMessage,
		InitiatingMessage: &ngapType.InitiatingMessage{
			ProcedureCode: ngapType.ProcedureCode{
				Value: ngapType.ProcedureCodeNGSetup,
			},
			Criticality: ngapType.Criticality{
				Value: ngapType.CriticalityPresentReject,
			},
			Value: ngapType.InitiatingMessageValue{
				Present: ngapType.InitiatingMessagePresentNGSetupRequest,
				NGSetupRequest: &ngapType.NGSetupRequest{
					ProtocolIEs: ngapType.ProtocolIEContainerNGSetupRequestIEs{
						List: []ngapType.NGSetupRequestIEs{},
					},
				},
			},
		},
	}

	// Add Global RAN Node ID
	globalRANNodeID := h.buildGlobalRANNodeID()
	pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List = append(
		pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List,
		ngapType.NGSetupRequestIEs{
			Id:          ngapType.ProtocolIEID{Value: ngapType.ProtocolIEIDGlobalRANNodeID},
			Criticality: ngapType.Criticality{Value: ngapType.CriticalityPresentReject},
			Value: ngapType.NGSetupRequestIEsValue{
				Present:         ngapType.NGSetupRequestIEsPresentGlobalRANNodeID,
				GlobalRANNodeID: &globalRANNodeID,
			},
		},
	)

	// Add RAN Node Name (optional, but useful for NTN identification)
	if h.gnbName != "" {
		pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List = append(
			pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List,
			ngapType.NGSetupRequestIEs{
				Id:          ngapType.ProtocolIEID{Value: ngapType.ProtocolIEIDRANNodeName},
				Criticality: ngapType.Criticality{Value: ngapType.CriticalityPresentIgnore},
				Value: ngapType.NGSetupRequestIEsValue{
					Present: ngapType.NGSetupRequestIEsPresentRANNodeName,
					RANNodeName: &ngapType.RANNodeName{
						Value: h.gnbName,
					},
				},
			},
		)
	}

	// Add Supported TA List
	supportedTAList := h.buildSupportedTAList()
	pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List = append(
		pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List,
		ngapType.NGSetupRequestIEs{
			Id:          ngapType.ProtocolIEID{Value: ngapType.ProtocolIEIDSupportedTAList},
			Criticality: ngapType.Criticality{Value: ngapType.CriticalityPresentReject},
			Value: ngapType.NGSetupRequestIEsValue{
				Present:         ngapType.NGSetupRequestIEsPresentSupportedTAList,
				SupportedTAList: &supportedTAList,
			},
		},
	)

	// Add Default Paging DRX
	defaultPagingDRX := ngapType.PagingDRX{
		Value: ngapType.PagingDRXPresentV128,
	}
	pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List = append(
		pdu.InitiatingMessage.Value.NGSetupRequest.ProtocolIEs.List,
		ngapType.NGSetupRequestIEs{
			Id:          ngapType.ProtocolIEID{Value: ngapType.ProtocolIEIDDefaultPagingDRX},
			Criticality: ngapType.Criticality{Value: ngapType.CriticalityPresentIgnore},
			Value: ngapType.NGSetupRequestIEsValue{
				Present:          ngapType.NGSetupRequestIEsPresentDefaultPagingDRX,
				DefaultPagingDRX: &defaultPagingDRX,
			},
		},
	)

	// Encode the PDU
	return ngap.Encoder(pdu)
}

// buildGlobalRANNodeID builds the Global RAN Node ID
func (h *NGSetupHandler) buildGlobalRANNodeID() ngapType.GlobalRANNodeID {
	return ngapType.GlobalRANNodeID{
		Present: ngapType.GlobalRANNodeIDPresentGlobalGNBID,
		GlobalGNBID: &ngapType.GlobalGNBID{
			PLMNIdentity: ngapType.PLMNIdentity{
				Value: aper.OctetString("\x02\xf8\x39"), // MCC=208, MNC=93 (correct encoding)
			},
			GNBID: ngapType.GNBID{
				Present: ngapType.GNBIDPresentGNBID,
				GNBID: &aper.BitString{
					Bytes:     h.gnbID,
					BitLength: uint64(len(h.gnbID) * 8),
				},
			},
		},
	}
}

// buildSupportedTAList builds the Supported TA List
func (h *NGSetupHandler) buildSupportedTAList() ngapType.SupportedTAList {
	broadcastPLMN := ngapType.BroadcastPLMNItem{
		PLMNIdentity: ngapType.PLMNIdentity{
			Value: aper.OctetString("\x02\xf8\x39"), // MCC=208, MNC=93 (correct encoding)
		},
		TAISliceSupportList: ngapType.SliceSupportList{
			List: []ngapType.SliceSupportItem{
				{
					SNSSAI: ngapType.SNSSAI{
						SST: ngapType.SST{
							Value: aper.OctetString("\x01"),
						},
						SD: &ngapType.SD{
							Value: aper.OctetString("\x01\x02\x03"),
						},
					},
				},
			},
		},
	}

	return ngapType.SupportedTAList{
		List: []ngapType.SupportedTAItem{
			{
				TAC: ngapType.TAC{
					Value: aper.OctetString{uint8(h.tac >> 16), uint8(h.tac >> 8), uint8(h.tac)},
				},
				BroadcastPLMNList: ngapType.BroadcastPLMNList{
					List: []ngapType.BroadcastPLMNItem{broadcastPLMN},
				},
			},
		},
	}
}
