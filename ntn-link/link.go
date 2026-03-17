package ntnlink

import (
	"log"
	"math/rand"
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
	dropProb      float64
	mutex         sync.RWMutex

	// JSON watcher for dynamic updates
	player *TimelinePlayer

	// Four dedicated schedulers, one per direction per hop:
	// Uplink:   UE -[UE-RAN]-> RAN -[RAN-5G]-> UPF
	// Downlink: UPF -[RAN-5G]-> RAN -[UE-RAN]-> UE
	ueRanUplinkScheduler   *Scheduler // UE -> RAN (uplink leg 1)
	ran5GUplinkScheduler   *Scheduler // RAN -> UPF (uplink leg 2)
	ran5GDownlinkScheduler *Scheduler // UPF -> RAN (downlink leg 1)
	ueRanDownlinkScheduler *Scheduler // RAN -> UE (downlink leg 2)
}

// NewLink creates a new NTN link that replays events from a timeline JSON file.
func NewLink(timelineFilePath string) (*Link, error) {
	link := &Link{}

	// Create timeline player
	player, err := NewTimelinePlayer(timelineFilePath)
	if err != nil {
		log.Printf("⚠️  Warning: Failed to load NTN timeline from %s: %v", timelineFilePath, err)
		log.Printf("    Using zero delay and PDR=1.0 until timeline file is available")
		player = &TimelinePlayer{
			updateChan:   make(chan *NTNState, 10),
			stopChan:     make(chan struct{}),
			callbacks:    make([]StateUpdateCallback, 0),
			currentState: &NTNState{PDR: 1.0},
		}
	}
	link.player = player

	// Apply initial state immediately
	if initialState := player.GetCurrentState(); initialState != nil {
		link.UpdateFromState(initialState)
		log.Printf("Initial NTN state: Satellite=%s, UE-RAN=%.1fms, RAN-5G=%.1fms, PDR=%.3f",
			initialState.Satellite, initialState.DelayUERan, initialState.DelayRan5G, initialState.PDR)
	}

	// Register callback to update link state when timeline events fire
	player.RegisterCallback(func(old, new *NTNState) {
		link.UpdateFromState(new)
	})

	// Create four dedicated schedulers, one per direction per hop
	lossModel := &linkLossModel{link: link}
	link.ueRanUplinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkUERan}, lossModel)
	link.ran5GUplinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkRan5G}, lossModel)
	link.ran5GDownlinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkRan5G}, lossModel)
	link.ueRanDownlinkScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkUERan}, lossModel)

	return link, nil
}

// Start starts the NTN link (timeline player and schedulers)
func (l *Link) Start() error {
	// Start timeline player
	if err := l.player.Start(); err != nil {
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
	if l.player != nil {
		l.player.Stop()
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
	l.dropProb = 1.0 - state.PDR

	log.Printf("🛰️  NTN Link updated: UE-RAN=%.1fms, RAN-5G=%.1fms, PDR=%.3f (Sat: %s)",
		state.DelayUERan, state.DelayRan5G, state.PDR, state.Satellite)
}

// linkDelayModel is a DelayModel that reads from a Link
type linkDelayModel struct {
	link     *Link
	linkType LinkType
}

func (d *linkDelayModel) GetDelay() time.Duration {
	return d.link.GetDelay(d.linkType)
}

// linkLossModel is a LossModel that reads drop probability from a Link
type linkLossModel struct {
	link *Link
	rng  *rand.Rand
}

func (m *linkLossModel) ShouldDrop() bool {
	m.link.mutex.RLock()
	p := m.link.dropProb
	m.link.mutex.RUnlock()

	if p <= 0 {
		return false
	}
	if m.rng == nil {
		m.rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return m.rng.Float64() < p
}
