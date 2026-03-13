package uelink

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"ntn-emulator/ue/tun"
)

const (
	UE_DATA_PLANE_INITIAL_PACKET = "INIT"
	UE_IMSI_PREFIX               = "imsi-"
)

// UEDataPlane handles UE side data plane communication (like free-ran-ue)
// Architecture: TUN <--> UE <--UDP raw IP--> RAN
type UEDataPlane struct {
	ranAddr  *net.UDPAddr
	imsi     string
	tunIface *tun.TUNInterface

	// Network connection — protected by ranConnMu so Reconnect can swap it safely.
	ranConn   *net.UDPConn
	ranConnMu sync.RWMutex

	// Channels for packet forwarding
	fromTunChan chan []byte
	fromRanChan chan []byte

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewUEDataPlane creates a new UE data plane handler
func NewUEDataPlane(ranAddr string, imsi string, tunIface *tun.TUNInterface) (*UEDataPlane, error) {
	// Parse RAN address
	ranUDPAddr, err := net.ResolveUDPAddr("udp", ranAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve RAN address: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	udp := &UEDataPlane{
		ranAddr:     ranUDPAddr,
		imsi:        imsi,
		tunIface:    tunIface,
		fromTunChan: make(chan []byte, 100),
		fromRanChan: make(chan []byte, 100),
		ctx:         ctx,
		cancel:      cancel,
	}

	return udp, nil
}

// Start starts the UE data plane
func (udp *UEDataPlane) Start() error {
	// Connect to RAN data plane server
	// Use 0.0.0.0 to bind to any interface (allows connection across namespaces)
	localAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	if err != nil {
		return fmt.Errorf("failed to resolve local address: %w", err)
	}

	udp.ranConn, err = net.DialUDP("udp", localAddr, udp.ranAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to RAN: %w", err)
	}

	udp.ranConn.SetReadBuffer(2 * 1024 * 1024)  // 2MB
	udp.ranConn.SetWriteBuffer(2 * 1024 * 1024) // 2MB

	log.Printf("🔌 UE: Connected to RAN %s\n", udp.ranAddr.String())

	// Send initial packet to RAN (like free-ran-ue)
	initialPacket := fmt.Sprintf("%s imsi-%s", UE_DATA_PLANE_INITIAL_PACKET, udp.imsi)
	n, err := udp.ranConn.Write([]byte(initialPacket))
	if err != nil {
		return fmt.Errorf("failed to send initial packet: %w", err)
	}
	log.Printf("📤 UE: Sent initial packet to RAN (%d bytes)\n", n)

	// Start goroutines
	udp.wg.Add(4)
	go udp.readFromTun()
	go udp.readFromRan()
	go udp.forwardToRan()
	go udp.forwardToTun()

	log.Println("✅ UE Data Plane started successfully")

	return nil
}

// Stop stops the UE data plane
func (udp *UEDataPlane) Stop() {
	udp.cancel()
	udp.ranConnMu.RLock()
	conn := udp.ranConn
	udp.ranConnMu.RUnlock()
	if conn != nil {
		conn.Close()
	}
	udp.wg.Wait()
}

// Reconnect switches the UE data plane to a new RAN address (e.g. RAN-2 after path switch).
// It opens a new UDP connection, sends an INIT packet so RAN-2 registers the UE address,
// then closes the old connection. In-flight packets from the old connection are discarded.
func (udp *UEDataPlane) Reconnect(newRanAddr string) error {
	ranUDPAddr, err := net.ResolveUDPAddr("udp", newRanAddr)
	if err != nil {
		return fmt.Errorf("resolve new RAN address: %w", err)
	}

	localAddr, _ := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	newConn, err := net.DialUDP("udp", localAddr, ranUDPAddr)
	if err != nil {
		return fmt.Errorf("dial new RAN: %w", err)
	}
	newConn.SetReadBuffer(2 * 1024 * 1024)
	newConn.SetWriteBuffer(2 * 1024 * 1024)

	// Send INIT to RAN-2 so it learns the UE's UDP address.
	initialPacket := fmt.Sprintf("%s imsi-%s", UE_DATA_PLANE_INITIAL_PACKET, udp.imsi)
	if _, err := newConn.Write([]byte(initialPacket)); err != nil {
		newConn.Close()
		return fmt.Errorf("send INIT to new RAN: %w", err)
	}

	// Atomically swap the connection.
	udp.ranConnMu.Lock()
	oldConn := udp.ranConn
	udp.ranConn = newConn
	udp.ranAddr = ranUDPAddr
	udp.ranConnMu.Unlock()

	// Closing old conn unblocks readFromRan which is blocked on Read().
	if oldConn != nil {
		oldConn.Close()
	}

	log.Printf("🔄 UE: Data plane reconnected to RAN at %s\n", newRanAddr)
	return nil
}

// readFromTun reads packets from TUN interface
func (udp *UEDataPlane) readFromTun() {
	defer udp.wg.Done()

	buffer := make([]byte, 4096)
	for {
		select {
		case <-udp.ctx.Done():
			return
		default:
		}

		n, err := udp.tunIface.Read(buffer)
		if err != nil {
			if udp.ctx.Err() != nil {
				return
			}
			log.Printf("⚠️  UE: Error reading from TUN: %v\n", err)
			continue
		}

		// Make a copy of the data
		data := make([]byte, n)
		copy(data, buffer[:n])

		// log.Printf("📥 UE: Read %d bytes from TUN\n", n)

		// Queue for RAN transmission
		udp.fromTunChan <- data
	}
}

// readFromRan reads packets from RAN
func (udp *UEDataPlane) readFromRan() {
	defer udp.wg.Done()

	buffer := make([]byte, 4096)
	for {
		select {
		case <-udp.ctx.Done():
			return
		default:
		}

		udp.ranConnMu.RLock()
		conn := udp.ranConn
		udp.ranConnMu.RUnlock()

		n, err := conn.Read(buffer)
		if err != nil {
			if udp.ctx.Err() != nil {
				return // normal shutdown
			}
			// Connection was closed by Reconnect — loop to pick up the new one.
			continue
		}

		// Make a copy of the data
		data := make([]byte, n)
		copy(data, buffer[:n])

		// Queue for TUN write
		udp.fromRanChan <- data
	}
}

// forwardToRan forwards packets from TUN to RAN
func (udp *UEDataPlane) forwardToRan() {
	defer udp.wg.Done()

	for {
		select {
		case <-udp.ctx.Done():
			return
		case packet := <-udp.fromTunChan:
			// Send raw IP packet to RAN (no GTP encapsulation at UE side)
			udp.ranConnMu.RLock()
			conn := udp.ranConn
			udp.ranConnMu.RUnlock()
			_, err := conn.Write(packet)
			if err != nil && udp.ctx.Err() == nil {
				log.Printf("⚠️  UE: Error sending to RAN: %v\n", err)
			}
		}
	}
}

// forwardToTun forwards packets from RAN to TUN
func (udp *UEDataPlane) forwardToTun() {
	defer udp.wg.Done()

	for {
		select {
		case <-udp.ctx.Done():
			return
		case packet := <-udp.fromRanChan:
			// Write raw IP packet to TUN (RAN already removed GTP header)
			n, err := udp.tunIface.Write(packet)
			if err != nil {
				log.Printf("⚠️  UE: Error writing to TUN: %v\n", err)
				continue
			}
			_ = n // Suppress unused warning

			// log.Printf("📤 UE: Wrote %d bytes to TUN\n", n)
		}
	}
}
