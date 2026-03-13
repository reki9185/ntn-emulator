package ntnlink

import (
	"log"
	"sync"
	"time"
)

// LinkType identifies which link the delay applies to
type LinkType int

const (
	LinkUERan LinkType = iota // UE to RAN link
	LinkRan5G                 // RAN to 5GC link
)

// Link represents an NTN link with dynamic characteristics
type Link struct {
	delayUERan    time.Duration
	delayRan5G    time.Duration
	currentJitter time.Duration
	mutex         sync.RWMutex

	// JSON watcher for dynamic updates
	watcher *JSONWatcher

	// Four dedicated schedulers, one per direction per hop:
	// Uplink:   UE -[UE-RAN]-> RAN -[RAN-5G]-> UPF
	// Downlink: UPF -[RAN-5G]-> RAN -[UE-RAN]-> UE
	ueRanUplinkScheduler   *Scheduler // UE -> RAN (uplink leg 1)
	ran5GUplinkScheduler   *Scheduler // RAN -> UPF (uplink leg 2)
	ran5GDownlinkScheduler *Scheduler // UPF -> RAN (downlink leg 1)
	ueRanDownlinkScheduler *Scheduler // RAN -> UE (downlink leg 2)
}

// NewLink creates a new NTN link with dynamic delay from JSON file
func NewLink(stateFilePath string, pollInterval time.Duration) (*Link, error) {
	link := &Link{
		delayUERan:    0, // Will be set from JSON file
		delayRan5G:    0, // Will be set from JSON file
		currentJitter: 0, // Will be set from JSON file
	}

	// Create JSON watcher
	link.watcher = NewJSONWatcher(stateFilePath, pollInterval)

	// Read initial state from JSON file immediately
	initialState, err := link.watcher.readStateFile()
	if err != nil {
		log.Printf("⚠️  Warning: Failed to read initial NTN state from %s: %v", stateFilePath, err)
		log.Printf("    Using zero delay until state file is available")
	} else {
		// Apply initial state
		link.UpdateFromState(initialState)
		log.Printf("Initial NTN state: Satellite=%s, UE-RAN=%.1fms, RAN-5G=%.1fms, Timestamp=%.1fs",
			initialState.Satellite, initialState.DelayUERan, initialState.DelayRan5G, initialState.Timestamp)
	}

	// Register callback to update delays when state changes
	link.watcher.RegisterCallback(func(old, new *NTNState) {
		link.UpdateFromState(new)
	})

	// Create four dedicated schedulers, one per direction per hop
	link.ueRanUplinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkUERan})
	link.ran5GUplinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkRan5G})
	link.ran5GDownlinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkRan5G})
	link.ueRanDownlinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkUERan})

	return link, nil
}

// Start starts the NTN link (watcher and schedulers)
func (l *Link) Start() error {
	// Start JSON watcher
	if err := l.watcher.Start(); err != nil {
		return err
	}

	// Start all four directional schedulers
	l.ueRanUplinkScheduler.Start()
	l.ran5GUplinkScheduler.Start()
	l.ran5GDownlinkScheduler.Start()
	l.ueRanDownlinkScheduler.Start()

	log.Printf("🛰️  NTN Link started: UE-RAN=%.1fms, RAN-5G=%.1fms",
		l.delayUERan.Seconds()*1000, l.delayRan5G.Seconds()*1000)

	return nil
}

// Stop stops the NTN link
func (l *Link) Stop() {
	if l.watcher != nil {
		l.watcher.Stop()
	}
	if l.ueRanUplinkScheduler != nil {
		l.ueRanUplinkScheduler.Stop()
	}
	if l.ran5GUplinkScheduler != nil {
		l.ran5GUplinkScheduler.Stop()
	}
	if l.ran5GDownlinkScheduler != nil {
		l.ran5GDownlinkScheduler.Stop()
	}
	if l.ueRanDownlinkScheduler != nil {
		l.ueRanDownlinkScheduler.Stop()
	}
}

// GetDelay returns the current one-way propagation delay for a specific link
func (l *Link) GetDelay(linkType LinkType) time.Duration {
	l.mutex.RLock()
	defer l.mutex.RUnlock()

	switch linkType {
	case LinkUERan:
		return l.delayUERan
	case LinkRan5G:
		return l.delayRan5G
	default:
		return 0
	}
}

// GetJitter returns the current jitter
func (l *Link) GetJitter() time.Duration {
	l.mutex.RLock()
	defer l.mutex.RUnlock()
	return l.currentJitter
}

// Directional scheduler accessors
func (l *Link) GetUERanUplinkScheduler() *Scheduler   { return l.ueRanUplinkScheduler }
func (l *Link) GetRan5GUplinkScheduler() *Scheduler   { return l.ran5GUplinkScheduler }
func (l *Link) GetRan5GDownlinkScheduler() *Scheduler { return l.ran5GDownlinkScheduler }
func (l *Link) GetUERanDownlinkScheduler() *Scheduler { return l.ueRanDownlinkScheduler }

// UpdateFromState updates link characteristics from NTN state
// This is called by JSONWatcher callback when state changes
func (l *Link) UpdateFromState(state *NTNState) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.delayUERan = time.Duration(state.DelayUERan) * time.Millisecond
	l.delayRan5G = time.Duration(state.DelayRan5G) * time.Millisecond

	log.Printf("🛰️  NTN Link updated: UE-RAN=%.1fms, RAN-5G=%.1fms (Sat: %s)",
		state.DelayUERan, state.DelayRan5G, state.Satellite)
}

// linkDelayModel is a DelayModel that reads from a Link
type linkDelayModel struct {
	link     *Link
	linkType LinkType
}

func (d *linkDelayModel) GetDelay() time.Duration {
	return d.link.GetDelay(d.linkType)
}
