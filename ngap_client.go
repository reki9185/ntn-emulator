package ntnemulator

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/free5gc/aper"
	"github.com/free5gc/ngap"
	"github.com/free5gc/ngap/ngapType"
	"github.com/free5gc/sctp"

	"test"
	"test/ngapTestpacket"
)

// NGAPClient handles NGAP transport layer communication
type NGAPClient struct {
	amfN2IP   string
	ranN2IP   string
	amfN2Port int
	ranN2Port int
	conn      *sctp.SCTPConn
}

// NewNGAPClient creates a new NGAP client
func NewNGAPClient(amfN2IP, ranN2IP string, amfN2Port, ranN2Port int) *NGAPClient {
	return &NGAPClient{
		amfN2IP:   amfN2IP,
		ranN2IP:   ranN2IP,
		amfN2Port: amfN2Port,
		ranN2Port: ranN2Port,
	}
}

// Connect establishes SCTP connection to AMF
func (c *NGAPClient) Connect() error {
	amfAddr, ranAddr, err := c.getSCTPAddresses()
	if err != nil {
		return fmt.Errorf("failed to resolve SCTP addresses: %w", err)
	}

	conn, err := sctp.DialSCTP("sctp", ranAddr, amfAddr)
	if err != nil {
		return fmt.Errorf("failed to dial SCTP connection: %w", err)
	}

	c.conn = conn
	return nil
}

// Close closes the NGAP connection
func (c *NGAPClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Send sends an NGAP message to AMF with correct SCTP PPID
func (c *NGAPClient) Send(data []byte) (int, error) {
	if c.conn == nil {
		return 0, fmt.Errorf("NGAP connection not established")
	}

	// NGAP requires SCTP PPID = 60 (0x3c)
	info := &sctp.SndRcvInfo{
		Stream: 0,
		PPID:   0x3c000000,
	}

	return c.conn.SCTPWrite(data, info)
}

// Receive receives an NGAP message from AMF
func (c *NGAPClient) Receive(buffer []byte) (int, error) {
	if c.conn == nil {
		return 0, fmt.Errorf("NGAP connection not established")
	}
	return c.conn.Read(buffer)
}

// GetConnection returns the underlying SCTP connection
func (c *NGAPClient) GetConnection() *sctp.SCTPConn {
	return c.conn
}

// SendInitialUEMessage sends Initial UE Message (for first Registration Request)
func (c *NGAPClient) SendInitialUEMessage(ranUeNgapID int64, nasPdu []byte) error {
	// Build NGAP Initial UE Message using test package
	pdu := ngapTestpacket.BuildInitialUEMessage(ranUeNgapID, nasPdu, "")

	// Encode NGAP PDU
	ngapMsg, err := ngap.Encoder(pdu)
	if err != nil {
		return fmt.Errorf("failed to encode Initial UE Message: %w", err)
	}

	// Send via SCTP
	_, err = c.Send(ngapMsg)
	return err
}

// SendUplinkNASTransport sends Uplink NAS Transport (for subsequent NAS messages)
func (c *NGAPClient) SendUplinkNASTransport(amfUeNgapID, ranUeNgapID int64, nasPdu []byte) error {
	// Build NGAP Uplink NAS Transport using test package
	pdu := ngapTestpacket.BuildUplinkNasTransport(amfUeNgapID, ranUeNgapID, nasPdu)

	// Encode NGAP PDU
	ngapMsg, err := ngap.Encoder(pdu)
	if err != nil {
		return fmt.Errorf("failed to encode Uplink NAS Transport: %w", err)
	}

	// Send via SCTP
	_, err = c.Send(ngapMsg)
	return err
}

// SendInitialContextSetupResponse sends Initial Context Setup Response
func (c *NGAPClient) SendInitialContextSetupResponse(amfUeNgapID, ranUeNgapID int64) error {
	// Build and encode NGAP Initial Context Setup Response using test package
	// Note: test.GetInitialContextSetupResponse already returns encoded bytes
	ngapMsg, err := test.GetInitialContextSetupResponse(amfUeNgapID, ranUeNgapID)
	if err != nil {
		return fmt.Errorf("failed to build Initial Context Setup Response: %w", err)
	}

	// Send via SCTP
	_, err = c.Send(ngapMsg)
	return err
}

// ReceiveNASPDU receives and decodes NGAP message, extracts NAS PDU
// Returns NAS PDU bytes and any AMF/RAN UE NGAP IDs if present
func (c *NGAPClient) ReceiveNASPDU() (nasPdu []byte, amfUeNgapID *int64, ranUeNgapID *int64, err error) {
	// Receive NGAP message
	recvBuf := make([]byte, 65535)
	n, err := c.Receive(recvBuf)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to receive NGAP message: %w", err)
	}

	// Decode NGAP PDU
	pdu, err := ngap.Decoder(recvBuf[:n])
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to decode NGAP PDU: %w", err)
	}

	// Extract NAS PDU from Downlink NAS Transport or Initial Context Setup Request
	if pdu.Present == ngapType.NGAPPDUPresentInitiatingMessage {
		// Check for Downlink NAS Transport
		if pdu.InitiatingMessage.Value.Present == ngapType.InitiatingMessagePresentDownlinkNASTransport {
			downlinkNAS := pdu.InitiatingMessage.Value.DownlinkNASTransport
			if downlinkNAS == nil {
				return nil, nil, nil, fmt.Errorf("downlink NAS transport is nil")
			}

			// Extract IEs
			for _, ie := range downlinkNAS.ProtocolIEs.List {
				switch ie.Id.Value {
				case ngapType.ProtocolIEIDAMFUENGAPID:
					if ie.Value.AMFUENGAPID != nil {
						id := ie.Value.AMFUENGAPID.Value
						amfUeNgapID = &id
					}
				case ngapType.ProtocolIEIDRANUENGAPID:
					if ie.Value.RANUENGAPID != nil {
						id := ie.Value.RANUENGAPID.Value
						ranUeNgapID = &id
					}
				case ngapType.ProtocolIEIDNASPDU:
					if ie.Value.NASPDU != nil {
						nasPdu = ie.Value.NASPDU.Value
					}
				}
			}

			if nasPdu == nil {
				return nil, nil, nil, fmt.Errorf("NAS PDU not found in Downlink NAS Transport")
			}

			return nasPdu, amfUeNgapID, ranUeNgapID, nil
		}

		// Check for Initial Context Setup Request
		if pdu.InitiatingMessage.Value.Present == ngapType.InitiatingMessagePresentInitialContextSetupRequest {
			initialContextSetup := pdu.InitiatingMessage.Value.InitialContextSetupRequest
			if initialContextSetup == nil {
				return nil, nil, nil, fmt.Errorf("initial context setup request is nil")
			}

			// Extract IEs
			for _, ie := range initialContextSetup.ProtocolIEs.List {
				switch ie.Id.Value {
				case ngapType.ProtocolIEIDAMFUENGAPID:
					if ie.Value.AMFUENGAPID != nil {
						id := ie.Value.AMFUENGAPID.Value
						amfUeNgapID = &id
					}
				case ngapType.ProtocolIEIDRANUENGAPID:
					if ie.Value.RANUENGAPID != nil {
						id := ie.Value.RANUENGAPID.Value
						ranUeNgapID = &id
					}
				case ngapType.ProtocolIEIDNASPDU:
					if ie.Value.NASPDU != nil {
						nasPdu = ie.Value.NASPDU.Value
					}
				}
			}

			if nasPdu == nil {
				return nil, nil, nil, fmt.Errorf("NAS PDU not found in Initial Context Setup Request")
			}

			fmt.Println("DEBUG: Received Initial Context Setup Request with NAS PDU")
			return nasPdu, amfUeNgapID, ranUeNgapID, nil
		}

		// Check for PDU Session Resource Setup Request
		if pdu.InitiatingMessage.Value.Present == ngapType.InitiatingMessagePresentPDUSessionResourceSetupRequest {
			pduSessionSetup := pdu.InitiatingMessage.Value.PDUSessionResourceSetupRequest
			if pduSessionSetup == nil {
				return nil, nil, nil, fmt.Errorf("PDU session resource setup request is nil")
			}

			// Extract IEs
			for _, ie := range pduSessionSetup.ProtocolIEs.List {
				switch ie.Id.Value {
				case ngapType.ProtocolIEIDAMFUENGAPID:
					if ie.Value.AMFUENGAPID != nil {
						id := ie.Value.AMFUENGAPID.Value
						amfUeNgapID = &id
					}
				case ngapType.ProtocolIEIDRANUENGAPID:
					if ie.Value.RANUENGAPID != nil {
						id := ie.Value.RANUENGAPID.Value
						ranUeNgapID = &id
					}
				case ngapType.ProtocolIEIDNASPDU:
					if ie.Value.NASPDU != nil {
						nasPdu = ie.Value.NASPDU.Value
					}
				}
			}

			if nasPdu == nil {
				return nil, nil, nil, fmt.Errorf("NAS PDU not found in PDU Session Resource Setup Request")
			}

			fmt.Println("DEBUG: Received PDU Session Resource Setup Request with NAS PDU")
			return nasPdu, amfUeNgapID, ranUeNgapID, nil
		}
	}

	return nil, nil, nil, fmt.Errorf("unexpected NGAP message type (not Downlink NAS Transport or Initial Context Setup)")
}

// getSCTPAddresses resolves and returns SCTP addresses for AMF and RAN
func (c *NGAPClient) getSCTPAddresses() (*sctp.SCTPAddr, *sctp.SCTPAddr, error) {
	amfIps := make([]net.IPAddr, 0)
	ranIps := make([]net.IPAddr, 0)

	// Resolve AMF IP
	if ip, err := net.ResolveIPAddr("ip", c.amfN2IP); err != nil {
		return nil, nil, fmt.Errorf("failed to resolve AMF N2 IP '%s': %w", c.amfN2IP, err)
	} else {
		amfIps = append(amfIps, *ip)
	}

	amfAddr := &sctp.SCTPAddr{
		IPAddrs: amfIps,
		Port:    c.amfN2Port,
	}

	// Resolve RAN IP
	if ip, err := net.ResolveIPAddr("ip", c.ranN2IP); err != nil {
		return nil, nil, fmt.Errorf("failed to resolve RAN N2 IP '%s': %w", c.ranN2IP, err)
	} else {
		ranIps = append(ranIps, *ip)
	}

	ranAddr := &sctp.SCTPAddr{
		IPAddrs: ranIps,
		Port:    c.ranN2Port,
	}

	return amfAddr, ranAddr, nil
}

// PDUSessionSetupInfo contains information from PDU Session Resource Setup Request
type PDUSessionSetupInfo struct {
	UEIPAddress string
	UPFTEID     uint32
	UPFAddress  string
	UPFPort     int
	NASPdu      []byte
}

// ReceivePDUSessionResourceSetupRequest receives and parses PDU Session Resource Setup Request
func (c *NGAPClient) ReceivePDUSessionResourceSetupRequest() (*PDUSessionSetupInfo, error) {
	// Receive NGAP message
	recvBuf := make([]byte, 65535)
	n, err := c.Receive(recvBuf)
	if err != nil {
		return nil, fmt.Errorf("failed to receive NGAP message: %w", err)
	}

	// Decode NGAP PDU
	pdu, err := ngap.Decoder(recvBuf[:n])
	if err != nil {
		return nil, fmt.Errorf("failed to decode NGAP PDU: %w", err)
	}

	// Verify it's PDU Session Resource Setup Request
	if pdu.Present != ngapType.NGAPPDUPresentInitiatingMessage ||
		pdu.InitiatingMessage.Value.Present != ngapType.InitiatingMessagePresentPDUSessionResourceSetupRequest {
		return nil, fmt.Errorf("expected PDU Session Resource Setup Request, got different message")
	}

	setupReq := pdu.InitiatingMessage.Value.PDUSessionResourceSetupRequest
	if setupReq == nil {
		return nil, fmt.Errorf("PDU Session Resource Setup Request is nil")
	}

	info := &PDUSessionSetupInfo{
		UPFPort: 2152, // Default GTP-U port
	}

	// Extract IEs from PDU Session Resource Setup List
	for _, ie := range setupReq.ProtocolIEs.List {
		if ie.Id.Value == ngapType.ProtocolIEIDPDUSessionResourceSetupListSUReq {
			for _, item := range ie.Value.PDUSessionResourceSetupListSUReq.List {
				// Extract NAS PDU if present
				if item.PDUSessionNASPDU != nil {
					info.NASPdu = item.PDUSessionNASPDU.Value
				}

				// Decode PDU Session Resource Setup Request Transfer
				if item.PDUSessionResourceSetupRequestTransfer != nil {
					transfer, err := c.decodePDUSessionResourceSetupRequestTransfer(
						item.PDUSessionResourceSetupRequestTransfer)
					if err != nil {
						return nil, fmt.Errorf("failed to decode transfer: %w", err)
					}

					info.UEIPAddress = transfer.UEIPAddress
					info.UPFTEID = transfer.UPFTEID
					info.UPFAddress = transfer.UPFAddress
				}
			}
		}
	}

	return info, nil
}

// decodePDUSessionResourceSetupRequestTransfer decodes the transfer IE
func (c *NGAPClient) decodePDUSessionResourceSetupRequestTransfer(transferBytes []byte) (*PDUSessionSetupInfo, error) {
	var transfer ngapType.PDUSessionResourceSetupRequestTransfer
	err := aper.UnmarshalWithParams(transferBytes, &transfer, "valueExt")
	if err != nil {
		return nil, fmt.Errorf("APER unmarshal failed: %w", err)
	}

	info := &PDUSessionSetupInfo{}

	for _, ie := range transfer.ProtocolIEs.List {
		switch ie.Id.Value {
		case ngapType.ProtocolIEIDULNGUUPTNLInformation:
			// Extract UPF address and TEID
			if ie.Value.ULNGUUPTNLInformation != nil &&
				ie.Value.ULNGUUPTNLInformation.GTPTunnel != nil {
				tunnel := ie.Value.ULNGUUPTNLInformation.GTPTunnel

				// Extract TEID
				teid, err := ParseTEID(tunnel.GTPTEID.Value)
				if err != nil {
					return nil, fmt.Errorf("failed to parse TEID: %w", err)
				}
				info.UPFTEID = teid

				// Extract UPF IP address
				ipBytes := tunnel.TransportLayerAddress.Value.Bytes
				if len(ipBytes) >= 4 {
					info.UPFAddress = fmt.Sprintf("%d.%d.%d.%d",
						ipBytes[0], ipBytes[1], ipBytes[2], ipBytes[3])
				}
			}

		case ngapType.ProtocolIEIDPDUSessionType:
			// PDU Session Type (IPv4, IPv6, etc.)

		case ngapType.ProtocolIEIDQosFlowSetupRequestList:
			// QoS Flow information
		}
	}

	return info, nil
}

// SendPDUSessionResourceSetupResponse sends PDU Session Resource Setup Response
func (c *NGAPClient) SendPDUSessionResourceSetupResponse(amfUeNgapID, ranUeNgapID int64, pduSessionID uint8, gnbTEID uint32) error {
	// Use default RAN N2 IP (control plane IP)
	return c.SendPDUSessionResourceSetupResponseWithIP(amfUeNgapID, ranUeNgapID, pduSessionID, gnbTEID, c.ranN2IP)
}

// SendPDUSessionResourceSetupResponseWithIP sends PDU Session Resource Setup Response with custom gNB IP
func (c *NGAPClient) SendPDUSessionResourceSetupResponseWithIP(amfUeNgapID, ranUeNgapID int64, pduSessionID uint8, gnbTEID uint32, gnbN3IP string) error {
	// Build PDU Session Resource Setup Response manually
	pdu := buildPDUSessionResourceSetupResponsePDU(amfUeNgapID, ranUeNgapID, pduSessionID, gnbTEID, gnbN3IP)

	// Encode NGAP PDU
	ngapMsg, err := ngap.Encoder(pdu)
	if err != nil {
		return fmt.Errorf("failed to encode PDU session resource setup response: %w", err)
	}

	// Send via SCTP
	_, err = c.Send(ngapMsg)
	return err
}

// buildPDUSessionResourceSetupResponsePDU builds the NGAP PDU for PDU Session Resource Setup Response
func buildPDUSessionResourceSetupResponsePDU(amfUeNgapID, ranUeNgapID int64, pduSessionID uint8, gnbTEID uint32, gnbIP string) ngapType.NGAPPDU {
	// Create response PDU
	pdu := ngapType.NGAPPDU{}
	pdu.Present = ngapType.NGAPPDUPresentSuccessfulOutcome
	pdu.SuccessfulOutcome = new(ngapType.SuccessfulOutcome)
	pdu.SuccessfulOutcome.ProcedureCode.Value = ngapType.ProcedureCodePDUSessionResourceSetup
	pdu.SuccessfulOutcome.Criticality.Value = ngapType.CriticalityPresentReject

	pdu.SuccessfulOutcome.Value.Present = ngapType.SuccessfulOutcomePresentPDUSessionResourceSetupResponse
	pdu.SuccessfulOutcome.Value.PDUSessionResourceSetupResponse = new(ngapType.PDUSessionResourceSetupResponse)

	setupResp := pdu.SuccessfulOutcome.Value.PDUSessionResourceSetupResponse
	setupResp.ProtocolIEs.List = make([]ngapType.PDUSessionResourceSetupResponseIEs, 3)

	// IE 0: AMF UE NGAP ID
	setupResp.ProtocolIEs.List[0].Id.Value = ngapType.ProtocolIEIDAMFUENGAPID
	setupResp.ProtocolIEs.List[0].Criticality.Value = ngapType.CriticalityPresentIgnore
	setupResp.ProtocolIEs.List[0].Value.Present = ngapType.PDUSessionResourceSetupResponseIEsPresentAMFUENGAPID
	setupResp.ProtocolIEs.List[0].Value.AMFUENGAPID = new(ngapType.AMFUENGAPID)
	setupResp.ProtocolIEs.List[0].Value.AMFUENGAPID.Value = amfUeNgapID

	// IE 1: RAN UE NGAP ID
	setupResp.ProtocolIEs.List[1].Id.Value = ngapType.ProtocolIEIDRANUENGAPID
	setupResp.ProtocolIEs.List[1].Criticality.Value = ngapType.CriticalityPresentIgnore
	setupResp.ProtocolIEs.List[1].Value.Present = ngapType.PDUSessionResourceSetupResponseIEsPresentRANUENGAPID
	setupResp.ProtocolIEs.List[1].Value.RANUENGAPID = new(ngapType.RANUENGAPID)
	setupResp.ProtocolIEs.List[1].Value.RANUENGAPID.Value = ranUeNgapID

	// IE 2: PDU Session Resource Setup List
	setupResp.ProtocolIEs.List[2].Id.Value = ngapType.ProtocolIEIDPDUSessionResourceSetupListSURes
	setupResp.ProtocolIEs.List[2].Criticality.Value = ngapType.CriticalityPresentIgnore
	setupResp.ProtocolIEs.List[2].Value.Present = ngapType.PDUSessionResourceSetupResponseIEsPresentPDUSessionResourceSetupListSURes
	setupResp.ProtocolIEs.List[2].Value.PDUSessionResourceSetupListSURes = new(ngapType.PDUSessionResourceSetupListSURes)

	// PDU Session Resource Setup Item
	setupResp.ProtocolIEs.List[2].Value.PDUSessionResourceSetupListSURes.List = make([]ngapType.PDUSessionResourceSetupItemSURes, 1)
	setupItem := &setupResp.ProtocolIEs.List[2].Value.PDUSessionResourceSetupListSURes.List[0]

	setupItem.PDUSessionID.Value = int64(pduSessionID)

	// Build PDU Session Resource Setup Response Transfer
	transfer := buildPDUSessionResourceSetupResponseTransfer(gnbTEID, gnbIP)
	transferBytes, _ := aper.MarshalWithParams(transfer, "valueExt")
	setupItem.PDUSessionResourceSetupResponseTransfer = transferBytes

	fmt.Printf("📤 NGAP: Sending PDU Session Resource Setup Response with gNB IP=%s, TEID=0x%08x\n", gnbIP, gnbTEID)

	return pdu
}

// buildPDUSessionResourceSetupResponseTransfer builds the transfer IE
func buildPDUSessionResourceSetupResponseTransfer(gnbTEID uint32, gnbIP string) ngapType.PDUSessionResourceSetupResponseTransfer {
	transfer := ngapType.PDUSessionResourceSetupResponseTransfer{}

	// DL GTP Tunnel (gNB side)
	transfer.DLQosFlowPerTNLInformation.UPTransportLayerInformation.Present = ngapType.UPTransportLayerInformationPresentGTPTunnel
	transfer.DLQosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel = new(ngapType.GTPTunnel)

	// Set gNB IP address
	ipBytes := net.ParseIP(gnbIP).To4()
	transfer.DLQosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel.TransportLayerAddress.Value = aper.BitString{
		Bytes:     ipBytes,
		BitLength: 32,
	}

	// Set gNB TEID
	teidBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(teidBytes, gnbTEID)
	transfer.DLQosFlowPerTNLInformation.UPTransportLayerInformation.GTPTunnel.GTPTEID.Value = teidBytes

	// Associated QoS Flow List (QFI=1)
	transfer.DLQosFlowPerTNLInformation.AssociatedQosFlowList.List = make([]ngapType.AssociatedQosFlowItem, 1)
	transfer.DLQosFlowPerTNLInformation.AssociatedQosFlowList.List[0].QosFlowIdentifier.Value = 1

	return transfer
}
