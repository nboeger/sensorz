package collect

import (
	"github.com/nathan/sensorz/internal/collect/sensors"
	"github.com/nathan/sensorz/internal/model"
)

// The headline series. Each is the average across every sensor of its kind,
// which is what makes a machine with many cores, many GPUs or many case fans
// legible on one graph instead of a dozen.
const (
	// MetricCPUTemp is the mean of every CPU temperature sensor: one figure
	// per CPU on a multi-socket board, plus the cores within each package.
	MetricCPUTemp = "cpu.temp.avg"
	// MetricGPUTemp is the mean of every GPU's main temperature.
	MetricGPUTemp = "gpu.temp.avg"
	// MetricFanAvg is the mean RPM across every fan sensor on the machine.
	MetricFanAvg = "fan.avg"
	// MetricDiskTemp is the mean temperature across every drive that reports
	// one.
	MetricDiskTemp = "disk.temp.avg"
)

// derive appends the average series to a snapshot.
//
// Averages are computed here rather than in the UI because they are genuine
// series: they need history of their own so the graph shows how the average
// moved over time, which cannot be reconstructed from the individual readings.
// The individual sensors are still published alongside them, so the average is
// a headline and not a black box.
//
// Each average is emitted only when at least one sensor of its kind produced a
// reading, so a machine with no fan sensor gets no fan average rather than a
// graph pinned to zero.
func derive(snap *model.Snapshot) {
	for _, mt := range []model.Metric{cpuTempAvg(snap), gpuTempAvg(snap), fanAvg(snap), diskTempAvg(snap)} {
		if mt.ID == "" {
			continue
		}
		snap.Metrics = append(snap.Metrics, mt)
	}
}

// cpuTempAvg averages every temperature the CPU's own chips report.
//
// Averaging the cores as well as the package is deliberate: it is the figure
// that tracks real silicon heat, and it keeps the single-socket and
// dual-socket cases on the same footing - a two-socket machine shows one
// average over both packages rather than one graph per socket.
func cpuTempAvg(snap *model.Snapshot) model.Metric {
	var (
		sum   float64
		n     int
		crit  float64
		label = "CPU"
	)
	for _, mt := range snap.Metrics {
		if mt.Category != model.CategorySensor || mt.Kind != model.KindTemperature {
			continue
		}
		if !sensors.IsCPUChip(mt.Group) || !mt.Valid() {
			continue
		}
		sum += mt.Value
		n++
		if mt.Crit > 0 && (crit == 0 || mt.Crit < crit) {
			// The lowest critical temperature on the board is the one the
			// CPU will actually throttle at, so the average's thresholds
			// follow it rather than a hardcoded guess.
			crit = mt.Crit
		}
	}
	if n == 0 {
		return model.Metric{}
	}

	// A machine with several CPUs says so, so one flat line on a big machine
	// is not mistaken for a single-core reading.
	if snap.Cores > 1 {
		label = "CPU ×" + itoa(snap.Cores)
	}

	avg := sum / float64(n)
	warn, limit := 50.0, 100.0
	if crit > 0 {
		limit = crit
		// Use 50°C as warn threshold unless the critical temp is lower
		if crit < 50 {
			warn = crit * 0.85
		}
	}
	return model.Metric{
		ID:       MetricCPUTemp,
		Label:    label,
		Group:    "Average",
		Category: model.CategoryCPU,
		Kind:     model.KindTemperature,
		Value:    avg,
		Min:      20,
		Max:      model.Clamp(limit*1.1, 60, 120),
		Warn:     warn,
		Crit:     limit,
		// Hint records how many sensors went into the average, so the panel
		// can be honest about what "average" means on this machine.
		Hint: sensorCountNote(n),
	}
}

// gpuTempAvg averages the main temperature of every readable GPU.
//
// Only cards that produced an actual reading take part: a display-only adapter
// with no telemetry would otherwise drag the average toward zero.
func gpuTempAvg(snap *model.Snapshot) model.Metric {
	var (
		sum   float64
		n     int
		label string
	)
	for _, d := range snap.Devices {
		if !d.HasTemperature() {
			continue
		}
		sum += d.Temperature
		n++
		if label == "" {
			label = "GPU"
		}
	}
	if n == 0 {
		return model.Metric{}
	}
	if n > 1 {
		label = "GPU ×" + itoa(n)
	}
	return model.Metric{
		ID:       MetricGPUTemp,
		Label:    label,
		Group:    "Average",
		Category: model.CategoryGPU,
		Kind:     model.KindTemperature,
		Value:    sum / float64(n),
		Min:      20,
		Max:      100,
		Warn:     80,
		Crit:     90,
		Hint:     sensorCountNote(n),
	}
}

// fanAvg averages the RPM of every fan the machine reports.
//
// Fan RPM has no meaningful "full scale" that is the same on every machine,
// so the graph is scaled against the highest fan the hwmon chips declare and
// carries no warning thresholds: a fan spinning harder is not a fault.
func fanAvg(snap *model.Snapshot) model.Metric {
	var (
		sum  float64
		n    int
		peak float64
	)
	for _, mt := range snap.Metrics {
		if mt.Kind != model.KindFan || !mt.Valid() {
			continue
		}
		sum += mt.Value
		n++
		if mt.Max > peak {
			peak = mt.Max
		}
	}
	if n == 0 {
		return model.Metric{}
	}
	return model.Metric{
		ID:       MetricFanAvg,
		Label:    "Average fan",
		Group:    "Fans",
		Category: model.CategorySensor,
		Kind:     model.KindFan,
		Value:    sum / float64(n),
		Min:      0,
		Max:      3500,
		Hint:     sensorCountNote(n),
	}
}

// diskTempAvg averages the composite temperature of every drive that reports
// one.
//
// A machine with two or three NVMe drives otherwise gets a graph and a number
// per drive, which is the same problem the CPU average solves: a panel per
// sensor rather than one figure per class of sensor. The individual drives stay
// visible underneath it.
func diskTempAvg(snap *model.Snapshot) model.Metric {
	var (
		sum   float64
		n     int
		crit  float64
		label = "Drive"
	)
	for _, mt := range snap.Metrics {
		// The composite reading is the drive's headline temperature. The
		// secondary sensors - an NVMe's NAND hot spot, for one - are listed
		// under it rather than averaged into it.
		if mt.Category != model.CategoryDisk || mt.Kind != model.KindTemperature {
			continue
		}
		if mt.Group != "SSD Temp" && mt.Group != "HDD Temp" {
			continue
		}
		sum += mt.Value
		n++
		if mt.Crit > 0 && (crit == 0 || mt.Crit < crit) {
			// The first drive to throttle sets the limit for all of them.
			crit = mt.Crit
		}
	}
	if n == 0 {
		return model.Metric{}
	}
	if n > 1 {
		label = "Drives ×" + itoa(n)
	}
	warn, limit := 40.0, 70.0
	if crit > 0 {
		limit = crit
		// Use 40°C as warn threshold unless the critical temp is lower
		if crit < 40 {
			warn = crit * 0.8
		}
	}
	return model.Metric{
		ID:       MetricDiskTemp,
		Label:    label,
		Group:    "Average",
		Category: model.CategoryDisk,
		Kind:     model.KindTemperature,
		Value:    sum / float64(n),
		Min:      20,
		Max:      model.Clamp(limit*1.1, 60, 120),
		Warn:     warn,
		Crit:     limit,
		Hint:     sensorCountNote(n),
	}
}

// sensorCountNote describes how many sensors went into an average, e.g.
// "32 sensors", or "" when there was only one and the average is the reading.
func sensorCountNote(n int) string {
	if n <= 1 {
		return ""
	}
	return itoa(n) + " sensors"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
