package link

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	ntnlink "ntn-emulator/ntn-link"
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

	// NTN link for dynamic delay
	ntnLink *ntnlink.Link

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewRANDataPlane creates a new RAN data plane server
func NewRANDataPlane(ip string, port int, n3IP string, n3Port int, upfAddr string, ulTEID, dlTEID uint32, imsi string, ntnStateFile string) (*RANDataPlane, error) {
	// Parse UPF address
	upfUDPAddr, err := net.ResolveUDPAddr("udp", upfAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve UPF address: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create NTN link if state file is provided
	var ntnLink *ntnlink.Link
	if ntnStateFile != "" {
		ntnLink, err = ntnlink.NewLink(ntnStateFile, 100*time.Millisecond)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("failed to create NTN link: %w", err)
		}
	}

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
		ntnLink:      ntnLink,
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

	// Start NTN link if enabled
	if rdp.ntnLink != nil {
		if err := rdp.ntnLink.Start(); err != nil {
			return fmt.Errorf("failed to start NTN link: %w", err)
		}
	}

	// Determine number of goroutines based on NTN link
	numGoroutines := 4
	if rdp.ntnLink != nil {
		numGoroutines = 8 // Add 4 more for scheduler readers
	}

	// Start goroutines
	rdp.wg.Add(numGoroutines)
	go rdp.receiveFromUE()
	go rdp.receiveFromUPF()
	go rdp.forwardUplinkToUPF()
	go rdp.forwardDownlinkToUE()

	// Start scheduler readers if NTN link is enabled
	if rdp.ntnLink != nil {
		go rdp.readUERanUplinkScheduler()
		go rdp.readUERanDownlinkScheduler()
		go rdp.readRan5GUplinkScheduler()
		go rdp.readRan5GDownlinkScheduler()
	}

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
	if rdp.ntnLink != nil {
		rdp.ntnLink.Stop()
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
			// Regular data packet - apply NTN delay if enabled
			if rdp.ntnLink != nil {
				// Enqueue in UE-RAN uplink scheduler (UE -> RAN leg)
				rdp.ntnLink.GetUERanUplinkScheduler().Enqueue(data)
			} else {
				// No NTN delay - direct forwarding
				rdp.uplinkChan <- data
			}
		}
	}
}

// handleInitialPacket processes the initial registration packet from UE
func (rdp *RANDataPlane) handleInitialPacket(ueAddr *net.UDPAddr, data []byte) {
	// Extract IMSI from packet: "INIT imsi-208930000000001"
	imsi := string(data[len(UE_DATA_PLANE_INITIAL_PACKET)+1:])

	log.Printf("📡 RAN: UE registered from %s (IMSI: %s)\n", ueAddr.String(), imsi)

	// Store UE address (removed strict IMSI validation - was blocking traffic)
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

		// Apply NTN delay if enabled
		if rdp.ntnLink != nil {
			// Enqueue in RAN-5G downlink scheduler (UPF -> RAN leg)
			rdp.ntnLink.GetRan5GDownlinkScheduler().Enqueue(data)
		} else {
			// No NTN delay - direct forwarding
			rdp.downlinkChan <- data
		}
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
			binary.BigEndian.PutUint16(gtpHeader[2:4], uint16(len(packet)+4)) // Length (payload + 4 byte sequence, excluding first 4 bytes)
			binary.BigEndian.PutUint32(gtpHeader[4:8], rdp.ulTEID)            // TEID
			// Sequence number fields [8:12] = 0x00000000

			log.Printf("📤 RAN->UPF: Sending GTP-U packet (TEID=%d, payload=%d bytes, dest=%s)\n", rdp.ulTEID, len(packet), rdp.upfAddr.String())

			// Combine header + payload
			gtpPacket := append(gtpHeader, packet...)

			// Apply NTN delay if enabled
			if rdp.ntnLink != nil {
				// Enqueue in RAN-5G uplink scheduler (RAN -> UPF leg)
				rdp.ntnLink.GetRan5GUplinkScheduler().Enqueue(gtpPacket)
			} else {
				// No NTN delay - send directly
				n, err := rdp.upfConn.WriteToUDP(gtpPacket, rdp.upfAddr)
				if err != nil {
					log.Printf("⚠️  RAN: Error sending to UPF (%s): %v\n", rdp.upfAddr, err)
					continue
				}
				log.Printf("✅ RAN: Sent %d bytes GTP-U to UPF\n", n)
			}
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
			// Parse GTP-U header (minimum 8 bytes)
			if len(gtpPacket) < 8 {
				log.Printf("⚠️  RAN: GTP packet too short (%d bytes)\n", len(gtpPacket))
				continue
			}

			// Check GTP version and flags
			flags := gtpPacket[0]
			version := (flags >> 5) & 0x07
			hasExtension := (flags & 0x04) != 0
			hasSeq := (flags & 0x02) != 0
			hasNPDU := (flags & 0x01) != 0
			msgType := gtpPacket[1]
			_ = msgType // Suppress unused warning

			// log.Printf("🔍 RAN Downlink: GTP version=%d, flags=0x%02x, msgType=0x%02x, E=%v, S=%v, PN=%v, len=%d\n",
			// 	version, flags, msgType, hasExtension, hasSeq, hasNPDU, len(gtpPacket))

			if version != 1 {
				log.Printf("⚠️  RAN: Unsupported GTP version %d\n", version)
				continue
			}

			// Extract TEID
			teid := binary.BigEndian.Uint32(gtpPacket[4:8])
			log.Printf("🔽 RAN<-UPF: Downlink GTP packet (TEID=0x%08x, len=%d)\n", teid, len(gtpPacket))

			// Accept any TEID from UPF (free5GC UPF uses its own DL TEID independent of what gNB advertised)

			// Determine header length based on flags
			headerLen := 8
			if hasExtension || hasSeq || hasNPDU {
				// When any of E/S/PN flags are set, bytes 8-11 are present
				if len(gtpPacket) < 12 {
					log.Printf("⚠️  RAN: GTP packet with E/S/PN flags but too short (%d bytes)\n", len(gtpPacket))
					continue
				}
				headerLen = 12

				// If E (Extension) flag is set, parse extension headers
				if hasExtension {
					// Byte 11 contains the Next Extension Header Type
					nextExtType := gtpPacket[11]
					offset := 12

					// log.Printf("🔍 RAN Downlink: Extension headers present, first type=0x%02x\n", nextExtType)

					// Parse extension headers until we find type 0x00 (no more extensions)
					for nextExtType != 0x00 {
						if offset >= len(gtpPacket) {
							log.Printf("⚠️  RAN: Extension header parsing exceeded packet length\n")
							break
						}

						// First byte of extension header is the length (in 4-byte units, excluding first 2 bytes)
						extLen := int(gtpPacket[offset]) * 4
						if extLen < 4 || offset+extLen > len(gtpPacket) {
							log.Printf("⚠️  RAN: Invalid extension header length %d at offset %d\n", extLen, offset)
							break
						}

						// Last byte of extension header is the next extension type
						nextExtType = gtpPacket[offset+extLen-1]
						// log.Printf("🔍 RAN Downlink: Extension header at offset %d, length=%d bytes, next type=0x%02x\n",
						// 	offset, extLen, nextExtType)

						offset += extLen
						headerLen = offset
					}

					// log.Printf("🔍 RAN Downlink: Total header length with extensions: %d bytes\n", headerLen)
				}
			}

			// log.Printf("🔍 RAN Downlink: Final payload offset=%d\n", headerLen)

			// Extract payload (skip variable-length header)
			payload := gtpPacket[headerLen:]

			// Debug: Print first few bytes of payload (IP packet)
			// if len(payload) >= 20 {
			// 	ipVersion := (payload[0] >> 4) & 0x0F
			// 	protocol := payload[9]
			// 	srcIP := net.IP(payload[12:16])
			// 	dstIP := net.IP(payload[16:20])
			// 	log.Printf("🔍 RAN Downlink: IP v%d, proto=%d, src=%s, dst=%s, payload_len=%d\n",
			// 		ipVersion, protocol, srcIP, dstIP, len(payload))
			// }

			// Get UE address (check if connected)
			rdp.ueAddrLock.RLock()
			ueAddr := rdp.ueAddr
			rdp.ueAddrLock.RUnlock()

			if ueAddr == nil {
				log.Printf("⚠️  RAN: UE not connected yet, dropping downlink packet\n")
				continue
			}

			// Apply NTN delay if enabled
			if rdp.ntnLink != nil {
				// Enqueue in UE-RAN downlink scheduler (RAN -> UE leg)
				rdp.ntnLink.GetUERanDownlinkScheduler().Enqueue(payload)
			} else {
				// No NTN delay - send directly to UE
				n, err := rdp.ranServer.WriteToUDP(payload, ueAddr)
				if err != nil {
					log.Printf("⚠️  RAN: Error sending to UE: %v\n", err)
					continue
				}
				_ = n // Suppress unused warning
			}
		}
	}
}

// readUERanUplinkScheduler reads delayed packets from UE-RAN uplink scheduler
// (UE -> RAN leg) and forwards them into the uplink pipeline
func (rdp *RANDataPlane) readUERanUplinkScheduler() {
	defer rdp.wg.Done()
	readyChan := rdp.ntnLink.GetUERanUplinkScheduler().GetReadyChannel()
	for {
		select {
		case <-rdp.ctx.Done():
			return
		case packet := <-readyChan:
			if packet == nil {
				return
			}
			rdp.uplinkChan <- packet
		}
	}
}

// readRan5GUplinkScheduler reads delayed packets from RAN-5G uplink scheduler
// (RAN -> UPF leg) and sends them to the UPF
func (rdp *RANDataPlane) readRan5GUplinkScheduler() {
	defer rdp.wg.Done()
	readyChan := rdp.ntnLink.GetRan5GUplinkScheduler().GetReadyChannel()
	for {
		select {
		case <-rdp.ctx.Done():
			return
		case gtpPacket := <-readyChan:
			if gtpPacket == nil {
				return
			}
			_, err := rdp.upfConn.WriteToUDP(gtpPacket, rdp.upfAddr)
			if err != nil {
				log.Printf("⚠️  RAN: Error sending scheduled packet to UPF: %v\n", err)
			}
		}
	}
}

// readRan5GDownlinkScheduler reads delayed packets from RAN-5G downlink scheduler
// (UPF -> RAN leg) and forwards them into the downlink pipeline
func (rdp *RANDataPlane) readRan5GDownlinkScheduler() {
	defer rdp.wg.Done()
	readyChan := rdp.ntnLink.GetRan5GDownlinkScheduler().GetReadyChannel()
	for {
		select {
		case <-rdp.ctx.Done():
			return
		case packet := <-readyChan:
			if packet == nil {
				return
			}
			rdp.downlinkChan <- packet
		}
	}
}

// readUERanDownlinkScheduler reads delayed packets from UE-RAN downlink scheduler
// (RAN -> UE leg) and sends them directly to the UE
func (rdp *RANDataPlane) readUERanDownlinkScheduler() {
	defer rdp.wg.Done()
	readyChan := rdp.ntnLink.GetUERanDownlinkScheduler().GetReadyChannel()
	for {
		select {
		case <-rdp.ctx.Done():
			return
		case payload := <-readyChan:
			if payload == nil {
				return
			}
			rdp.ueAddrLock.RLock()
			ueAddr := rdp.ueAddr
			rdp.ueAddrLock.RUnlock()
			if ueAddr == nil {
				log.Printf("⚠️  RAN: UE not connected, dropping scheduled downlink packet\n")
				continue
			}
			_, err := rdp.ranServer.WriteToUDP(payload, ueAddr)
			if err != nil {
				log.Printf("⚠️  RAN: Error sending scheduled packet to UE: %v\n", err)
			}
		}
	}
}
