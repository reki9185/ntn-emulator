package ntnlink

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// NTNState represents the runtime NTN link state from ns-3
type NTNState struct {
	Timestamp float64 `json:"timestamp"`
	Satellite string  `json:"satellite"`
	Delay     float64 `json:"delay_ms"`
}

// StateUpdateCallback is called when NTN state changes
type StateUpdateCallback func(oldState, newState *NTNState)

// JSONWatcher monitors ntn_state.json and notifies on changes
type JSONWatcher struct {
	filePath     string
	pollInterval time.Duration
	currentState *NTNState
	stateMutex   sync.RWMutex

	// Callback mechanism
	callbacks     []StateUpdateCallback
	callbackMutex sync.RWMutex

	// Channel mechanism (alternative to callbacks)
	updateChan chan *NTNState

	// Control
	stopChan chan struct{}
	stopped  bool
	wg       sync.WaitGroup
}

// NewJSONWatcher creates a new NTN state file watcher
func NewJSONWatcher(filePath string, pollInterval time.Duration) *JSONWatcher {
	return &JSONWatcher{
		filePath:     filePath,
		pollInterval: pollInterval,
		callbacks:    make([]StateUpdateCallback, 0),
		updateChan:   make(chan *NTNState, 10),
		stopChan:     make(chan struct{}),
	}
}

// RegisterCallback adds a callback to be invoked on state changes
func (w *JSONWatcher) RegisterCallback(cb StateUpdateCallback) {
	w.callbackMutex.Lock()
	defer w.callbackMutex.Unlock()
	w.callbacks = append(w.callbacks, cb)
}

// GetUpdateChannel returns a channel that receives state updates
// Useful for select-based event loops
func (w *JSONWatcher) GetUpdateChannel() <-chan *NTNState {
	return w.updateChan
}

// GetCurrentState returns the current NTN state (thread-safe)
func (w *JSONWatcher) GetCurrentState() *NTNState {
	w.stateMutex.RLock()
	defer w.stateMutex.RUnlock()
	if w.currentState == nil {
		return nil
	}
	// Return a copy to avoid external mutation
	stateCopy := *w.currentState
	return &stateCopy
}

// Start begins monitoring the JSON file
func (w *JSONWatcher) Start() error {
	// Read initial state
	initialState, err := w.readStateFile()
	if err != nil {
		log.Printf("Warning: Could not read initial state: %v", err)
		// Continue anyway - file might be created later
	} else {
		w.stateMutex.Lock()
		w.currentState = initialState
		w.stateMutex.Unlock()
		log.Printf("Initial NTN state: Satellite=%s, Delay=%.1fms, Timestamp=%.1fs",
			initialState.Satellite, initialState.Delay, initialState.Timestamp)
	}

	// Start monitoring goroutine
	w.wg.Add(1)
	go w.watchLoop()

	return nil
}

// Stop stops the watcher
func (w *JSONWatcher) Stop() {
	if w.stopped {
		return
	}

	close(w.stopChan)
	w.stopped = true
	w.wg.Wait()
	close(w.updateChan)
}

// watchLoop periodically checks for file changes
func (w *JSONWatcher) watchLoop() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			newState, err := w.readStateFile()
			if err != nil {
				// File might not exist yet or be temporarily unavailable
				continue
			}

			// Check if state changed
			w.stateMutex.Lock()
			oldState := w.currentState
			changed := w.hasStateChanged(oldState, newState)
			if changed {
				w.currentState = newState
			}
			w.stateMutex.Unlock()

			if changed {
				w.notifyStateChange(oldState, newState)
			}
		}
	}
}

// readStateFile reads and parses the JSON state file
func (w *JSONWatcher) readStateFile() (*NTNState, error) {
	data, err := os.ReadFile(w.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state NTNState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state JSON: %w", err)
	}

	return &state, nil
}

// hasStateChanged checks if the state has meaningfully changed
func (w *JSONWatcher) hasStateChanged(old, new *NTNState) bool {
	if old == nil {
		return true
	}

	// Check for satellite change (most important for NTN)
	if old.Satellite != new.Satellite {
		return true
	}

	// Check for significant delay change (>1ms difference)
	if absFloat(old.Delay-new.Delay) > 1.0 {
		return true
	}

	// Check for timestamp update (indicates fresh data)
	if old.Timestamp != new.Timestamp {
		return true
	}

	return false
}

// notifyStateChange notifies all listeners of state changes
func (w *JSONWatcher) notifyStateChange(old, new *NTNState) {
	// Log the change
	if old == nil {
		log.Printf("NTN state initialized: Satellite=%s, Delay=%.1fms",
			new.Satellite, new.Delay)
	} else if old.Satellite != new.Satellite {
		log.Printf("Satellite handover: %s -> %s (Delay: %.1fms -> %.1fms)",
			old.Satellite, new.Satellite, old.Delay, new.Delay)
	} else {
		log.Printf("Link state update: Delay %.1fms -> %.1fms (Satellite: %s)",
			old.Delay, new.Delay, new.Satellite)
	}

	// Invoke callbacks
	w.callbackMutex.RLock()
	callbacks := w.callbacks
	w.callbackMutex.RUnlock()

	for _, cb := range callbacks {
		cb(old, new)
	}

	// Send to channel (non-blocking)
	select {
	case w.updateChan <- new:
	default:
		log.Printf("Warning: Update channel full, dropping state update")
	}
}

// absFloat returns absolute value of a float64
func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
