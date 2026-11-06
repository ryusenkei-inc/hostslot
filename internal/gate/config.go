// Package gate is the OS-independent scheduler core. It only sees numbers
// (memory, CPUs, samples) and a clock, so it runs against fakes in tests.
package gate

import (
	"math"
	"time"
)

// Config holds the tunables of the scheduler. Zero fields fall back to the
// defaults in DefaultConfig when passed through Normalize.
type Config struct {
	// MemoryFraction is the share of total memory that heavy jobs may use.
	MemoryFraction float64 `json:"memoryFraction,omitempty"`
	// JobCeilingFraction is the share of total memory one job may use before
	// it is stopped as a runaway. 1 or more turns the ceiling off.
	JobCeilingFraction float64 `json:"jobCeilingFraction,omitempty"`
	// ReserveMB is kept free for the editor, browsers and agents themselves.
	ReserveMB uint64 `json:"reserveMB,omitempty"`
	// CostMB is the assumed peak memory of a weight-1 job without samples.
	CostMB uint64 `json:"costMB,omitempty"`
	// Slots overrides the computed number of slots when > 0.
	Slots int `json:"slots,omitempty"`
	// MinSamples is the number of samples after which the learned peak
	// replaces the weight hint.
	MinSamples int `json:"minSamples,omitempty"`
	// Warmup is how long a newly admitted job counts as not yet using its
	// memory, so its estimate is subtracted from the free memory reading.
	Warmup time.Duration `json:"-"`
}

// DefaultConfig returns the built-in defaults.
func DefaultConfig() Config {
	return Config{
		MemoryFraction:     0.6,
		JobCeilingFraction: 0.75,
		ReserveMB:          1024,
		CostMB:             2048,
		MinSamples:         3,
		Warmup:             15 * time.Second,
	}
}

// Normalize fills zero fields with defaults.
func (c Config) Normalize() Config {
	d := DefaultConfig()
	if c.MemoryFraction <= 0 || c.MemoryFraction > 1 {
		c.MemoryFraction = d.MemoryFraction
	}
	if c.JobCeilingFraction <= 0 {
		c.JobCeilingFraction = d.JobCeilingFraction
	}
	if c.ReserveMB == 0 {
		c.ReserveMB = d.ReserveMB
	}
	if c.CostMB == 0 {
		c.CostMB = d.CostMB
	}
	if c.MinSamples <= 0 {
		c.MinSamples = d.MinSamples
	}
	if c.Warmup <= 0 {
		c.Warmup = d.Warmup
	}
	return c
}

// Slots computes the number of weight-1 jobs that may run at once:
// floor((total x fraction - reserve) / cost), capped by the CPU count and
// never below 1. An explicit override wins.
func Slots(totalMB uint64, cpus int, c Config) int {
	c = c.Normalize()
	if c.Slots > 0 {
		return c.Slots
	}
	budget := float64(totalMB)*c.MemoryFraction - float64(c.ReserveMB)
	n := int(math.Floor(budget / float64(c.CostMB)))
	if cpus > 0 && n > cpus {
		n = cpus
	}
	return max(n, 1)
}

// Ceiling is the memory one job may use on a machine with totalMB, or 0
// when the ceiling is off.
func Ceiling(totalMB uint64, c Config) uint64 {
	c = c.Normalize()
	if c.JobCeilingFraction >= 1 {
		return 0
	}
	return uint64(float64(totalMB) * c.JobCeilingFraction)
}

// Estimate returns the expected peak memory and the effective weight of a
// job from its recent peaks. Until MinSamples peaks exist, the weight hint
// sets the floor; afterwards the learned peak decides.
func Estimate(peaksMB []uint64, weight int, slots int, c Config) (estimateMB uint64, effectiveWeight int) {
	c = c.Normalize()
	weight = max(weight, 1)
	var peak uint64
	for _, p := range peaksMB {
		peak = max(peak, p)
	}
	hint := uint64(weight) * c.CostMB
	if len(peaksMB) < c.MinSamples {
		estimateMB = max(hint, peak)
	} else {
		estimateMB = peak
	}
	effectiveWeight = int((estimateMB + c.CostMB - 1) / c.CostMB)
	if len(peaksMB) < c.MinSamples {
		effectiveWeight = max(effectiveWeight, weight)
	}
	effectiveWeight = min(max(effectiveWeight, 1), max(slots, 1))
	return estimateMB, effectiveWeight
}

// Workers returns the inner parallelism for a job holding weight of slots on
// a machine with cpus logical CPUs.
func Workers(cpus, weight, slots int) int {
	if slots <= 0 {
		return max(cpus, 1)
	}
	return max(cpus*weight/slots, 1)
}
