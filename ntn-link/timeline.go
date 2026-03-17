package ntnlink

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"
)

// TimelineEvent represents a single scheduled link-state change produced by ns-3.
// Fields are omitempty so partial events (e.g. handover_start) need not carry
// redundant values.
type TimelineEvent struct {
	Time       float64 `json:"time"`
	Satellite  string  `json:"satellite,omitempty"`
	DelayUERan float64 `json:"delay_ue_ran,omitempty"`
	DelayRan5G float64 `json:"delay_ran_5g,omitempty"`
	PDR        float64 `json:"pdr,omitempty"`
	EventType  string  `json:"event,omitempty"`
}

// TimelinePlayer loads a precomputed event trace and replays it against an
// internal clock, calling registered callbacks exactly as JSONWatcher did.
type TimelinePlayer struct {
	events    []TimelineEvent
	startTime time.Time
	index     int

	currentState  *NTNState
	stateMutex    sync.RWMutex
	callbacks     []StateUpdateCallback
	callbackMutex sync.RWMutex
	updateChan    chan *NTNState

	stopChan chan struct{}
	stopped  bool
	wg       sync.WaitGroup
}

// NewTimelinePlayer loads the timeline JSON from filePath and returns a player
// ready to be started.  Events are sorted by time so out-of-order files are
// handled gracefully.
func NewTimelinePlayer(filePath string) (*TimelinePlayer, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read timeline file: %w", err)
	}

	var events []TimelineEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("failed to parse timeline JSON: %w", err)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].Time < events[j].Time
	})

	p := &TimelinePlayer{
		events:     events,
		updateChan: make(chan *NTNState, 10),
		stopChan:   make(chan struct{}),
		callbacks:  make([]StateUpdateCallback, 0),
	}

	// Derive initial state from the first non-handover event so callers can
	// read a sensible state before Start() is called.
	initial := &NTNState{PDR: 1.0}
	for _, e := range events {
		if e.EventType == "" || e.EventType == "handover_end" {
			initial = &NTNState{
				Timestamp:  e.Time,
				Satellite:  e.Satellite,
				DelayUERan: e.DelayUERan,
				DelayRan5G: e.DelayRan5G,
				PDR:        e.PDR,
			}
			break
		}
	}
	p.currentState = initial

	log.Printf("📋 Timeline loaded: %d events from %s", len(events), filePath)
	return p, nil
}

// RegisterCallback adds a callback invoked on every state transition.
func (p *TimelinePlayer) RegisterCallback(cb StateUpdateCallback) {
	p.callbackMutex.Lock()
	defer p.callbackMutex.Unlock()
	p.callbacks = append(p.callbacks, cb)
}

// GetUpdateChannel returns a channel that receives state snapshots after each
// applied event.  Drop-safe: a full channel logs a warning and discards.
func (p *TimelinePlayer) GetUpdateChannel() <-chan *NTNState {
	return p.updateChan
}

// GetCurrentState returns a copy of the current NTN state (thread-safe).
func (p *TimelinePlayer) GetCurrentState() *NTNState {
	p.stateMutex.RLock()
	defer p.stateMutex.RUnlock()
	if p.currentState == nil {
		return nil
	}
	cp := *p.currentState
	return &cp
}

// Start begins the event replay loop.  T=0 is set to the moment Start() is
// called; all event times are relative to this instant.
func (p *TimelinePlayer) Start() error {
	p.startTime = time.Now()
	p.index = 0
	p.wg.Add(1)
	go p.replayLoop()
	return nil
}

// Stop halts the replay loop and closes the update channel.
func (p *TimelinePlayer) Stop() {
	if p.stopped {
		return
	}
	close(p.stopChan)
	p.stopped = true
	p.wg.Wait()
	close(p.updateChan)
}

// replayLoop ticks every millisecond and fires any events whose scheduled time
// has passed.
func (p *TimelinePlayer) replayLoop() {
	defer p.wg.Done()

	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-ticker.C:
			p.checkAndApplyEvents()
		}
	}
}

func (p *TimelinePlayer) checkAndApplyEvents() {
	elapsed := time.Since(p.startTime).Seconds()
	for p.index < len(p.events) && p.events[p.index].Time <= elapsed {
		event := p.events[p.index]
		p.index++
		p.applyEvent(event)
	}
}

// applyEvent merges the event into the running NTNState and notifies listeners.
func (p *TimelinePlayer) applyEvent(event TimelineEvent) {
	p.stateMutex.Lock()

	var old *NTNState
	if p.currentState != nil {
		cp := *p.currentState
		old = &cp
	}

	next := NTNState{}
	if old != nil {
		next = *old
	}
	next.Timestamp = event.Time
	next.Event = event.EventType // propagate event type so callers can react

	switch event.EventType {
	case "handover_start":
		// Full packet loss during handover interruption.
		next.PDR = 0.0
		log.Printf("🔄 [Timeline] t=%.3fs handover_start — PDR=0.0 (full loss)", event.Time)

	case "handover_end":
		if event.Satellite != "" {
			next.Satellite = event.Satellite
		}
		if event.DelayUERan != 0 {
			next.DelayUERan = event.DelayUERan
		}
		if event.DelayRan5G != 0 {
			next.DelayRan5G = event.DelayRan5G
		}
		if event.PDR != 0 {
			next.PDR = event.PDR
		}
		log.Printf("✅ [Timeline] t=%.3fs handover_end — satellite=%s UE-RAN=%.1fms RAN-5G=%.1fms PDR=%.3f",
			event.Time, next.Satellite, next.DelayUERan, next.DelayRan5G, next.PDR)

	default:
		// Regular state-update event.
		if event.Satellite != "" {
			next.Satellite = event.Satellite
		}
		if event.DelayUERan != 0 {
			next.DelayUERan = event.DelayUERan
		}
		if event.DelayRan5G != 0 {
			next.DelayRan5G = event.DelayRan5G
		}
		if event.PDR != 0 {
			next.PDR = event.PDR
		}
		log.Printf("📡 [Timeline] t=%.3fs state update — satellite=%s UE-RAN=%.1fms RAN-5G=%.1fms PDR=%.3f",
			event.Time, next.Satellite, next.DelayUERan, next.DelayRan5G, next.PDR)
	}

	p.currentState = &next
	p.stateMutex.Unlock()

	p.notifyStateChange(old, &next)
}

func (p *TimelinePlayer) notifyStateChange(old, new *NTNState) {
	p.callbackMutex.RLock()
	callbacks := p.callbacks
	p.callbackMutex.RUnlock()

	for _, cb := range callbacks {
		cb(old, new)
	}

	select {
	case p.updateChan <- new:
	default:
		log.Printf("Warning: Timeline update channel full, dropping state update")
	}
}
