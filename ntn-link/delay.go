package ntnlink

import (
	"math/rand"
	"time"
)

// DelayModel applies delay and jitter to packets
type DelayModel interface {
	GetDelay() time.Duration
}

// StaticDelay implements a fixed delay
type StaticDelay struct {
	delay time.Duration
}

// NewStaticDelay creates a static delay model
func NewStaticDelay(delay time.Duration) *StaticDelay {
	return &StaticDelay{delay: delay}
}

// GetDelay returns the static delay
func (d *StaticDelay) GetDelay() time.Duration {
	return d.delay
}

// DynamicDelay implements delay with jitter
type DynamicDelay struct {
	baseDelay time.Duration
	jitter    time.Duration
	rng       *rand.Rand
}

// NewDynamicDelay creates a dynamic delay model with jitter
func NewDynamicDelay(baseDelay, jitter time.Duration) *DynamicDelay {
	return &DynamicDelay{
		baseDelay: baseDelay,
		jitter:    jitter,
		rng:       rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// GetDelay returns delay with random jitter applied
func (d *DynamicDelay) GetDelay() time.Duration {
	// Apply uniform jitter [-jitter, +jitter]
	jitterOffset := time.Duration(d.rng.Int63n(int64(d.jitter)*2) - int64(d.jitter))
	return d.baseDelay + jitterOffset
}

// NTNDelay implements NTN-specific delay model
// Updated dynamically from ns-3 via JSONWatcher
type NTNDelay struct {
	link *Link
}

// NewNTNDelay creates an NTN delay model
func NewNTNDelay(link *Link) *NTNDelay {
	return &NTNDelay{link: link}
}

// GetDelay returns the current NTN delay
func (d *NTNDelay) GetDelay() time.Duration {
	return d.link.GetDelay()
}
