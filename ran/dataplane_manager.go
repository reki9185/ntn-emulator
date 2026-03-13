package ran

import (
	"log"
	"sync"

	ranlink "ntn-emulator/ran/link"
)

var (
	dpMu     sync.Mutex
	activeDP *ranlink.RANDataPlane
)

func RegisterDataPlane(dp *ranlink.RANDataPlane) {
	dpMu.Lock()
	defer dpMu.Unlock()
	if activeDP != nil {
		log.Println("⚠️  Stopping previous data plane to register a new one.")
		activeDP.Stop()
	}
	activeDP = dp
}

func StopDataPlane() {
	dpMu.Lock()
	defer dpMu.Unlock()
	if activeDP != nil {
		activeDP.Stop()
		activeDP = nil
		log.Println("🔌 Data plane stopped globally.")
	}
}
