package history

import (
	"math"
	"testing"
	"time"
)

func TestRingWrapsAndKeepsOrder(t *testing.T) {
	st := NewStore(4)
	base := time.Unix(0, 0)
	for i := 0; i < 10; i++ {
		st.Push("a", float64(i), base.Add(time.Duration(i)*time.Second))
	}

	s := st.Get("a")
	if s.Len() != 4 {
		t.Fatalf("Len = %d, want the capacity 4", s.Len())
	}
	got := s.Values()
	want := []float64{6, 7, 8, 9}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Values = %v, want %v", got, want)
		}
	}
	if s.Latest() != 9 {
		t.Errorf("Latest = %v, want 9", s.Latest())
	}
	// At(i) counts back from the newest.
	if s.At(0) != 9 || s.At(3) != 6 {
		t.Errorf("At(0)=%v At(3)=%v, want 9 and 6", s.At(0), s.At(3))
	}
	if !math.IsNaN(s.At(4)) {
		t.Errorf("At past the end = %v, want NaN", s.At(4))
	}
}

func TestGapsAreRecordedNotInterpolated(t *testing.T) {
	st := NewStore(8)
	base := time.Unix(0, 0)
	lookup := func(id string) (float64, bool) {
		if id == "a" {
			return 10, true
		}
		return 0, false
	}
	// Two samples with the metric present, then one where it has gone away.
	st.PushSnapshot([]string{"a"}, lookup, base)
	st.PushSnapshot([]string{"a"}, lookup, base.Add(time.Second))
	st.PushSnapshot([]string{"b"}, lookup, base.Add(2*time.Second))

	a := st.Get("a").Values()
	if len(a) != 3 {
		t.Fatalf("a has %d samples, want 3", len(a))
	}
	if a[2] == 10 {
		t.Error("a missing metric was recorded as a repeated value; the graph will show a false straight line")
	}
	if !math.IsNaN(a[2]) {
		t.Errorf("a[2] = %v, want NaN", a[2])
	}
}

// Values returns a copy. A caller holding it must not be able to corrupt the
// store by writing to it.
func TestValuesIsACopy(t *testing.T) {
	st := NewStore(4)
	st.Push("a", 1, time.Unix(0, 0))
	st.Push("a", 2, time.Unix(0, 0))

	got := st.Get("a").Values()
	got[0] = 999

	if st.Get("a").Values()[0] == 999 {
		t.Error("Values returned the backing array, not a copy")
	}
}

func TestEmptySeries(t *testing.T) {
	s := &Series{values: make([]float64, 4), times: make([]time.Time, 4)}
	if !math.IsNaN(s.Latest()) {
		t.Error("Latest on an empty series should be NaN")
	}
	if s.Len() != 0 {
		t.Errorf("Len = %d, want 0", s.Len())
	}
	if !math.IsNaN(s.At(0)) {
		t.Error("At(0) on an empty series should be NaN")
	}
}
