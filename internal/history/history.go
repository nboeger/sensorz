// Package history keeps the rolling time series behind every graph.
package history

import (
	"math"
	"sync"
	"time"
)

// Series is a fixed-capacity ring of samples for one metric.
type Series struct {
	id     string
	values []float64
	times  []time.Time
	head   int
	n      int
}

// Len returns the number of samples currently held.
func (s *Series) Len() int { return s.n }

// Capacity returns the ring size.
func (s *Series) Capacity() int { return len(s.values) }

// Latest returns the most recent sample, and NaN when the series is empty.
func (s *Series) Latest() float64 {
	if s.n == 0 {
		return math.NaN()
	}
	return s.values[(s.head-1+len(s.values))%len(s.values)]
}

// At returns the sample recorded n steps back from the newest, 0 being the
// newest. It returns NaN past the end of the retained window.
func (s *Series) At(i int) float64 {
	if i < 0 || i >= s.n {
		return math.NaN()
	}
	// The newest sample sits just before head.
	idx := (s.head - 1 - i + len(s.values)*2) % len(s.values)
	return s.values[idx]
}

// Time returns the timestamp of the sample n steps back from the newest.
func (s *Series) Time(i int) time.Time {
	if i < 0 || i >= s.n || len(s.times) != len(s.values) {
		return time.Time{}
	}
	idx := (s.head - 1 - i + len(s.values)*2) % len(s.values)
	return s.times[idx]
}

// Store keeps one Series per metric id.
type Store struct {
	mu     sync.RWMutex
	series map[string]*Series
	cap    int
}

// NewStore builds a store retaining cap samples per series.
func NewStore(cap int) *Store {
	if cap <= 0 {
		cap = 256
	}
	return &Store{series: map[string]*Series{}, cap: cap}
}

// Push records one sample for a metric, creating the series on first use.
func (st *Store) Push(id string, v float64, at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()

	s, ok := st.series[id]
	if !ok {
		s = &Series{
			id:     id,
			values: make([]float64, st.cap),
			times:  make([]time.Time, st.cap),
		}
		st.series[id] = s
	}
	s.push(v, at)
}

// PushSnapshot records every valid metric in a snapshot. Metrics that were
// present last frame but absent now have NaN pushed for them, so a vanished
// sensor shows up as a gap in the graph instead of a straight line connecting
// stale values.
func (st *Store) PushSnapshot(ids []string, lookup func(string) (float64, bool), at time.Time) {
	st.mu.Lock()
	defer st.mu.Unlock()

	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		seen[id] = true
		s, ok := st.series[id]
		if !ok {
			s = &Series{id: id, values: make([]float64, st.cap), times: make([]time.Time, st.cap)}
			st.series[id] = s
		}
		if v, ok := lookup(id); ok {
			s.push(v, at)
		} else {
			s.push(math.NaN(), at)
		}
	}

	// Any series not in this snapshot also gets a gap sample.
	for id, s := range st.series {
		if !seen[id] {
			s.push(math.NaN(), at)
		}
	}
}

// Get returns the series for an id, or nil when there is none.
func (st *Store) Get(id string) *Series {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.series[id]
}

// Len returns the number of tracked series.
func (st *Store) Len() int {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.series)
}

func (s *Series) push(v float64, at time.Time) {
	s.values[s.head] = v
	s.times[s.head] = at
	s.head = (s.head + 1) % len(s.values)
	if s.n < len(s.values) {
		s.n++
	}
}

// Values returns the samples oldest-first. The returned slice is a fresh copy,
// so the renderer can walk it without holding the store's lock and a repaint
// never blocks the collector.
func (s *Series) Values() []float64 {
	out := make([]float64, 0, s.n)
	start := (s.head - s.n + len(s.values)*2) % len(s.values)
	for i := 0; i < s.n; i++ {
		out = append(out, s.values[(start+i)%len(s.values)])
	}
	return out
}

// Times returns the sample timestamps oldest-first.
func (s *Series) Times() []time.Time {
	if len(s.times) != len(s.values) {
		return nil
	}
	out := make([]time.Time, 0, s.n)
	start := (s.head - s.n + len(s.values)*2) % len(s.values)
	for i := 0; i < s.n; i++ {
		out = append(out, s.times[(start+i)%len(s.values)])
	}
	return out
}
