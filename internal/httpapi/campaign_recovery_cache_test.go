package httpapi

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestCampaignRecoveryStatsRefreshDoesNotBlockList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var cache campaignRecoveryStatsCache
		key := campaignRecoveryStatsKey{originalUUID: "original", recoveryUUIDs: "recovery-1"}
		releaseRefresh := make(chan struct{})
		loads := 0
		query := func(ctx context.Context) (campaignRecoveryStats, error) {
			loads++
			if loads == 2 {
				select {
				case <-releaseRefresh:
				case <-ctx.Done():
					return campaignRecoveryStats{}, ctx.Err()
				}
			}
			return campaignRecoveryStats{Recipients: int64(loads)}, nil
		}
		checkRecipients := func(key campaignRecoveryStatsKey, want int64) {
			t.Helper()
			stats, err := cache.get(t.Context(), key, query)
			if err != nil || stats.Recipients != want {
				t.Fatalf("recipients = %d, error = %v, want %d recipients", stats.Recipients, err, want)
			}
		}

		checkRecipients(key, 1)
		time.Sleep(recoveryStatsRefreshInterval)
		checkRecipients(key, 1)
		checkRecipients(key, 1)
		synctest.Wait()
		if loads != 2 {
			t.Fatalf("started %d loads, want one initial lookup and one refresh", loads)
		}
		close(releaseRefresh)
		synctest.Wait()
		checkRecipients(key, 2)

		// Replacing a recovery send with another keeps the count of linked
		// sends the same, but the stored stats describe a different group.
		checkRecipients(campaignRecoveryStatsKey{originalUUID: "original", recoveryUUIDs: "recovery-2"}, 3)
	})
}
