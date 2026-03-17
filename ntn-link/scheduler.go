package ntnlink

import (
	"container/heap"
	"sync"
	"time"
)

// ScheduledPacket represents a packet with scheduled delivery time
type ScheduledPacket struct {
	Data      []byte
	DeliverAt time.Time
	Index     int // heap index
}

// PacketQueue implements a priority queue for scheduled packets
type PacketQueue []*ScheduledPacket

func (pq PacketQueue) Len() int { return len(pq) }

func (pq PacketQueue) Less(i, j int) bool {
	return pq[i].DeliverAt.Before(pq[j].DeliverAt)
}

func (pq PacketQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].Index = i
	pq[j].Index = j
}

func (pq *PacketQueue) Push(x interface{}) {
	n := len(*pq)
	packet := x.(*ScheduledPacket)
	packet.Index = n
	*pq = append(*pq, packet)
}

func (pq *PacketQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	packet := old[n-1]
	old[n-1] = nil
	packet.Index = -1
	*pq = old[0 : n-1]
	return packet
}

// Scheduler manages delayed packet delivery
type Scheduler struct {
	queue      PacketQueue
	mutex      sync.Mutex
	delayModel DelayModel
	lossModel  LossModel

	// Notification channel for ready packets
	readyChan chan []byte
	stopChan  chan struct{}
	wg        sync.WaitGroup
}

// NewScheduler creates a new packet scheduler
func NewScheduler(delayModel DelayModel, lossModel LossModel) *Scheduler {
	return &Scheduler{
		queue:      make(PacketQueue, 0),
		delayModel: delayModel,
		lossModel:  lossModel,
		readyChan:  make(chan []byte, 100),
		stopChan:   make(chan struct{}),
	}
}

// Start begins the scheduler processing loop
func (s *Scheduler) Start() {
	heap.Init(&s.queue)

	s.wg.Add(1)
	go s.processLoop()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	close(s.stopChan)
	s.wg.Wait()
	close(s.readyChan)
}

// Enqueue adds a packet to the scheduler
func (s *Scheduler) Enqueue(data []byte) {
	if s.lossModel != nil && s.lossModel.ShouldDrop() {
		return // drop packet
	}

	delay := s.delayModel.GetDelay()
	deliverAt := time.Now().Add(delay)

	packet := &ScheduledPacket{
		Data:      data,
		DeliverAt: deliverAt,
	}

	s.mutex.Lock()
	heap.Push(&s.queue, packet)
	s.mutex.Unlock()
}

// GetReadyChannel returns the channel for ready packets
func (s *Scheduler) GetReadyChannel() <-chan []byte {
	return s.readyChan
}

// processLoop periodically checks for ready packets
func (s *Scheduler) processLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(1 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.deliverReadyPackets()
		}
	}
}

// deliverReadyPackets delivers all packets whose time has come
func (s *Scheduler) deliverReadyPackets() {
	now := time.Now()

	s.mutex.Lock()
	defer s.mutex.Unlock()

	for s.queue.Len() > 0 {
		packet := s.queue[0]
		if packet.DeliverAt.After(now) {
			// Not ready yet
			break
		}

		// Remove from queue
		heap.Pop(&s.queue)

		// Deliver packet
		select {
		case s.readyChan <- packet.Data:
		default:
			// Channel full, packet dropped (TODO: add metrics)
		}
	}
}
