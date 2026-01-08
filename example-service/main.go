package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/transactrx/snowflake-cache/example-service/cache"
	"github.com/transactrx/snowflake-cache/example-service/comparator"
	"github.com/transactrx/snowflake-cache/example-service/config"
	"github.com/transactrx/snowflake-cache/example-service/reporter"
)

func main() {
	// Set up logger
	logger := log.New(os.Stdout, "[cache-compare] ", log.Ldate|log.Ltime|log.Lmicroseconds)

	// Load configuration
	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize reporter
	rep := reporter.NewReporter(logger, cfg.MaxDetailedMismatches)

	// Initialize cache manager
	cacheManager, err := cache.NewCacheManager(cfg, logger)
	if err != nil {
		logger.Fatalf("Failed to initialize caches: %v", err)
	}
	defer cacheManager.Close()

	// Set up graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	// Create comparison ticker
	ticker := time.NewTicker(cfg.ComparisonInterval)
	defer ticker.Stop()

	// Log startup
	rep.LogStartup(cfg.ComparisonInterval)

	// Run initial comparison
	runComparison(cacheManager, rep, cfg.RefreshBeforeCompare)

	// Main loop
	for {
		select {
		case <-ticker.C:
			runComparison(cacheManager, rep, cfg.RefreshBeforeCompare)

		case sig := <-shutdown:
			logger.Printf("Received signal %v, initiating shutdown...", sig)
			rep.LogShutdown()
			return
		}
	}
}

// runComparison performs a single comparison cycle.
func runComparison(cacheManager *cache.CacheManager, rep *reporter.Reporter, refreshBeforeCompare bool) {
	rep.LogComparisonStart()

	if refreshBeforeCompare {
		sfErr, pgErr := cacheManager.ForceRefreshBoth()

		// Track whether any refresh failed so we can:
		//   1. Log a structured error for each failing cache (Snowflake, Postgres, or both)
		//   2. Skip this comparison cycle entirely whenever at least one refresh fails.
		// This ensures we never emit a misleading ComparisonReport that is actually
		// caused by a refresh failure rather than a true data discrepancy.
		var hadError bool
		if sfErr != nil {
			rep.LogRefreshError("snowflake", sfErr)
			hadError = true
		}
		if pgErr != nil {
			rep.LogRefreshError("postgres", pgErr)
			hadError = true
		}
		if hadError {
			// At least one cache failed to refresh; do not attempt to compare data.
			return
		}
	}

	// Get data from both caches
	snowflakeData := cacheManager.SnowflakeCache.GetAll()
	postgresData := cacheManager.PostgresCache.GetAll()

	// Perform comparison
	report := comparator.Compare(snowflakeData, postgresData)

	// Log results
	rep.LogComparisonResult(report)
}
