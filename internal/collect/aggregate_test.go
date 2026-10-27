package collect

import (
	"math"
	"strings"
	"testing"

	"github.com/nathan/sensorz/internal/model"
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

// A card with no sensor of its own is not a fault, so it must not raise the
// warning that is drawn under the dashboard. An integrated GPU without a
// thermal diode is ordinary, and a warning about it every couple of seconds
// buries the one that means something.
func TestSensorlessCardsDoNotWarn(t *testing.T) {
	devs := []model.Device{
		{Index: 0, Name: "NVIDIA GeForce GTX 1650", Temperature: 45},
		{
			Index: 1, Name: "Intel 9BC4", Temperature: math.NaN(),
			Sensorless: true, Err: "no telemetry exposed by this driver",
		},
		{Index: 2, Name: "Half-broken card", Temperature: math.NaN(), Err: "device handle is stale"},
	}

	warns := deviceWarnings(devs)
	if len(warns) != 1 {
		t.Fatalf("got %d warnings %v, want only the real failure", len(warns), warns)
	}
	if !strings.Contains(warns[0], "Half-broken card") {
		t.Errorf("warning %q does not name the failing card", warns[0])
	}
}
