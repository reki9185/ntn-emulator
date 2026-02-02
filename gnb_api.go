package ntnemulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// UERegistrationRequest is the request to register UE with gNB
type UERegistrationRequest struct {
	UEAddr       string `json:"ue_addr"`       // UE data plane UDP address
	UplinkTEID   uint32 `json:"uplink_teid"`   // Uplink TEID (from UE to UPF)
	DownlinkTEID uint32 `json:"downlink_teid"` // Downlink TEID (from UPF to UE)
}

// GNBRegistrationAPI provides HTTP API for UE registration
type GNBRegistrationAPI struct {
	gnb    *GNBDataPlane
	server *http.Server
}

// NewGNBRegistrationAPI creates a new registration API server
func NewGNBRegistrationAPI(gnb *GNBDataPlane, listenAddr string) *GNBRegistrationAPI {
	api := &GNBRegistrationAPI{
		gnb: gnb,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/register", api.handleRegister)
	mux.HandleFunc("/unregister", api.handleUnregister)

	api.server = &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	return api
}

// Start starts the API server
func (a *GNBRegistrationAPI) Start() error {
	go func() {
		fmt.Printf("🌐 gNB Registration API listening on %s\n", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("❌ API server error: %v\n", err)
		}
	}()
	return nil
}

// Stop stops the API server
func (a *GNBRegistrationAPI) Stop() error {
	return a.server.Close()
}

// handleRegister handles UE registration requests
func (a *GNBRegistrationAPI) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UERegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Parse UE address
	ueAddr, err := net.ResolveUDPAddr("udp", req.UEAddr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid UE address: %v", err), http.StatusBadRequest)
		return
	}

	// Register UE
	a.gnb.RegisterUE(ueAddr, req.UplinkTEID, req.DownlinkTEID)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "registered"})
}

// handleUnregister handles UE unregistration requests
func (a *GNBRegistrationAPI) handleUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UERegistrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Parse UE address
	ueAddr, err := net.ResolveUDPAddr("udp", req.UEAddr)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid UE address: %v", err), http.StatusBadRequest)
		return
	}

	// Unregister UE
	a.gnb.UnregisterUE(ueAddr, req.DownlinkTEID)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "unregistered"})
}

// RegisterUEWithGNB registers UE with gNB via HTTP API
func RegisterUEWithGNB(gnbAPIAddr string, ueAddr string, uplinkTEID, downlinkTEID uint32) error {
	req := UERegistrationRequest{
		UEAddr:       ueAddr,
		UplinkTEID:   uplinkTEID,
		DownlinkTEID: downlinkTEID,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := http.Post(
		fmt.Sprintf("http://%s/register", gnbAPIAddr),
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return fmt.Errorf("failed to send registration request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration failed with status: %d", resp.StatusCode)
	}

	fmt.Printf("✅ UE registered with gNB: %s (UL TEID: 0x%08x, DL TEID: 0x%08x)\n",
		ueAddr, uplinkTEID, downlinkTEID)

	return nil
}
