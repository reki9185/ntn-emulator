package common

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
)

// WriteMessage writes a length-prefixed message to the connection
// Format: [2-byte length (big-endian)][message bytes]
func WriteMessage(conn net.Conn, message []byte) error {
	// Prepare length prefix (2 bytes, big-endian)
	length := uint16(len(message))
	lengthBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(lengthBytes, length)

	// Write length prefix
	if _, err := conn.Write(lengthBytes); err != nil {
		return fmt.Errorf("failed to write length prefix: %w", err)
	}

	// Write message body
	if _, err := conn.Write(message); err != nil {
		return fmt.Errorf("failed to write message body: %w", err)
	}

	return nil
}

// ReadMessage reads a length-prefixed message from the connection
// Format: [2-byte length (big-endian)][message bytes]
func ReadMessage(conn net.Conn) ([]byte, error) {
	// Read length prefix (2 bytes)
	lengthBytes := make([]byte, 2)
	if _, err := io.ReadFull(conn, lengthBytes); err != nil {
		return nil, fmt.Errorf("failed to read length prefix: %w", err)
	}

	// Parse length
	length := binary.BigEndian.Uint16(lengthBytes)
	if length == 0 {
		return []byte{}, nil
	}

	// Read exact message bytes
	message := make([]byte, length)
	if _, err := io.ReadFull(conn, message); err != nil {
		return nil, fmt.Errorf("failed to read message body: %w", err)
	}

	return message, nil
}
