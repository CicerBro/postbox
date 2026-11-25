package httpapi

import (
	"context"
	"maps"
	"sync"
	"time"
)

const (
	recoveryStatsRefreshInterval = 30 * time.Second
	recoveryStatsMaxAge          = 5 * time.Minute
	recoveryStatsQueryTimeout    = 15 * time.Second
)

type campaignRecoveryStatsKey struct {
	originalUUID  string
	recoveryUUIDs string
}

type campaignRecoveryStatsCache struct {
	mu          sync.Mutex
	entries     map[campaignRecoveryStatsKey]*campaignRecoveryStatsEntry
	slots       chan struct{}
	nextCleanup time.Time
}

type campaignRecoveryStatsEntry struct {
	stats       campaignRecoveryStats
	updatedAt   time.Time
	nextRefresh time.Time
	pending     *campaignRecoveryStatsLoad
}

type campaignRecoveryStatsLoad struct {
	done  chan struct{}
	stats campaignRecoveryStats
	err   error
}

func (c *campaignRecoveryStatsCache) get(ctx context.Context, key campaignRecoveryStatsKey, query func(context.Context) (campaignRecoveryStats, error)) (campaignRecoveryStats, error) {
	if err := ctx.Err(); err != nil {
		return campaignRecoveryStats{}, err
	}
	c.mu.Lock()
	now := time.Now()
	if c.entries == nil {
		c.entries = make(map[campaignRecoveryStatsKey]*campaignRecoveryStatsEntry)
		c.slots = make(chan struct{}, 2)
	}
	if !now.Before(c.nextCleanup) {
		maps.DeleteFunc(c.entries, func(_ campaignRecoveryStatsKey, entry *campaignRecoveryStatsEntry) bool {
			return entry.pending == nil && now.Sub(entry.updatedAt) >= recoveryStatsMaxAge
		})
		c.nextCleanup = now.Add(recoveryStatsMaxAge)
	}
	entry := c.entries[key]
	if entry == nil {
		entry = &campaignRecoveryStatsEntry{}
		c.entries[key] = entry
	}
	usable := now.Sub(entry.updatedAt) < recoveryStatsMaxAge
	if entry.pending == nil && (!usable || !now.Before(entry.nextRefresh)) {
		entry.pending = &campaignRecoveryStatsLoad{done: make(chan struct{})}
		go c.refresh(entry, entry.pending, query)
	}
	stats, pending := entry.stats, entry.pending
	c.mu.Unlock()

	// The list can reuse recent stats while a new snapshot is computed.
	// Cold requests share the same query, even if one caller disconnects.
	if usable {
		return stats, nil
	}
	select {
	case <-ctx.Done():
		return campaignRecoveryStats{}, ctx.Err()
	case <-pending.done:
		return pending.stats, pending.err
	}
}

func (c *campaignRecoveryStatsCache) refresh(entry *campaignRecoveryStatsEntry, pending *campaignRecoveryStatsLoad, query func(context.Context) (campaignRecoveryStats, error)) {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryStatsQueryTimeout)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		pending.stats, pending.err = query(ctx)
		<-c.slots
	case <-ctx.Done():
		pending.err = ctx.Err()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if pending.err == nil {
		entry.stats = pending.stats
		entry.updatedAt = now
	}
	entry.nextRefresh = now.Add(recoveryStatsRefreshInterval)
	entry.pending = nil
	close(pending.done)
}
