package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"custom-chiain-indexer/config"
	"custom-chiain-indexer/token"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==========================================
// 1. Database Models
// ==========================================

// TransferLog represents the ERC20 transfer event stored in DB
type TransferLog struct {
	ID          uint   `gorm:"primaryKey"`
	TxHash      string `gorm:"type:char(66);uniqueIndex:idx_tx_log"` // Part of composite unique index
	BlockNumber uint64
	LogIndex    uint   `gorm:"uniqueIndex:idx_tx_log"` // Part of composite unique index
	FromAddress string `gorm:"type:char(42);index"`
	ToAddress   string `gorm:"type:char(42);index"`
	Amount      string `gorm:"type:varchar(78)"` // Stored as string to prevent precision loss
}

// SyncState tracks the synchronization progress
type SyncState struct {
	ID            string `gorm:"primaryKey"`
	LastBlockNum  uint64
	LastBlockHash string `gorm:"type:char(66)"`
}

const WorkerID = "usdt_worker"

// ==========================================
// 2. Main Entry Point (The Commander)
// ==========================================

func main() {
	// A. Load Configuration
	cfg := config.LoadConfig()

	// B. Parse Command Line Flags
	// These allow overriding behavior at runtime, e.g., for backfilling data
	backfillMode := flag.Bool("backfill", false, "Enable historical data backfill mode")
	startBlock := flag.Int64("start", cfg.Chain.StartBlock, "Backfill start block (defaults to config)")
	endBlock := flag.Int64("end", cfg.Chain.StartBlock+1000, "Backfill end block")
	workers := flag.Int("workers", 5, "Number of concurrent workers for backfill")
	flag.Parse()

	// C. Initialize Database
	db, err := gorm.Open(postgres.Open(cfg.Database.Dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Failed to connect to Database:", err)
	}
	// Auto-migrate tables
	db.AutoMigrate(&TransferLog{}, &SyncState{})

	// D. Initialize RPC Client
	client, err := ethclient.Dial(cfg.Chain.RpcUrl)
	if err != nil {
		log.Fatal("Failed to connect to RPC:", err)
	}

	// E. Prepare Smart Contract ABI and Address
	contractAbi, _ := abi.JSON(strings.NewReader(token.Erc20ABI))
	contractAddress := common.HexToAddress(cfg.Chain.ContractAddress)

	// F. Mode Branching: Backfill Mode
	// If enabled, run the backfill engine and exit immediately after completion
	if *backfillMode {
		fmt.Printf("🚀 Starting Backfill Mode: Block %d -> %d | Workers: %d\n", *startBlock, *endBlock, *workers)
		startBackfill(client, db, contractAddress, contractAbi, *startBlock, *endBlock, *workers)
		return
	}

	// ==========================================
	// G. Realtime Indexer Mode (Daemon)
	// ==========================================

	// 1. Initialize Prometheus Metrics
	InitMetrics()

	// 2. Start API Server (in a non-blocking goroutine)
	srv := StartServer(db, cfg.Server.Port)

	// 3. Setup Graceful Shutdown
	// Create a channel to listen for OS signals (Ctrl+C, Docker Stop)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 4. Create a Context for the Indexer
	// This allows us to signal the worker loop to stop safely
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 5. Start the Indexer Logic in a separate Goroutine
	go StartIndexer(ctx, client, db, contractAddress, contractAbi)

	// 6. Block Main Thread until a signal is received
	<-quit
	log.Println("🛑 Shutdown signal received. Closing application safely...")

	// 7. Signal the Indexer to stop
	cancel()

	// 8. Shutdown API Server (Wait max 5 seconds for active requests)
	ctxServer, cancelServer := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelServer()

	if err := srv.Shutdown(ctxServer); err != nil {
		log.Fatal("API Server forced to shutdown:", err)
	}

	log.Println("✅ System exited successfully.")
}

// ==========================================
// 3. Realtime Indexer Logic
// ==========================================

// StartIndexer runs the main loop for data synchronization
func StartIndexer(ctx context.Context, client *ethclient.Client, db *gorm.DB,
	contractAddress common.Address, contractAbi abi.ABI) {
	fmt.Println(">>> Indexer started in Daemon Mode...")
	// Poll every 3 seconds (adjust based on block time)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Stop signal received from main()
			fmt.Println(">>> Indexer stopping after current batch...")
			return
		case <-ticker.C:
			// Execute business logic
			runSyncLogic(client, db, contractAddress, contractAbi)
		}
	}
}

func runSyncLogic(client *ethclient.Client, db *gorm.DB, address common.Address, contractAbi abi.ABI) {
	// A. Get Local State (Where did stop?)
	var state SyncState
	// If no state exists, start from default
	if err := db.FirstOrCreate(&state, SyncState{ID: WorkerID, LastBlockNum: 18000000}).Error; err != nil {
		log.Printf("Failed to read sync state: %v", err)
		return
	}

	targetBlockNum := state.LastBlockNum + 1

	// B. Get Remote Chain Head
	header, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		log.Printf("Network error (RPC): %v", err)
		return
	}
	chainHead := header.Number.Uint64()
	MetricChainHead.Set(float64(chainHead)) // Update Metric

	// Check if caught up
	if targetBlockNum > chainHead {
		fmt.Printf("\r>>> Synced [%d]... Waiting for new blocks...", chainHead)
		return
	}

	// C. Reorg Detection (Safety Check)
	// If the ParentHash of the new block matches the Hash of last synced block.
	if state.LastBlockHash != "" {
		targetHeader, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(targetBlockNum)))
		if err != nil {
			log.Printf("Failed to fetch target block header: %v", err)
			return
		}

		// If ParentHash mismatch -> Chain Reorg detected!
		if targetHeader.ParentHash.Hex() != state.LastBlockHash {
			log.Printf("⚠️ Hash Mismatch! Local: %s, Chain Parent: %s",
				state.LastBlockHash, targetHeader.ParentHash.Hex())
			if err := rollback(db, &state); err != nil {
				log.Printf("Rollback failed: %v", err)
			}
			return // Skip this cycle and try again after rollback
		}
	}

	// D. Determine Batch Size (Max 10 blocks per request)
	endBlock := targetBlockNum + 10
	if endBlock > chainHead {
		endBlock = chainHead
	}

	fmt.Printf("\n>>> Syncing: %d -> %d (Lag: %d)\n", targetBlockNum, endBlock, chainHead-endBlock)

	// E. Execute Fetch & Store
	lastBlockHash, err := processBatch(client, db, address, contractAbi, int64(targetBlockNum), int64(endBlock))
	if err != nil {
		log.Println("Batch processing failed, retrying...", err)
		return
	}

	// F. Update Sync State
	state.LastBlockNum = endBlock
	state.LastBlockHash = lastBlockHash
	db.Save(&state)

	MetricLastBlock.Set(float64(endBlock)) // Update Metric
}

// rollback handles chain reorgs by deleting the last synced block
func rollback(db *gorm.DB, state *SyncState) error {
	blockNumToDelete := state.LastBlockNum
	log.Printf("🚨 Reorg detected! Rolling back block: %d", blockNumToDelete)

	return db.Transaction(func(tx *gorm.DB) error {
		// 1. Delete logs for the invalid block
		if err := tx.Where("block_number = ?", blockNumToDelete).Delete(&TransferLog{}).Error; err != nil {
			return err
		}
		// 2. Revert state to previous block
		state.LastBlockNum -= 1
		state.LastBlockHash = "" // Clear hash to force a re-check next cycle
		return tx.Save(state).Error
	})
}

// processBatch fetches logs, parses them, and stores them in DB.
// Returns the Hash of the last processed block for state tracking.
func processBatch(client *ethclient.Client, db *gorm.DB, address common.Address, contractAbi abi.ABI,
	from int64, to int64) (string, error) {
	// 1. Construct Filter Query
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(from),
		ToBlock:   big.NewInt(to),
		Addresses: []common.Address{address},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 2. Call RPC
	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		return "", err
	}

	// 3. Parse Logs
	if len(logs) > 0 {
		type TransferEventData struct{ Value *big.Int }
		var dataList []TransferLog

		for _, vLog := range logs {
			// Verify Event Signature (Topic[0])
			if len(vLog.Topics) < 3 || vLog.Topics[0].Hex() != contractAbi.Events["Transfer"].ID.Hex() {
				continue
			}
			var eventData TransferEventData
			if err := contractAbi.UnpackIntoInterface(&eventData, "Transfer", vLog.Data); err != nil {
				continue
			}

			// Map to Model
			logModel := TransferLog{
				TxHash:      vLog.TxHash.Hex(),
				BlockNumber: vLog.BlockNumber,
				LogIndex:    vLog.Index,
				FromAddress: common.HexToAddress(vLog.Topics[1].Hex()).Hex(),
				ToAddress:   common.HexToAddress(vLog.Topics[2].Hex()).Hex(),
				Amount:      eventData.Value.String(),
			}
			dataList = append(dataList, logModel)
		}

		// 4. Batch Insert (Idempotent)
		if len(dataList) > 0 {
			// DoNothing on conflict ensures can re-run blocks safely
			err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&dataList).Error
			if err != nil {
				return "", err
			}
			fmt.Printf("   -> Successfully indexed %d records\n", len(dataList))
			MetricIndexedTxTotal.Add(float64(len(dataList))) // Update Metric
		}
	}

	// 5. Fetch EndBlock Header to get its Hash
	// This is required even if there were no logs in the batch
	header, err := client.HeaderByNumber(context.Background(), big.NewInt(to))
	if err != nil {
		return "", err
	}
	return header.Hash().Hex(), nil
}

// ==========================================
// 4. Backfill Engine (Producer-Consumer)
// ==========================================

type Job struct {
	From int64
	To   int64
}

func startBackfill(client *ethclient.Client, db *gorm.DB, address common.Address, abi abi.ABI, start int64,
	end int64, workerCount int) {
	jobs := make(chan Job, workerCount*2)
	var wg sync.WaitGroup

	// Start Consumers (Workers)
	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			workerLogic(id, client, db, address, abi, jobs)
		}(w)
	}

	// Producer: Generate Jobs
	batchSize := int64(100)
	for i := start; i < end; i += batchSize {
		currentEnd := i + batchSize - 1
		if currentEnd > end {
			currentEnd = end
		}
		jobs <- Job{From: i, To: currentEnd}
	}

	close(jobs) // Signal that no more jobs are coming
	wg.Wait()   // Wait for all workers to finish
	fmt.Println("✅ Historical backfill completed successfully!")
}

func workerLogic(id int, client *ethclient.Client, db *gorm.DB, address common.Address, abi abi.ABI, jobs <-chan Job) {
	for job := range jobs {
		fmt.Printf("[Worker %d] Processing: %d - %d\n", id, job.From, job.To)

		// Simple retry mechanism for network stability
		for retry := 0; retry < 3; retry++ {
			// Ignore the returned hash in backfill mode
			_, err := processBatch(client, db, address, abi, job.From, job.To)
			if err == nil {
				break
			}
			fmt.Printf("[Worker %d] Retry (%d/3): %v\n", id, retry+1, err)
			time.Sleep(time.Second * 1)
		}
	}
}
