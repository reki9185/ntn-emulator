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

	// Schedulers for each link
	ueRanScheduler *Scheduler
	ran5GScheduler *Scheduler
}

// NewLink creates a new NTN link with dynamic delay from JSON file
func NewLink(stateFilePath string, pollInterval time.Duration) (*Link, error) {
	link := &Link{
		delayUERan:    3 * time.Millisecond, // Default UE-RAN delay
		delayRan5G:    5 * time.Millisecond, // Default RAN-5GC delay
		currentJitter: 1 * time.Millisecond, // Default jitter
	}

	// Create JSON watcher
	link.watcher = NewJSONWatcher(stateFilePath, pollInterval)

	// Register callback to update delays
	link.watcher.RegisterCallback(func(old, new *NTNState) {
		link.UpdateFromState(new)
	})

	// Create schedulers with NTN delay models
	link.ueRanScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkUERan})
	link.ran5GScheduler = NewScheduler(&linkDelayModel{link: link, linkType: LinkRan5G})

	return link, nil
}

// Start starts the NTN link (watcher and schedulers)
func (l *Link) Start() error {
	// Start JSON watcher
	if err := l.watcher.Start(); err != nil {
		return err
	}

	// Start schedulers
	l.ueRanScheduler.Start()
	l.ran5GScheduler.Start()

	log.Printf("🛰️  NTN Link started: UE-RAN=%.1fms, RAN-5G=%.1fms",
		l.delayUERan.Seconds()*1000, l.delayRan5G.Seconds()*1000)

	return nil
}

// Stop stops the NTN link
func (l *Link) Stop() {
	if l.watcher != nil {
		l.watcher.Stop()
	}
	if l.ueRanScheduler != nil {
		l.ueRanScheduler.Stop()
	}
	if l.ran5GScheduler != nil {
		l.ran5GScheduler.Stop()
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

// GetScheduler returns the scheduler for a specific link
func (l *Link) GetScheduler(linkType LinkType) *Scheduler {
	switch linkType {
	case LinkUERan:
		return l.ueRanScheduler
	case LinkRan5G:
		return l.ran5GScheduler
	default:
		return nil
	}
}

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
