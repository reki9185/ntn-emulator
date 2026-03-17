package main

import (
	"fmt"
	"os"

	ntnlink "ntn-emulator/ntn-link"
)

func main() {
	testFile := "/home/ntn/ntn-emulator/ntn_state.json"

	fmt.Println("Replaying timeline:", testFile)

	// Create and start timeline player
	player, err := ntnlink.NewTimelinePlayer(testFile)
	if err != nil {
		fmt.Printf("❌ Failed to load timeline: %v\n", err)
		os.Exit(1)
	}

	// Register callback
	player.RegisterCallback(func(old, new *ntnlink.NTNState) {
		if old == nil {
			fmt.Printf("✓ Initial state: satellite=%s ue-ran=%.1fms PDR=%.3f\n",
				new.Satellite, new.DelayUERan, new.PDR)
		} else {
			fmt.Printf("✓ State changed: %s (ue-ran:%.1fms PDR:%.3f) -> %s (ue-ran:%.1fms PDR:%.3f)\n",
				old.Satellite, old.DelayUERan, old.PDR, new.Satellite, new.DelayUERan, new.PDR)
		}
	})

	player.Start()
	defer player.Stop()

	// Keep running and monitoring
	fmt.Println("Replaying timeline events... Press Ctrl+C to stop")

	// Block forever (or until Ctrl+C)
	select {}
}
