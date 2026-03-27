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
	events       []TimelineEvent
	startTime    time.Time     // Absolute time when timeline t=0 begins
	scheduledAt  *time.Time    // Optional: scheduled absolute start time
	index        int

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

// SetScheduledStartTime sets an absolute time when the timeline should begin.
// If set, Start() will wait until this time before beginning event replay.
// This enables multiple processes to synchronize their timelines to a common t=0.
func (p *TimelinePlayer) SetScheduledStartTime(t time.Time) {
	p.scheduledAt = &t
}

// Start begins the event replay loop.  T=0 is set to either:
//   - The scheduled start time (if SetScheduledStartTime was called), or
//   - The moment Start() is called (default behavior, backward compatible)
// All event times in the timeline are relative to this t=0 instant.
func (p *TimelinePlayer) Start() error {
	if p.scheduledAt != nil {
		now := time.Now()
		if now.Before(*p.scheduledAt) {
			waitDuration := p.scheduledAt.Sub(now)
			log.Printf("⏱️  [Timeline] Waiting %.3fs until scheduled start at %s",
				waitDuration.Seconds(), p.scheduledAt.Format("15:04:05.000"))
			time.Sleep(waitDuration)
		} else if now.After(p.scheduledAt.Add(5 * time.Second)) {
			// Warn if we're significantly late (>5s), but still proceed
			late := now.Sub(*p.scheduledAt)
			log.Printf("⚠️  [Timeline] Starting %.3fs late (scheduled: %s, now: %s)",
				late.Seconds(), p.scheduledAt.Format("15:04:05.000"), now.Format("15:04:05.000"))
		}
		p.startTime = *p.scheduledAt
	} else {
		p.startTime = time.Now()
	}
	
	p.index = 0
	p.wg.Add(1)
	go p.replayLoop()
	
	log.Printf("▶️  [Timeline] Started at t=0: %s (UNIX: %d.%06d)",
		p.startTime.Format("15:04:05.000000"),
		p.startTime.Unix(),
		p.startTime.Nanosecond()/1000)
	
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
	// Calculate elapsed time since t=0 (startTime), using absolute time comparison
	now := time.Now()
	elapsed := now.Sub(p.startTime).Seconds()
	
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
		absTime := p.startTime.Add(time.Duration(event.Time * float64(time.Second)))
		log.Printf("🔄 [Timeline] t=%.3fs (abs: %s) handover_start — PDR=0.0 (full loss)", 
			event.Time, absTime.Format("15:04:05.000"))

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
		absTime := p.startTime.Add(time.Duration(event.Time * float64(time.Second)))
		log.Printf("✅ [Timeline] t=%.3fs (abs: %s) handover_end — satellite=%s UE-RAN=%.1fms RAN-5G=%.1fms PDR=%.3f",
			event.Time, absTime.Format("15:04:05.000"), next.Satellite, next.DelayUERan, next.DelayRan5G, next.PDR)

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
		absTime := p.startTime.Add(time.Duration(event.Time * float64(time.Second)))
		log.Printf("📡 [Timeline] t=%.3fs (abs: %s) state update — satellite=%s UE-RAN=%.1fms RAN-5G=%.1fms PDR=%.3f",
			event.Time, absTime.Format("15:04:05.000"), next.Satellite, next.DelayUERan, next.DelayRan5G, next.PDR)
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
