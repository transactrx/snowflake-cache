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
	runComparison(cacheManager, rep)

	// Main loop
	for {
		select {
		case <-ticker.C:
			runComparison(cacheManager, rep)

		case sig := <-shutdown:
			logger.Printf("Received signal %v, initiating shutdown...", sig)
			rep.LogShutdown()
			return
		}
	}
}

// runComparison performs a single comparison cycle.
func runComparison(cacheManager *cache.CacheManager, rep *reporter.Reporter) {
	rep.LogComparisonStart()

	// Force refresh both caches before comparison
	sfErr, pgErr := cacheManager.ForceRefreshBoth()

	// If either refresh fails, log and skip this comparison
	if sfErr != nil {
		rep.LogRefreshError("snowflake", sfErr)
		return
	}
	if pgErr != nil {
		rep.LogRefreshError("postgres", pgErr)
		return
	}

	// Get data from both caches
	snowflakeData := cacheManager.SnowflakeCache.GetAll()
	postgresData := cacheManager.PostgresCache.GetAll()

	// Perform comparison
	report := comparator.Compare(snowflakeData, postgresData)

	// Log results
	rep.LogComparisonResult(report)
}

