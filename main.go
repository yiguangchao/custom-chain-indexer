package main

import (
	"context"
	"custom-chiain-indexer/token"
	"flag"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"custom-chiain-indexer/config"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"gorm.io/gorm/clause"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"sync"
)

// 1. Define database model (Entity)
// Corresponding to the transfer_Logs table in the database
type TransferLog struct {
	ID          uint   `gorm:"primaryKey"`
	TxHash      string `gorm:"type:char(66);uniqueIndex:idx_tx_log"`
	BlockNumber uint64
	LogIndex    uint   `gorm:"uniqueIndex:idx_tx_log"`
	FromAddress string `gorm:"type:char(42);index"`
	ToAddress   string `gorm:"type:char(42);index"`
	Amount      string `gorm:"type:varchar(78)"`
}

type SyncState struct {
	ID            string `gorm:"primaryKey"`
	LastBlockNum  uint64
	LastBlockHash string `gorm:"type:char(66)"`
}

// Define a constant ID to mark our process in the database
const WorkerID = "usdt_worker"

func main() {

	cfg := config.LoadConfig()

	// ==========================================
	// 1. Define command-line parameters
	// ==========================================
	backfillMode := flag.Bool("backfill", false, "Enable historical data backfilling mode")
	startBlock := flag.Int64("start", cfg.Chain.StartBlock, "Backfilling starting block")
	endBlock := flag.Int64("end", cfg.Chain.StartBlock+1000, "Backfilling completed block")
	workers := flag.Int("workers", 5, "Concurrent coroutine count (recommendation 3-10)")
	flag.Parse()

	// ---------------------------------------------------------
	// 1. Connect nodes (Connections)
	// ---------------------------------------------------------
	db, err := gorm.Open(postgres.Open(cfg.Database.Dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Database connection failed:", err)
	}
	db.AutoMigrate(&TransferLog{}, &SyncState{})

	InitMetrics()

	// ---------------------------------------------------------
	// B. Connect blockchain nodes
	// ---------------------------------------------------------
	client, err := ethclient.Dial(cfg.Chain.RpcUrl)
	if err != nil {
		log.Fatal("RPC connection failed:", err)
	}

	// 3. Prepare ABI
	contractAbi, _ := abi.JSON(strings.NewReader(token.Erc20ABI))
	contractAddress := common.HexToAddress(cfg.Chain.ContractAddress)

	fmt.Println(">>> The indexer has started successfully and entered daemon mode...")

	// ==========================================
	// 4. Mode diversion
	// ==========================================
	if *backfillMode {
		fmt.Printf("🚀 Activate backfill mode: Block %d -> %d | Workers: %d\n", *startBlock, *endBlock, *workers)
		startBackfill(client, db, contractAddress, contractAbi, *startBlock, *endBlock, *workers)
		return
	}

	// ==========================================
	// New: Starting API Server
	// ==========================================
	go StartServer(db, cfg.Server.Port)

	// ---------------------------------------------------------
	// Enter while (Daemon Loop)
	// ---------------------------------------------------------
	ticker := time.NewTicker(3 * time.Second) // Check every 3 seconds
	defer ticker.Stop()

	for range ticker.C {
		// A. Get local status
		var state SyncState
		if err := db.FirstOrCreate(&state, SyncState{ID: WorkerID, LastBlockNum: 18000000}).Error; err != nil {
			log.Printf("Reading status failed: %v", err)
			continue
		}

		// Target Block=Local Latest+1
		targetBlockNum := state.LastBlockNum + 1

		// B. Get the latest height on the chain (used to determine if it is tied)
		header, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			chainHead := header.Number.Uint64()
			MetricChainHead.Set(float64(chainHead))
		}
		chainHead := header.Number.Uint64()

		if targetBlockNum > chainHead {
			fmt.Printf("\r>>> Already equalized [%d]... Waiting for a new block...", chainHead)
			continue
		}

		// =========================================================
		// Core addition: Reorg security check
		// =========================================================
		if state.LastBlockHash != "" {
			// 1. Get the header of the new block (targetBlock) to be synchronized
			targetHeader, err := client.HeaderByNumber(context.Background(), big.NewInt(int64(targetBlockNum)))
			if err != nil {
				log.Printf("Failed to retrieve the header of the target block: %v", err)
				continue
			}

			// 2. Check whether the ParentHash of the new block is equal to the Hash in the database (LastBlockHash)
			if targetHeader.ParentHash.Hex() != state.LastBlockHash {
				// Not matching! The LastBlock in the database is no longer on the main chain
				log.Printf("Hash mismatch! Local: %s, on chain Parent: %s",
					state.LastBlockHash, targetHeader.ParentHash.Hex())

				// 3. Perform rollback
				if err := rollback(db, &state); err != nil {
					log.Printf("Rollback failed: %v", err)
				}
				continue // Skip this loop after rollback and restart
			}
		}
		// =========================================================

		// D. Batch capture range (Batch Size = 10)
		endBlock := targetBlockNum + 10
		if endBlock > chainHead {
			endBlock = chainHead
		}

		// E. Execute Capture
		lastBlockHash, err := processBatch(client, db, contractAddress, contractAbi, int64(targetBlockNum), int64(endBlock))
		if err != nil {
			log.Println("Processing failed, retry...")
			continue
		}

		// F. Update Status (Save Hash)
		state.LastBlockNum = endBlock
		state.LastBlockHash = lastBlockHash
		db.Save(&state)

		MetricLastBlock.Set(float64(endBlock))

		// G. [Monitoring] Update local altitude indicators
		fmt.Printf(" -> synchronously complete: %d (Hash: %s...)\n", endBlock, lastBlockHash[:10])
	}
}

// processBatch Responsible for capturing ->parsing ->storing
func processBatch(client *ethclient.Client, db *gorm.DB, address common.Address, contractAbi abi.ABI,
	from int64, to int64) (string, error) {
	// 1. Construct query conditions
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(from),
		ToBlock:   big.NewInt(to),
		Addresses: []common.Address{address},
	}

	// 2. Initiate RPC request
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		log.Printf("RPC Failed to retrieve logs [Block %d-%d]: %v", from, to, err)
		return "", err
	}

	// 3. Even if the logs are empty, it must still go down because the
	// function needs to obtain the hash of endBlock at the end
	if len(logs) > 0 {
		// --- Start parsing logic ---
		type TransferEventData struct {
			Value *big.Int
		}
		var dataList []TransferLog

		for _, vLog := range logs {
			if len(vLog.Topics) < 3 || vLog.Topics[0].Hex() != contractAbi.Events["Transfer"].ID.Hex() {
				continue
			}

			var eventData TransferEventData
			err := contractAbi.UnpackIntoInterface(&eventData, "Transfer", vLog.Data)
			if err != nil {
				continue
			}

			fromAddr := common.HexToAddress(vLog.Topics[1].Hex()).Hex()
			toAddr := common.HexToAddress(vLog.Topics[2].Hex()).Hex()

			logModel := TransferLog{
				TxHash:      vLog.TxHash.Hex(),
				BlockNumber: vLog.BlockNumber,
				LogIndex:    vLog.Index,
				FromAddress: fromAddr,
				ToAddress:   toAddr,
				Amount:      eventData.Value.String(),
			}

			dataList = append(dataList, logModel)
		}

		// Batch warehousing
		if len(dataList) > 0 {
			err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&dataList).Error
			if err != nil {
				log.Printf("Database write failed: %v", err)
				return "", err
			}
			fmt.Printf("   -> Successfully stored %d transaction records\n", len(dataList))

			// [Monitoring] Counter+N
			MetricIndexedTxTotal.Add(float64(len(dataList)))
		}
	}

	// 4. Obtain the hash of the last block in this batch
	header, err := client.HeaderByNumber(context.Background(), big.NewInt(to))
	if err != nil {
		log.Printf("Failed to obtain EndBlock Hash: %v", err)
		return "", err
	}

	return header.Hash().Hex(), nil
}

// Rollback a block: delete all logs at that height and roll back the state by 1
func rollback(db *gorm.DB, state *SyncState) error {
	blockNumToDelete := state.LastBlockNum
	log.Printf("Chain fork detected (Reorg)! Rolling back the block: %d", blockNumToDelete)

	return db.Transaction(func(tx *gorm.DB) error {
		// 1. Delete all logs (dirty data) at this height
		if err := tx.Where("block_number = ?", blockNumToDelete).Delete(&TransferLog{}).Error; err != nil {
			return err
		}

		// 2. Return the status to the previous block (Num-1)
		// Note: Simply leaving Hash empty here will trigger the check again in the next loop,
		state.LastBlockNum -= 1
		state.LastBlockHash = "" // Empty, force the next round to check the Parent on the chain again

		if err := tx.Save(state).Error; err != nil {
			return err
		}

		return nil
	})
}

// Job Define a job task: processing blocks from Start to End
type Job struct {
	From int64
	To   int64
}

func startBackfill(client *ethclient.Client, db *gorm.DB, address common.Address, abi abi.ABI, start int64, end int64, workerCount int) {
	// 1. Create task channel (with buffering to prevent blocking)
	jobs := make(chan Job, workerCount*2)

	// 2. Create a WaitGroup to ensure that all workers have completed their tasks before exiting
	var wg sync.WaitGroup

	// 3. Activate Workers
	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			workerLogic(id, client, db, address, abi, jobs)
		}(w)
	}

	// 4. Producer: responsible for distributing tasks
	// Allocate 100 blocks per time (Batch Size), which can be slightly larger for historical
	// backfilling compared to real-time 10 blocks
	batchSize := int64(100)
	for i := start; i < end; i += batchSize {
		currentEnd := i + batchSize - 1
		if currentEnd > end {
			currentEnd = end
		}

		// Throw the task into the channel
		jobs <- Job{From: i, To: currentEnd}
	}

	// 5. Task completed, close channel
	close(jobs)

	// 6. Waiting for all workers to finish work
	wg.Wait()
	fmt.Println("All historical data backfilling has been completed!")
}

// Specific work logic of workers
func workerLogic(id int, client *ethclient.Client, db *gorm.DB, address common.Address, abi abi.ABI, jobs <-chan Job) {
	for job := range jobs {
		fmt.Printf("[Worker %d] Processing: %d - %d\n", id, job.From, job.To)

		// Reuse the processBatch function!
		// Note: Backfilling does not require concern for Reorg (historical data is usually finalized),
		// nor does it require a return value Hash
		// So we need to add a simple retry mechanism to prevent network jitter from causing data loss in this area
		for retry := 0; retry < 3; retry++ {
			_, err := processBatch(client, db, address, abi, job.From, job.To)
			if err == nil {
				break
			}
			fmt.Printf("[Worker %d] Retry on failure (%d/3): %v\n", id, retry+1, err)
			time.Sleep(time.Second * 1)
		}
	}
}
