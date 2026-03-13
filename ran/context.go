package ran

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// UEHandoverContext holds the minimal UE session info that the target RAN (RAN-2)
// needs when performing an NGAP Path Switch.
// The source RAN (RAN-1) keeps this in memory and serves it over a lightweight
// TCP "Xn" socket; no files are written.
type UEHandoverContext struct {
	IMSI         string `json:"imsi"`
	AmfUeNgapID  int64  `json:"amf_ue_ngap_id"`
	PDUSessionID uint8  `json:"pdu_session_id"`
	UPFN3IP      string `json:"upf_n3_ip"`
	UPFPort      int    `json:"upf_port"`
	UPFTEID      uint32 `json:"upf_teid"` // UL TEID placed in GTP header when RAN sends to UPF
}

// XnServer is a minimal Xn-interface context server.
// The source RAN starts this after PDU Session Establishment so the target RAN
// can retrieve the UE context when performing a path switch.
type XnServer struct {
	addr     string
	mu       sync.RWMutex
	ctx      *UEHandoverContext
	listener net.Listener
	stopCh   chan struct{}
}

// NewXnServer creates an Xn context server that will listen on addr.
func NewXnServer(addr string) *XnServer {
	return &XnServer{addr: addr, stopCh: make(chan struct{})}
}

// SetContext stores the UE context so it can be served to requesting RANs.
func (s *XnServer) SetContext(ctx *UEHandoverContext) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	log.Printf("📋 [Xn] UE context ready: IMSI=%s AmfID=%d PDUSession=%d\n",
		ctx.IMSI, ctx.AmfUeNgapID, ctx.PDUSessionID)
}

// Start begins listening for context requests.
func (s *XnServer) Start() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("xn listen %s: %w", s.addr, err)
	}
	s.listener = l
	log.Printf("📡 [Xn] Context server listening on %s\n", s.addr)
	go s.serve()
	return nil
}

// Stop shuts down the Xn server.
func (s *XnServer) Stop() {
	close(s.stopCh)
	if s.listener != nil {
		s.listener.Close()
	}
}

func (s *XnServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return
			default:
				log.Printf("⚠️  [Xn] Accept error: %v\n", err)
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *XnServer) handleConn(conn net.Conn) {
	defer conn.Close()
	s.mu.RLock()
	ctx := s.ctx
	s.mu.RUnlock()
	if ctx == nil {
		// No context yet — respond with empty object so the caller can retry.
		conn.Write([]byte("{}"))
		return
	}
	data, _ := json.Marshal(ctx)
	conn.Write(data)
}

// FetchContextFromXn connects to the source RAN's Xn server and returns the UE
// context. It retries up to maxRetries times (with 500 ms gaps) so it can be
// called immediately after the satellite switch even if RAN-1 hasn't stored the
// context yet.
func FetchContextFromXn(xnPeerAddr string) (*UEHandoverContext, error) {
	const maxRetries = 10
	for i := 0; i < maxRetries; i++ {
		ctx, err := tryFetchContext(xnPeerAddr)
		if err == nil {
			return ctx, nil
		}
		log.Printf("⚠️  [Xn] Context not ready, retrying in 500ms (%v)\n", err)
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("xn peer %s: context unavailable after %d retries", xnPeerAddr, maxRetries)
}

func tryFetchContext(addr string) (*UEHandoverContext, error) {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to xn peer: %w", err)
	}
	defer conn.Close()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read from xn peer: %w", err)
	}
	var ctx UEHandoverContext
	if err := json.Unmarshal(buf[:n], &ctx); err != nil {
		return nil, fmt.Errorf("unmarshal xn context: %w", err)
	}
	if ctx.IMSI == "" {
		return nil, fmt.Errorf("xn server returned empty context (UE not registered yet?)")
	}
	return &ctx, nil
}
