package collect

import (
	"testing"
)

// The command defers the aggregator's Close and the UI model's Close, and the
// model's Close closes the aggregator. A second close of the done channel would
// panic on the way out, so closing twice has to be a no-op.
func TestCloseTwiceDoesNotPanic(t *testing.T) {
	a, err := New(Options{})
	if err != nil {
		t.Skip(err)
	}
	a.Close()
	a.Close()
}
