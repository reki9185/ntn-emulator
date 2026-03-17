package ntnlink

import (
	"math/rand"
	"time"
)

// LossModel decides whether an individual packet should be dropped
type LossModel interface {
	ShouldDrop() bool
}

// RandomLossModel implements independent per-packet probabilistic loss
type RandomLossModel struct {
	dropProb float64
	rng      *rand.Rand
}

// NewRandomLossModel creates a loss model with the given drop probability (1 - PDR)
func NewRandomLossModel(dropProb float64) *RandomLossModel {
	return &RandomLossModel{
		dropProb: dropProb,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// SetDropProb updates the drop probability (thread-unsafe; callers must synchronise)
func (m *RandomLossModel) SetDropProb(p float64) {
	m.dropProb = p
}

// ShouldDrop returns true if the packet should be dropped
func (m *RandomLossModel) ShouldDrop() bool {
	if m.dropProb <= 0 {
		return false
	}
	return m.rng.Float64() < m.dropProb
}
