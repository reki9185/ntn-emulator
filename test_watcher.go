package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"ntn-emulator/ntn-link"
)

func main() {
	testFile := "/home/ntn/ntn-emulator/ntn_state.json"

	fmt.Println("Monitoring:", testFile)
	fmt.Println("Modify the JSON file in another terminal to see changes detected")

	// Create and start watcher
	watcher := ntnlink.NewJSONWatcher(testFile, 500*time.Millisecond)

	// Register callback
	watcher.RegisterCallback(func(old, new *ntnlink.NTNState) {
		if old == nil {
			fmt.Printf("✓ Initial state detected\n")
		} else {
			fmt.Printf("✓ State changed: %s (%.1fms) -> %s (%.1fms)\n",
				old.Satellite, old.Delay, new.Satellite, new.Delay)
		}
	})

	watcher.Start()
	defer watcher.Stop()

	// Keep running and monitoring
	fmt.Println("Watching for changes... Press Ctrl+C to stop")

	// Block forever (or until Ctrl+C)
	select {}
}

func writeState(path string, state *ntnlink.NTNState) {
	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(path, data, 0644)
}
