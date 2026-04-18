package statusstream

import (
	"context"
	"sync"

	"rig/internal/core"
)

type Hub struct {
	mu          sync.Mutex
	subscribers map[int]*hubSubscriber
	nextID      int
}

type hubSubscriber struct {
	ch     chan core.TaskStatusUpdate
	mu     sync.RWMutex
	closed bool
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[int]*hubSubscriber),
	}
}

func (h *Hub) Subscribe(ctx context.Context) (<-chan core.TaskStatusUpdate, func()) {
	if h == nil {
		ch := make(chan core.TaskStatusUpdate)
		close(ch)
		return ch, func() {}
	}

	subscriber := newHubSubscriber(16)

	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subscribers[id] = subscriber
	h.mu.Unlock()

	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			h.mu.Lock()
			current, ok := h.subscribers[id]
			if ok {
				delete(h.subscribers, id)
			}
			h.mu.Unlock()
			if ok {
				current.close()
			}
		})
	}

	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			cleanup()
		}()
	}

	return subscriber.ch, cleanup
}

func (h *Hub) Publish(update core.TaskStatusUpdate) {
	if h == nil {
		return
	}

	h.mu.Lock()
	subscribers := make([]*hubSubscriber, 0, len(h.subscribers))
	for _, subscriber := range h.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	h.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.publish(update)
	}
}

func newHubSubscriber(buffer int) *hubSubscriber {
	if buffer < 0 {
		buffer = 0
	}

	return &hubSubscriber{
		ch: make(chan core.TaskStatusUpdate, buffer),
	}
}

func (s *hubSubscriber) publish(update core.TaskStatusUpdate) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.closed {
		return false
	}

	select {
	case s.ch <- update:
		return true
	default:
		return false
	}
}

func (s *hubSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}

	s.closed = true
	close(s.ch)
}
