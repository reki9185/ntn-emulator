package controlplane

import (
	"fmt"
	"log"
	"net"
	"sync"
)

// RANControlPlane handles UE control plane connections
type RANControlPlane struct {
	listenIP   string
	listenPort int
	listener   net.Listener

	// Map of UE connections: IMSI -> net.Conn
	ueConns sync.Map

	// Callback for handling UE NAS messages
	onUEMessage func(conn net.Conn, data []byte) error
}

// NewRANControlPlane creates a new RAN control plane server
func NewRANControlPlane(listenIP string, listenPort int) *RANControlPlane {
	return &RANControlPlane{
		listenIP:   listenIP,
		listenPort: listenPort,
	}
}

// SetMessageHandler sets the callback for handling UE NAS messages
func (rcp *RANControlPlane) SetMessageHandler(handler func(conn net.Conn, data []byte) error) {
	rcp.onUEMessage = handler
}

// Start starts the RAN control plane TCP server
func (rcp *RANControlPlane) Start() error {
	addr := fmt.Sprintf("%s:%d", rcp.listenIP, rcp.listenPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to start control plane listener on %s: %w", addr, err)
	}

	rcp.listener = listener
	log.Printf("✓ RAN Control Plane listening on %s\n", addr)

	// Accept connections in goroutine
	go rcp.acceptConnections()

	return nil
}

// acceptConnections accepts incoming UE connections
func (rcp *RANControlPlane) acceptConnections() {
	for {
		conn, err := rcp.listener.Accept()
		if err != nil {
			// Check if listener was closed
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			log.Printf("Error accepting UE connection: %v\n", err)
			continue
		}

		log.Printf("✓ New UE control plane connection from: %s\n", conn.RemoteAddr())

		// Handle each UE connection in a goroutine
		go rcp.handleUEConnection(conn)
	}
}

// handleUEConnection handles a single UE control plane connection
func (rcp *RANControlPlane) handleUEConnection(conn net.Conn) {
	defer conn.Close()

	buffer := make([]byte, 4096)

	for {
		n, err := conn.Read(buffer)
		if err != nil {
			log.Printf("UE connection closed: %v\n", err)
			return
		}

		if n == 0 {
			continue
		}

		// Process the message through the handler
		if rcp.onUEMessage != nil {
			if err := rcp.onUEMessage(conn, buffer[:n]); err != nil {
				log.Printf("Error handling UE message: %v\n", err)
			}
		}
	}
}

// Stop closes the control plane server
func (rcp *RANControlPlane) Stop() error {
	if rcp.listener != nil {
		return rcp.listener.Close()
	}
	return nil
}

// SendToUE sends a NAS message to a specific UE connection
func (rcp *RANControlPlane) SendToUE(conn net.Conn, data []byte) error {
	if conn == nil {
		return fmt.Errorf("UE connection is nil")
	}

	_, err := conn.Write(data)
	return err
}
