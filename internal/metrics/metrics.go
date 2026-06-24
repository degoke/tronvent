// Package metrics defines the Prometheus metrics for tronvent.
// Call Register() once from main before starting any goroutines.
package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// BlocksScanned is the total number of Tron blocks processed by the poller.
	BlocksScanned = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tron_scanner_blocks_scanned_total",
		Help: "Total number of Tron blocks scanned by the poller",
	})

	// MatchesFound is the total number of watched-address matches across all scans.
	MatchesFound = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tron_scanner_matches_total",
		Help: "Total number of watched-address matches detected",
	})

	// EventsPublished is the total number of events pushed to the Redis stream.
	EventsPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "tron_scanner_events_published_total",
		Help: "Total number of events published to the Redis tron:events stream",
	})

	// ScanErrors is the total number of errors during block scanning.
	ScanErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "tron_scanner_scan_errors_total",
		Help: "Total number of scan errors, partitioned by type (trx, trc20)",
	}, []string{"type"})

	// BlockScanDuration is a histogram of per-block scan duration in seconds.
	BlockScanDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "tron_scanner_block_scan_duration_seconds",
		Help:    "Duration in seconds to scan a single Tron block",
		Buckets: prometheus.DefBuckets,
	})

	// WatchlistSize is the current number of addresses in the in-memory hashset.
	WatchlistSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "tron_scanner_watchlist_size",
		Help: "Current number of Tron addresses in the in-memory watch hashset",
	})
)

// Register registers all metrics with the default Prometheus registry.
// Must be called once from main() before any metric is incremented.
func Register() {
	prometheus.MustRegister(
		BlocksScanned,
		MatchesFound,
		EventsPublished,
		ScanErrors,
		BlockScanDuration,
		WatchlistSize,
	)
}
