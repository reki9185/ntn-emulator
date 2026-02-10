package link

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// GNBDataPlane handles GTP-U tunneling between UEs and UPF
type GNBDataPlane struct {
	// N3 connection to UPF
	n3Conn  *net.UDPConn
	upfAddr *net.UDPAddr

	// Data plane server for UEs
	ueDataPlaneServer *net.UDPConn

	// TEID mapping: downlink TEID -> UE data plane address
	dlTeidToUeAddr sync.Map // map[uint32]*net.UDPAddr

	// TEID mapping: UE address -> uplink TEID
	ueAddrToUlTeid sync.Map // map[string]uint32

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewGNBDataPlane creates a new gNB data plane server
func NewGNBDataPlane(gnbN3Addr string, upfAddr string, ueDataPlaneAddr string) (*GNBDataPlane, error) {
	// Parse UPF address
	upfUDPAddr, err := net.ResolveUDPAddr("udp", upfAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UPF address: %w", err)
	}

	// Create N3 connection to UPF
	// CRITICAL: Must bind to a FIXED local address (IP:port) that UPF can send downlink to
	// Following free-ran-ue pattern: use DialUDP with explicit local address
	// This binds the local socket to gnbN3Addr (e.g., 127.0.0.10:2152)
	gnbLocalAddr, err := net.ResolveUDPAddr("udp", gnbN3Addr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve gNB N3 address: %w", err)
	}

	// DialUDP creates a "connected" UDP socket bound to gnbLocalAddr
	// This allows both sending (uplink) and receiving (downlink) on the same socket
	n3Conn, err := net.DialUDP("udp", gnbLocalAddr, upfUDPAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create N3 connection: %w", err)
	}

	fmt.Printf("🔌 gNB N3: Connected to UPF %s from %s\n", upfAddr, n3Conn.LocalAddr().String())

	// Create data plane server for UEs
	ueServerAddr, err := net.ResolveUDPAddr("udp", ueDataPlaneAddr)
	if err != nil {
		n3Conn.Close()
		return nil, fmt.Errorf("failed to resolve UE data plane address: %w", err)
	}

	ueDataPlaneServer, err := net.ListenUDP("udp", ueServerAddr)
	if err != nil {
		n3Conn.Close()
		return nil, fmt.Errorf("failed to create UE data plane server: %w", err)
	}

	fmt.Printf("🔌 gNB Data Plane: Listening for UEs on %s\n", ueDataPlaneServer.LocalAddr().String())

	ctx, cancel := context.WithCancel(context.Background())

	gnb := &GNBDataPlane{
		n3Conn:            n3Conn,
		upfAddr:           upfUDPAddr,
		ueDataPlaneServer: ueDataPlaneServer,
		ctx:               ctx,
		cancel:            cancel,
	}

	return gnb, nil
}

// RegisterUE registers a UE's data plane connection with TEID mapping
func (g *GNBDataPlane) RegisterUE(ueDataPlaneAddr *net.UDPAddr, uplinkTEID uint32, downlinkTEID uint32) {
	addrKey := ueDataPlaneAddr.String()

	g.ueAddrToUlTeid.Store(addrKey, uplinkTEID)
	g.dlTeidToUeAddr.Store(downlinkTEID, ueDataPlaneAddr)

	fmt.Printf("📝 gNB: Registered UE %s (UL TEID: 0x%08x, DL TEID: 0x%08x)\n",
		addrKey, uplinkTEID, downlinkTEID)
}

// UnregisterUE removes a UE's data plane mapping
func (g *GNBDataPlane) UnregisterUE(ueDataPlaneAddr *net.UDPAddr, downlinkTEID uint32) {
	addrKey := ueDataPlaneAddr.String()
	g.ueAddrToUlTeid.Delete(addrKey)
	g.dlTeidToUeAddr.Delete(downlinkTEID)
	fmt.Printf("📝 gNB: Unregistered UE %s (DL TEID: 0x%08x)\n", addrKey, downlinkTEID)
}

// Start starts the gNB data plane forwarding
func (g *GNBDataPlane) Start() {
	// Uplink: UE → gNB → GTP → UPF
	g.wg.Add(1)
	go g.handleUplinkFromUEs()

	// Downlink: UPF → GTP → gNB → UE
	g.wg.Add(1)
	go g.handleDownlinkFromUPF()

	fmt.Println("✅ gNB Data Plane started")
}

// handleUplinkFromUEs receives IP packets from UEs and forwards to UPF via GTP-U
func (g *GNBDataPlane) handleUplinkFromUEs() {
	defer g.wg.Done()

	buffer := make([]byte, 4096)

	for {
		select {
		case <-g.ctx.Done():
			fmt.Println("⬆️  gNB Uplink handler stopped")
			return
		default:
		}

		// Read from UE data plane server
		n, ueAddr, err := g.ueDataPlaneServer.ReadFromUDP(buffer)
		if err != nil {
			fmt.Printf("❌ gNB Uplink: Error reading from UE: %v\n", err)
			continue
		}

		// Get uplink TEID for this UE
		addrKey := ueAddr.String()
		teidVal, ok := g.ueAddrToUlTeid.Load(addrKey)
		if !ok {
			fmt.Printf("⚠️  gNB Uplink: No TEID mapping for UE %s, dropping packet\n", addrKey)
			continue
		}

		uplinkTEID := teidVal.(uint32)

		// Encapsulate in GTP-U
		ipPacket := make([]byte, n)
		copy(ipPacket, buffer[:n])

		gtpPacket := g.encapsulateGTP(uplinkTEID, ipPacket)

		// Send to UPF
		_, err = g.n3Conn.Write(gtpPacket)
		if err != nil {
			fmt.Printf("❌ gNB Uplink: Error sending to UPF: %v\n", err)
			continue
		}

		fmt.Printf("⬆️  gNB Uplink: Forwarded %d bytes from UE %s (TEID: 0x%08x) to UPF\n",
			n, addrKey, uplinkTEID)
	}
}

// handleDownlinkFromUPF receives GTP-U packets from UPF and forwards to UEs
func (g *GNBDataPlane) handleDownlinkFromUPF() {
	defer g.wg.Done()

	buffer := make([]byte, 4096)

	for {
		select {
		case <-g.ctx.Done():
			fmt.Println("⬇️  gNB Downlink handler stopped")
			return
		default:
		}

		// Read GTP-U packet from UPF
		n, err := g.n3Conn.Read(buffer)
		if err != nil {
			fmt.Printf("❌ gNB Downlink: Error reading from UPF: %v\n", err)
			continue
		}

		gtpPacket := make([]byte, n)
		copy(gtpPacket, buffer[:n])

		// Parse GTP-U header and extract TEID
		teid, ipPacket, err := g.parseGTP(gtpPacket)
		if err != nil {
			fmt.Printf("❌ gNB Downlink: Error parsing GTP: %v\n", err)
			continue
		}

		// Find UE address for this TEID
		ueAddrVal, ok := g.dlTeidToUeAddr.Load(teid)
		if !ok {
			fmt.Printf("⚠️  gNB Downlink: No UE mapping for TEID 0x%08x, dropping packet\n", teid)
			continue
		}

		ueAddr := ueAddrVal.(*net.UDPAddr)

		// Forward to UE
		_, err = g.ueDataPlaneServer.WriteToUDP(ipPacket, ueAddr)
		if err != nil {
			fmt.Printf("❌ gNB Downlink: Error sending to UE: %v\n", err)
			continue
		}

		fmt.Printf("⬇️  gNB Downlink: Forwarded %d bytes to UE %s (TEID: 0x%08x)\n",
			len(ipPacket), ueAddr.String(), teid)
	}
}

// encapsulateGTP wraps IP packet in GTP-U header
func (g *GNBDataPlane) encapsulateGTP(teid uint32, ipPacket []byte) []byte {
	// GTP-U header: 8 bytes (basic) or 12 bytes (with optional fields)
	// Using basic 8-byte header
	gtpHeader := make([]byte, 8)

	// Flags: version=1, PT=1, E=0, S=0, PN=0
	gtpHeader[0] = 0x30

	// Message Type: 255 (G-PDU)
	gtpHeader[1] = 0xFF

	// Length: payload length (excluding GTP header)
	binary.BigEndian.PutUint16(gtpHeader[2:4], uint16(len(ipPacket)))

	// TEID
	binary.BigEndian.PutUint32(gtpHeader[4:8], teid)

	// Concatenate header + payload
	return append(gtpHeader, ipPacket...)
}

// parseGTP extracts TEID and IP packet from GTP-U packet
func (g *GNBDataPlane) parseGTP(gtpPacket []byte) (uint32, []byte, error) {
	if len(gtpPacket) < 8 {
		return 0, nil, fmt.Errorf("GTP packet too short: %d bytes", len(gtpPacket))
	}

	// Extract TEID (bytes 4-7)
	teid := binary.BigEndian.Uint32(gtpPacket[4:8])

	// Check for optional fields (E, S, PN flags)
	flags := gtpPacket[0]
	headerLen := 8

	// If any of E, S, or PN flags are set, header is 12 bytes
	if (flags & 0x07) != 0 {
		headerLen = 12
	}

	if len(gtpPacket) < headerLen {
		return 0, nil, fmt.Errorf("GTP packet too short for header: %d bytes", len(gtpPacket))
	}

	// IP packet starts after GTP header
	ipPacket := gtpPacket[headerLen:]

	return teid, ipPacket, nil
}

// Stop stops the gNB data plane
func (g *GNBDataPlane) Stop() {
	g.cancel()
	g.n3Conn.Close()
	g.ueDataPlaneServer.Close()
	g.wg.Wait()
	fmt.Println("✅ gNB Data Plane stopped")
}
