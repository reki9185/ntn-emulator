package ntnlink

import (
	"time"
)

// Link represents an NTN link with dynamic characteristics
type Link struct {
	currentDelay  time.Duration
	currentJitter time.Duration

	// Will be integrated with JSONWatcher
	watcher *JSONWatcher
}

// NewLink creates a new NTN link
// TODO: Integrate with JSONWatcher to update delay dynamically
func NewLink(stateFilePath string) *Link {
	return &Link{
		currentDelay:  50 * time.Millisecond, // Default NTN delay
		currentJitter: 5 * time.Millisecond,
	}
}

// GetDelay returns the current one-way propagation delay
func (l *Link) GetDelay() time.Duration {
	return l.currentDelay
}

// GetJitter returns the current jitter
func (l *Link) GetJitter() time.Duration {
	return l.currentJitter
}

// UpdateFromState updates link characteristics from NTN state
// This will be called by JSONWatcher callback
func (l *Link) UpdateFromState(state *NTNState) {
	l.currentDelay = time.Duration(state.Delay) * time.Millisecond
}
