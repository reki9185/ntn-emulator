package ntnemulator

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
)

const (
	UE_DATA_PLANE_INITIAL_PACKET = "INIT"
	UE_IMSI_PREFIX               = "imsi-"
)

// RANDataPlane implements the RAN data plane server (like free-ran-ue)
// Architecture: UE <--UDP raw IP--> RAN <--GTP-U--> UPF
type RANDataPlane struct {
	ip           string // RAN's data plane IP for UE connections
	port         int    // RAN's data plane port for UE connections
	n3IP         string // RAN's N3 IP for GTP-U
	n3Port       int    // RAN's N3 port for GTP-U (2152)
	upfAddr      *net.UDPAddr
	ulTEID       uint32 // Uplink TEID (RAN->UPF)
	dlTEID       uint32 // Downlink TEID (UPF->RAN)
	expectedIMSI string

	// Network connections
	ranServer *net.UDPConn // Receives from UE
	upfConn   *net.UDPConn // Sends to/receives from UPF (GTP-U)

	// UE tracking
	ueAddr     *net.UDPAddr
	ueAddrLock sync.RWMutex

	// Channels for packet forwarding
	uplinkChan   chan []byte
	downlinkChan chan []byte

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRANDataPlane creates a new RAN data plane server
func NewRANDataPlane(ip string, port int, n3IP string, n3Port int, upfAddr string, ulTEID, dlTEID uint32, imsi string) (*RANDataPlane, error) {
	// Parse UPF address
	upfUDPAddr, err := net.ResolveUDPAddr("udp", upfAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UPF address: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	rdp := &RANDataPlane{
		ip:           ip,
		port:         port,
		n3IP:         n3IP,
		n3Port:       n3Port,
		upfAddr:      upfUDPAddr,
		ulTEID:       ulTEID,
		dlTEID:       dlTEID,
		expectedIMSI: imsi,
		uplinkChan:   make(chan []byte, 100),
		downlinkChan: make(chan []byte, 100),
		ctx:          ctx,
		cancel:       cancel,
	}

	return rdp, nil
}

// Start starts the RAN data plane server
func (rdp *RANDataPlane) Start() error {
	// Create UDP server for UE connections (bind to specific IP and port)
	serverAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", rdp.ip, rdp.port))
	if err != nil {
		return fmt.Errorf("failed to resolve server address: %w", err)
	}

	rdp.ranServer, err = net.ListenUDP("udp", serverAddr)
	if err != nil {
		return fmt.Errorf("failed to start RAN data plane server: %w", err)
	}

	rdp.ranServer.SetReadBuffer(2 * 1024 * 1024)  // 2MB
	rdp.ranServer.SetWriteBuffer(2 * 1024 * 1024) // 2MB

	// Bind GTP-U socket to RAN's N3 address (where UPF will send downlink)
	// This is critical - UPF needs to send downlink GTP-U packets to this address
	n3Addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", rdp.n3IP, rdp.n3Port))
	if err != nil {
		return fmt.Errorf("failed to resolve N3 address: %w", err)
	}

	rdp.upfConn, err = net.ListenUDP("udp", n3Addr)
	if err != nil {
		return fmt.Errorf("failed to bind to N3 address %s: %w", n3Addr.String(), err)
	}

	rdp.upfConn.SetReadBuffer(2 * 1024 * 1024)  // 2MB
	rdp.upfConn.SetWriteBuffer(2 * 1024 * 1024) // 2MB

	log.Printf("🔌 RAN Data Plane: Listening on %s:%d (for UE connections)\n", rdp.ip, rdp.port)
	log.Printf("🔌 RAN Data Plane: N3 GTP-U listening on %s (for UPF)\n", n3Addr.String())
	log.Printf("🔌 RAN Data Plane: Will send uplink to UPF %s\n", rdp.upfAddr.String())

	// Start goroutines
	rdp.wg.Add(4)
	go rdp.receiveFromUE()
	go rdp.receiveFromUPF()
	go rdp.forwardUplinkToUPF()
	go rdp.forwardDownlinkToUE()

	return nil
}

// Stop stops the RAN data plane server
func (rdp *RANDataPlane) Stop() {
	rdp.cancel()
	if rdp.ranServer != nil {
		rdp.ranServer.Close()
	}
	if rdp.upfConn != nil {
		rdp.upfConn.Close()
	}
	rdp.wg.Wait()
}

// GetN3Port returns the actual N3 port being used
func (rdp *RANDataPlane) GetN3Port() int {
	if rdp.upfConn != nil {
		return rdp.upfConn.LocalAddr().(*net.UDPAddr).Port
	}
	return rdp.n3Port
}

// receiveFromUE receives packets from UE
func (rdp *RANDataPlane) receiveFromUE() {
	defer rdp.wg.Done()

	buffer := make([]byte, 4096)
	for {
		select {
		case <-rdp.ctx.Done():
			return
		default:
		}

		n, ueAddr, err := rdp.ranServer.ReadFromUDP(buffer)
		if err != nil {
			if rdp.ctx.Err() != nil {
				return
			}
			log.Printf("⚠️  RAN: Error reading from UE: %v\n", err)
			continue
		}

		// Make a copy of the data
		data := make([]byte, n)
		copy(data, buffer[:n])

		// Check if this is an initial packet
		if n > len(UE_DATA_PLANE_INITIAL_PACKET) && string(data[:len(UE_DATA_PLANE_INITIAL_PACKET)]) == UE_DATA_PLANE_INITIAL_PACKET {
			rdp.handleInitialPacket(ueAddr, data)
		} else {
			// Regular data packet - queue for GTP encapsulation
			log.Printf("📥 RAN: Received %d bytes from UE %s\n", n, ueAddr.String())
			rdp.uplinkChan <- data
		}
	}
}

// handleInitialPacket processes the initial registration packet from UE
func (rdp *RANDataPlane) handleInitialPacket(ueAddr *net.UDPAddr, data []byte) {
	// Extract IMSI from packet: "INIT imsi-208930000000001"
	imsi := string(data[len(UE_DATA_PLANE_INITIAL_PACKET)+1:])

	log.Printf("📡 RAN: UE registered from %s (IMSI: %s)\n", ueAddr.String(), imsi)

	// Validate IMSI
	if imsi != UE_IMSI_PREFIX+rdp.expectedIMSI[5:] { // Skip "imsi-" prefix from expectedIMSI
		log.Printf("⚠️  RAN: Unexpected IMSI %s (expected %s)\n", imsi, rdp.expectedIMSI)
		return
	}

	// Store UE address
	rdp.ueAddrLock.Lock()
	rdp.ueAddr = ueAddr
	rdp.ueAddrLock.Unlock()

	log.Printf("✅ RAN: UE data plane connection established (%s)\n", ueAddr.String())
}

// receiveFromUPF receives GTP-U packets from UPF
func (rdp *RANDataPlane) receiveFromUPF() {
	defer rdp.wg.Done()

	buffer := make([]byte, 4096)
	for {
		select {
		case <-rdp.ctx.Done():
			return
		default:
		}

		n, err := rdp.upfConn.Read(buffer)
		if err != nil {
			if rdp.ctx.Err() != nil {
				return
			}
			log.Printf("⚠️  RAN: Error reading from UPF: %v\n", err)
			continue
		}

		// Make a copy of the data
		data := make([]byte, n)
		copy(data, buffer[:n])

		log.Printf("📥 RAN: Received %d bytes GTP-U from UPF\n", n)

		// Queue for downlink forwarding
		rdp.downlinkChan <- data
	}
}

// forwardUplinkToUPF encapsulates packets in GTP-U and sends to UPF
func (rdp *RANDataPlane) forwardUplinkToUPF() {
	defer rdp.wg.Done()

	for {
		select {
		case <-rdp.ctx.Done():
			return
		case packet := <-rdp.uplinkChan:
			// Create GTP-U header (like free-ran-ue)
			gtpHeader := make([]byte, 12)
			gtpHeader[0] = 0x32                                               // Version=1, PT=1, E=0, S=1, PN=0
			gtpHeader[1] = 0xff                                               // Message type: G-PDU
			binary.BigEndian.PutUint16(gtpHeader[2:4], uint16(len(packet)+8)) // Length (excluding first 4 bytes)
			binary.BigEndian.PutUint32(gtpHeader[4:8], rdp.ulTEID)            // TEID
			// Sequence number fields [8:12] = 0x00000000

			// Combine header + payload
			gtpPacket := append(gtpHeader, packet...)

			// Send to UPF (use WriteToUDP since we're using ListenUDP)
			n, err := rdp.upfConn.WriteToUDP(gtpPacket, rdp.upfAddr)
			if err != nil {
				log.Printf("⚠️  RAN: Error sending to UPF: %v\n", err)
				continue
			}

			log.Printf("📤 RAN: Sent %d bytes GTP-U to UPF (TEID: 0x%08x, payload: %d bytes)\n",
				n, rdp.ulTEID, len(packet))
		}
	}
}

// forwardDownlinkToUE decapsulates GTP-U and sends raw IP to UE
func (rdp *RANDataPlane) forwardDownlinkToUE() {
	defer rdp.wg.Done()

	for {
		select {
		case <-rdp.ctx.Done():
			return
		case gtpPacket := <-rdp.downlinkChan:
			// Parse GTP-U header
			if len(gtpPacket) < 12 {
				log.Printf("⚠️  RAN: GTP packet too short (%d bytes)\n", len(gtpPacket))
				continue
			}

			// Extract TEID
			teid := binary.BigEndian.Uint32(gtpPacket[4:8])
			if teid != rdp.dlTEID {
				log.Printf("⚠️  RAN: Unexpected TEID 0x%08x (expected 0x%08x)\n", teid, rdp.dlTEID)
				continue
			}

			// Extract payload (skip 12-byte header)
			payload := gtpPacket[12:]

			// Get UE address
			rdp.ueAddrLock.RLock()
			ueAddr := rdp.ueAddr
			rdp.ueAddrLock.RUnlock()

			if ueAddr == nil {
				log.Printf("⚠️  RAN: UE not connected yet, dropping downlink packet\n")
				continue
			}

			// Send raw IP packet to UE
			n, err := rdp.ranServer.WriteToUDP(payload, ueAddr)
			if err != nil {
				log.Printf("⚠️  RAN: Error sending to UE: %v\n", err)
				continue
			}

			log.Printf("📤 RAN: Sent %d bytes to UE %s\n", n, ueAddr.String())
		}
	}
}
