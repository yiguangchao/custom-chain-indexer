package main

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// 1. Gauge: Record which block is currently synchronized to
	// Gauge Used for numerical values that can be up or down
	MetricLastBlock = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_last_synced_block",
		Help: " The height of the last synced block",
	})

	// 2. Gauge: Record the latest height on the chain (used to calculate Lag)
	MetricChainHead = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "indexer_chain_head_block",
		Help: "The current height of the blockchain",
	})

	// 3. Counter: records the total number of transactions indexed
	// Counter only increases, not decreases
	MetricIndexedTxTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "indexer_indexed_tx_total",
		Help: "Total number of transactions indexed",
	})
)

// Initialize and register metrics
func InitMetrics() {
	prometheus.MustRegister(MetricLastBlock)
	prometheus.MustRegister(MetricChainHead)
	prometheus.MustRegister(MetricIndexedTxTotal)
}
