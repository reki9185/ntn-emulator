package ran

import (
	"log"
	"sync"

	ranlink "ntn-emulator/ran/link"
)

var (
	dpMu      sync.Mutex
	activeDP  *ranlink.RANDataPlane
	dpStopped = true
)

func RegisterDataPlane(dp *ranlink.RANDataPlane) {
	dpMu.Lock()
	defer dpMu.Unlock()
	if activeDP != nil {
		log.Println("⚠️  Stopping previous data plane to register a new one.")
		activeDP.Stop()
	}
	activeDP = dp
	dpStopped = false
}

func StopDataPlane() {
	dpMu.Lock()
	defer dpMu.Unlock()

	if dpStopped {
		log.Println("⚠️  StopDataPlane() called but already stopped (ignored).")
		return
	}

	if activeDP != nil {
		activeDP.Stop()
		activeDP = nil
	}

	dpStopped = true
	log.Println("🔌 Data plane stopped globally.")
}
