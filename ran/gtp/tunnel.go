package gtp

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"

	"ntn-emulator/ue/tun"
)

// GTPTunnel represents a GTP-U tunnel
type GTPTunnel struct {
	localTEID  uint32
	remoteTEID uint32
	upfAddr    *net.UDPAddr
	conn       *net.UDPConn
	tunIface   *tun.TUNInterface

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewGTPTunnel creates a new GTP-U tunnel
func NewGTPTunnel(localTEID, remoteTEID uint32, upfIP string, upfPort int, tunIface *tun.TUNInterface) (*GTPTunnel, error) {
	// Resolve UPF address (remote)
	upfAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", upfIP, upfPort))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UPF address: %w", err)
	}

	// Resolve local address (RAN N3 interface)
	// Use DialUDP instead of ListenUDP - this creates a connected UDP socket
	// The system will automatically assign a local port and handle bidirectional communication
	// IMPORTANT: Must use 127.0.0.1 (same as UPF N3 address) for proper GTP-U communication
	localAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local address: %w", err)
	}

	// Create connected UDP connection (like free-ran-ue)
	// This is better than ListenUDP because:
	// 1. System handles return path automatically
	// 2. No need to worry about port conflicts
	// 3. More efficient for point-to-point communication
	conn, err := net.DialUDP("udp", localAddr, upfAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial UPF: %w", err)
	}

	// Print actual local address being used
	fmt.Printf("🔌 GTP-U: Connected to UPF %s from local %s\n", upfAddr.String(), conn.LocalAddr().String())

	// Set read buffer size for better performance
	conn.SetReadBuffer(2 * 1024 * 1024) // 2MB buffer

	ctx, cancel := context.WithCancel(context.Background())

	tunnel := &GTPTunnel{
		localTEID:  localTEID,
		remoteTEID: remoteTEID,
		upfAddr:    upfAddr,
		conn:       conn,
		tunIface:   tunIface,
		ctx:        ctx,
		cancel:     cancel,
	}

	return tunnel, nil
}

// NewGTPTunnelWithN3IP creates a new GTP-U tunnel with specific N3 IP binding
// This is the free-ran-ue pattern: UE binds to a specific local IP for N3
func NewGTPTunnelWithN3IP(localTEID, remoteTEID uint32, upfAddr string, n3IP string, tunIface *tun.TUNInterface) (*GTPTunnel, error) {
	// Parse UPF address (e.g., "10.0.1.1:2152")
	upfUDPAddr, err := net.ResolveUDPAddr("udp", upfAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UPF address: %w", err)
	}

	// Bind to specific N3 IP address with port 2152 (standard GTP-U port)
	// This allows UPF to send downlink packets to this IP:port
	localAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:2152", n3IP))
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local N3 address: %w", err)
	}

	// Use ListenUDP to bind to specific local address, then create connected socket
	// This is critical for free-ran-ue pattern: UE must be reachable at N3 IP
	listenConn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to bind to N3 address %s: %w", localAddr.String(), err)
	}

	// Set buffer sizes
	listenConn.SetReadBuffer(2 * 1024 * 1024)  // 2MB
	listenConn.SetWriteBuffer(2 * 1024 * 1024) // 2MB

	fmt.Printf("🔌 GTP-U: Bound to N3 %s, remote UPF %s\n", localAddr.String(), upfUDPAddr.String())

	ctx, cancel := context.WithCancel(context.Background())

	tunnel := &GTPTunnel{
		localTEID:  localTEID,
		remoteTEID: remoteTEID,
		upfAddr:    upfUDPAddr,
		conn:       listenConn,
		tunIface:   tunIface,
		ctx:        ctx,
		cancel:     cancel,
	}

	return tunnel, nil
}

// Start starts the GTP-U tunnel forwarding
func (g *GTPTunnel) Start() {
	// Uplink: TUN → GTP → UPF
	g.wg.Add(1)
	go g.handleUplink()

	// Downlink: UPF → GTP → TUN
	g.wg.Add(1)
	go g.handleDownlink()

	fmt.Printf("✓ GTP-U tunnel started (Local TEID: 0x%08x, Remote TEID: 0x%08x, UPF: %s)\n",
		g.localTEID, g.remoteTEID, g.upfAddr.String())
}

// Stop stops the GTP-U tunnel
func (g *GTPTunnel) Stop() {
	g.cancel()
	g.conn.Close()
	g.wg.Wait()
	fmt.Println("✓ GTP-U tunnel stopped")
}

// handleUplink reads IP packets from TUN and sends as GTP-U to UPF
// This is the uplink path: Application → TUN → GTP-U → UPF
func (g *GTPTunnel) handleUplink() {
	defer g.wg.Done()

	// Channel for receiving packets from TUN reader goroutine
	packetChan := make(chan []byte, 10)

	fmt.Printf("⬆️  Uplink handler started - reading from TUN interface\n")

	// Goroutine for blocking read from TUN (free-ran-ue pattern)
	go func() {
		buffer := make([]byte, 2048)
		for {
			n, err := g.tunIface.Read(buffer)
			if err != nil {
				// TUN closed or error - exit goroutine
				fmt.Printf("⬆️  Uplink: TUN read stopped: %v\n", err)
				close(packetChan)
				return
			}

			// Copy packet data (buffer will be reused)
			packet := make([]byte, n)
			copy(packet, buffer[:n])

			select {
			case packetChan <- packet:
			case <-g.ctx.Done():
				close(packetChan)
				return
			}
		}
	}()

	// Process packets from channel
	for {
		select {
		case <-g.ctx.Done():
			fmt.Println("⬆️  Uplink handler stopped")
			return
		case packet, ok := <-packetChan:
			if !ok {
				// Channel closed, TUN read stopped
				return
			}

			fmt.Printf("⬆️  Uplink: Read %d bytes IP packet from TUN\n", len(packet))

			// Encapsulate IP packet in GTP-U
			gtpPacket := g.encapsulateGTP(packet)

			// Send to UPF (use WriteTo for unconnected socket, Write for connected)
			var bytesSent int
			var err error
			if g.upfAddr != nil {
				// Use WriteTo for ListenUDP socket (specifies remote address)
				bytesSent, err = g.conn.WriteToUDP(gtpPacket, g.upfAddr)
			} else {
				// Use Write for DialUDP socket (connected, remote already set)
				bytesSent, err = g.conn.Write(gtpPacket)
			}

			if err != nil {
				fmt.Printf("⚠️  Uplink: Failed to send GTP packet to UPF: %v\n", err)
				continue
			}

			fmt.Printf("✅ Uplink: Sent %d bytes GTP-U packet to UPF %s\n", bytesSent, g.upfAddr.String())
		}
	}
}

// handleDownlink receives GTP-U packets from UPF and writes IP packets to TUN
// This is the critical downlink path: UPF → GTP-U → TUN → Application
func (g *GTPTunnel) handleDownlink() {
	defer g.wg.Done()

	buffer := make([]byte, 2048)

	fmt.Printf("⬇️  Downlink handler started - listening for GTP-U packets on %s\n", g.conn.LocalAddr().String())

	for {
		select {
		case <-g.ctx.Done():
			fmt.Println("⬇️  Downlink handler stopped")
			return
		default:
			// Set read deadline to allow periodic context check
			// This prevents blocking forever if no packets arrive
			g.conn.SetReadDeadline(time.Now().Add(1 * time.Second))

			// Read from connected UDP socket (no need to know remote address)
			n, err := g.conn.Read(buffer)
			if err != nil {
				// Check if it's a timeout (expected)
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout is normal, just continue to check context
					continue
				}
				// Check if we're shutting down
				if g.ctx.Err() != nil {
					return
				}
				fmt.Printf("⚠️  Downlink: UDP read error: %v\n", err)
				continue
			}

			fmt.Printf("⬇️  Downlink: Received %d bytes GTP-U packet\n", n)

			// Decapsulate GTP-U packet to extract IP packet
			ipPacket, err := g.decapsulateGTP(buffer[:n])
			if err != nil {
				fmt.Printf("⚠️  Downlink: Failed to decapsulate GTP packet: %v\n", err)
				continue
			}

			fmt.Printf("⬇️  Downlink: Extracted %d bytes IP packet from GTP-U\n", len(ipPacket))

			srcIP := net.IP(ipPacket[12:16])
			dstIP := net.IP(ipPacket[16:20])
			protocol := ipPacket[9]

			fmt.Printf("IP src=%s dst=%s protocol=%d\n",
				srcIP.String(),
				dstIP.String(),
				protocol,
			)

			// Write IP packet to TUN interface
			bytesWritten, err := g.tunIface.Write(ipPacket)
			if err != nil {
				fmt.Printf("⚠️  Downlink: TUN write error: %v\n", err)
				continue
			}

			fmt.Printf("✅ Downlink: Wrote %d bytes IP packet to TUN interface\n", bytesWritten)
		}
	}
}

// encapsulateGTP adds GTP-U header to IP packet
// GTP-U header format:
//
//	Byte 0: Version (3 bits) | PT (1 bit) | Reserved (1 bit) | E (1 bit) | S (1 bit) | PN (1 bit)
//	Byte 1: Message Type (0xFF for G-PDU)
//	Bytes 2-3: Length (payload length)
//	Bytes 4-7: TEID
//	Bytes 8-11: Sequence Number, N-PDU Number, Next Extension Header Type (optional)
func (g *GTPTunnel) encapsulateGTP(ipPacket []byte) []byte {
	gtpHeader := make([]byte, 8) // Basic GTP-U header without optional fields

	// GTP-U version 1, PT=1, no extension/sequence/N-PDU
	gtpHeader[0] = 0x30 // 0011 0000

	// Message type: G-PDU
	gtpHeader[1] = 0xFF

	// Length: payload length (IP packet length)
	binary.BigEndian.PutUint16(gtpHeader[2:4], uint16(len(ipPacket)))

	// TEID (remote TEID for uplink)
	binary.BigEndian.PutUint32(gtpHeader[4:8], g.remoteTEID)

	// Combine header and payload
	return append(gtpHeader, ipPacket...)
}

// decapsulateGTP extracts IP packet from GTP-U packet
func (g *GTPTunnel) decapsulateGTP(gtpPacket []byte) ([]byte, error) {
	if len(gtpPacket) < 8 {
		return nil, fmt.Errorf("GTP packet too short: %d bytes", len(gtpPacket))
	}

	// Check GTP version (should be 1)
	version := (gtpPacket[0] >> 5) & 0x07
	if version != 1 {
		return nil, fmt.Errorf("unsupported GTP version: %d", version)
	}

	// Check message type (should be 0xFF for G-PDU)
	msgType := gtpPacket[1]
	if msgType != 0xFF {
		fmt.Printf("⚠️  Warning: GTP message type is 0x%02x (expected 0xFF for G-PDU)\n", msgType)
	}

	// Check for extension/sequence/N-PDU flags
	hasExtension := (gtpPacket[0] & 0x04) != 0
	hasSeqNum := (gtpPacket[0] & 0x02) != 0
	hasNPDU := (gtpPacket[0] & 0x01) != 0

	headerLen := 8
	if hasExtension || hasSeqNum || hasNPDU {
		// headerLen = 12 // Extended header
		return nil, fmt.Errorf("GTP extension header not supported yet")
		// if len(gtpPacket) < 12 {
		// 	return nil, fmt.Errorf("GTP packet too short for extended header: %d bytes", len(gtpPacket))
		// }
		// fmt.Printf("🔍 GTP-U header has extensions (Ext=%v, Seq=%v, NPDU=%v), header length: %d\n",
		// 	hasExtension, hasSeqNum, hasNPDU, headerLen)
	}

	// Extract TEID and verify it matches our local TEID
	teid := binary.BigEndian.Uint32(gtpPacket[4:8])
	if teid != g.localTEID {
		fmt.Printf("⚠️  Warning: Received packet for TEID 0x%08x (expected 0x%08x) - processing anyway\n",
			teid, g.localTEID)
		// Don't reject, just warn - UPF might use different TEID convention
	}

	// Extract payload length from GTP header
	payloadLen := binary.BigEndian.Uint16(gtpPacket[2:4])
	fmt.Printf("🔍 GTP-U: TEID=0x%08x, Payload Length=%d bytes\n", teid, payloadLen)

	// Return IP packet (everything after GTP header)
	ipPacket := gtpPacket[headerLen:]

	// Verify we have actual IP packet data
	if len(ipPacket) < 20 {
		return nil, fmt.Errorf("IP packet too short: %d bytes", len(ipPacket))
	}

	// Log IP packet info (for debugging)
	ipVersion := (ipPacket[0] >> 4) & 0x0F
	fmt.Printf("🔍 Extracted IP packet: version=%d, length=%d bytes\n", ipVersion, len(ipPacket))

	return ipPacket, nil
}

// GetLocalTEID returns the local TEID
func (g *GTPTunnel) GetLocalTEID() uint32 {
	return g.localTEID
}

// GetRemoteTEID returns the remote TEID
func (g *GTPTunnel) GetRemoteTEID() uint32 {
	return g.remoteTEID
}

// FormatTEID formats TEID as hex string
func FormatTEID(teid uint32) string {
	return fmt.Sprintf("%08x", teid)
}

// ParseTEID parses TEID from hex string (big endian bytes)
func ParseTEID(teidBytes []byte) (uint32, error) {
	if len(teidBytes) != 4 {
		return 0, fmt.Errorf("TEID must be 4 bytes, got %d", len(teidBytes))
	}
	teidHex := hex.EncodeToString(teidBytes)
	var teid uint32
	_, err := fmt.Sscanf(teidHex, "%08x", &teid)
	return teid, err
}
