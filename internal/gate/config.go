// Package gate is the OS-independent scheduler core. It only sees numbers
// (memory, CPUs, samples) and a clock, so it runs against fakes in tests.
package gate

import (
	"math"
	"slices"
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

// Bounds of the reserve when it follows the machine's memory.
const (
	reserveShare = 0.06
	minReserveMB = 1024
	maxReserveMB = 4096
)

// ForMachine resolves the settings that depend on the machine. Without a
// configured reserve, it is 6% of total memory, between 1 GB and 4 GB: a
// bigger machine runs a bigger editor and browser, and a fixed gigabyte
// leaves the out-of-memory guard too little time there.
func (c Config) ForMachine(totalMB uint64) Config {
	c = c.Normalize()
	if c.ReserveMB == 0 {
		c.ReserveMB = min(max(uint64(float64(totalMB)*reserveShare), minReserveMB), maxReserveMB)
	}
	return c
}

// Slots computes the number of weight-1 jobs that may run at once:
// floor((total x fraction - reserve) / cost), at most half the CPU count
// (a heavy command keeps several cores busy) and never below 1. An explicit
// override wins.
func Slots(totalMB uint64, cpus int, c Config) int {
	c = c.ForMachine(totalMB)
	if c.Slots > 0 {
		return c.Slots
	}
	budget := float64(totalMB)*c.MemoryFraction - float64(c.ReserveMB)
	n := int(math.Floor(budget / float64(c.CostMB)))
	if limit := max(cpus/2, 1); cpus > 0 && n > limit {
		n = limit
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
// job from its recent peaks. Until MinSamples peaks exist, a prior from
// similar work (0 when there is none; see store.Store.Prior) or else the
// weight hint sets the floor; an explicit weight above 1 still counts.
// Afterwards a high percentile of the peaks decides rather than their
// maximum: costs swing between cold and warm runs, and one outlier should not
// over-reserve every later run. Running jobs' headroom and the per-job
// ceiling cover a run above it.
func Estimate(peaksMB []uint64, priorMB uint64, weight int, slots int, c Config) (estimateMB uint64, effectiveWeight int) {
	c = c.Normalize()
	weight = max(weight, 1)
	var peak uint64
	for _, p := range peaksMB {
		peak = max(peak, p)
	}
	hint := uint64(weight) * c.CostMB
	if priorMB > 0 {
		hint = priorMB
		if weight > 1 {
			hint = max(priorMB, uint64(weight)*c.CostMB)
		}
	}
	if len(peaksMB) < c.MinSamples {
		estimateMB = max(hint, peak)
	} else {
		estimateMB = percentile(peaksMB, estimatePercentile)
	}
	effectiveWeight = int((estimateMB + c.CostMB - 1) / c.CostMB)
	if len(peaksMB) < c.MinSamples {
		effectiveWeight = max(effectiveWeight, weight)
	}
	effectiveWeight = min(max(effectiveWeight, 1), max(slots, 1))
	return estimateMB, effectiveWeight
}

// estimatePercentile of a job's recent peaks is its estimate once learned.
const estimatePercentile = 0.9

// percentile by nearest rank: with 10 peaks, 0.9 is the second highest.
func percentile(values []uint64, p float64) uint64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[min(max(rank, 0), len(sorted)-1)]
}

// Workers returns the inner parallelism for a job holding weight of slots on
// a machine with cpus logical CPUs.
func Workers(cpus, weight, slots int) int {
	if slots <= 0 {
		return max(cpus, 1)
	}
	return max(cpus*weight/slots, 1)
}
