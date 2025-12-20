package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"os"

	"custom-chiain-indexer/token"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"gorm.io/gorm/clause"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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
	ID           string `gorm:"primaryKey"`
	LastBlockNum uint64
}

// Define a constant ID to mark our process in the database
const WorkerID = "usdt_worker"

func main() {
	// ---------------------------------------------------------
	// 1. Connect nodes (Connections)
	// ---------------------------------------------------------
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=password dbname=web3_indexer port=5432 sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("Database connection failed:", err)
	}
	// Auto Migration, similar to Hibernate's ddl auto
	db.AutoMigrate(&TransferLog{}, &SyncState{})

	// ---------------------------------------------------------
	// B. Connect blockchain nodes
	// ---------------------------------------------------------
	rpcUrl := "https://mainnet.infura.io/v3/96ca8d4da9ef40c29975cad96332357b"
	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		log.Fatal(err)
	}

	// 3. Prepare ABI
	contractAbi, _ := abi.JSON(strings.NewReader(token.Erc20ABI))
	contractAddress := common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")

	fmt.Println(">>> The indexer has started successfully and entered daemon mode...")

	// ---------------------------------------------------------
	// Enter while (Daemon Loop)
	// ---------------------------------------------------------
	ticker := time.NewTicker(3 * time.Second) // Check every 3 seconds
	defer ticker.Stop()

	for range ticker.C {
		// A. Get where the last synchronization was to (Local)
		var state SyncState
		// If there is no record in the database,
		// it means it is running for the first time and defaults to starting from 0
		if err := db.FirstOrCreate(&state, SyncState{ID: WorkerID, LastBlockNum: 18000000}).Error; err != nil {
			log.Printf("Get status failed: %v", err)
			continue
		}

		startBlock := state.LastBlockNum + 1

		// B. Obtain the latest height on the chain (Remote)
		header, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Printf("网络波动: %v", err)
			continue
		}
		chainHead := header.Number.Uint64()

		//C. Determine whether synchronization is necessary
		if startBlock > chainHead {
			fmt.Printf("\r>>> 已追平最新块 [%d]... 等待新块...", chainHead)
			continue
		}

		// D. Set the range of batch capture (Batch Size)
		// To prevent catching too many blocks at once and exceeding the time limit,
		// limit the maximum number of blocks caught at once to 10
		endBlock := startBlock + 10
		if endBlock > chainHead {
			endBlock = chainHead
		}

		fmt.Printf("\n>>> Synchronizing: %d -> %d (behind %d blocks)\n", startBlock,
			endBlock, chainHead-endBlock)

		// E. Execute capture and storage
		// Only proceed when processBatch returns nil (successful)
		err = processBatch(client, db, contractAddress, contractAbi, int64(startBlock), int64(endBlock))
		if err != nil {
			log.Println("This batch processing failed and will be retried in 3 seconds ..")
			continue
		}

		// F. Storage successful, update cursor
		state.LastBlockNum = endBlock
		db.Save(&state)
	}
}

// processBatch Responsible for capturing ->parsing ->storing
func processBatch(client *ethclient.Client, db *gorm.DB, address common.Address, contractAbi abi.ABI, from int64, to int64) error {
	// 1. Construct query conditions
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(from),
		ToBlock:   big.NewInt(to),
		Addresses: []common.Address{address},
	}

	// 2. Initiate RPC request (with timeout control)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logs, err := client.FilterLogs(ctx, query)
	if err != nil {
		log.Printf("RPC failed to retrieve logs[Block %d-%d]: %v", from, to, err)
		return err
	}

	if len(logs) == 0 {
		return nil
	}

	// 3. Prepare data container
	type TransferEventData struct {
		Value *big.Int
	}
	var dataList []TransferLog

	// 4. Traverse and parse
	for _, vLog := range logs {
		// Security check: Ensure TopicID matches Transfer event
		if len(vLog.Topics) < 3 || vLog.Topics[0].Hex() != contractAbi.Events["Transfer"].ID.Hex() {
			continue
		}

		// A. Analyze Data (non indexed field: Value)
		var eventData TransferEventData
		err := contractAbi.UnpackIntoInterface(&eventData, "Transfer", vLog.Data)
		if err != nil {
			log.Printf("Failed to parse data (Tx: %s): %v", vLog.TxHash.Hex(), err)
			continue
		}

		// B. Resolve Topics (index fields: From, To)
		fromAddr := common.HexToAddress(vLog.Topics[1].Hex()).Hex()
		toAddr := common.HexToAddress(vLog.Topics[2].Hex()).Hex()

		// C. Assemble the Model
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

	// 5. Batch Insert
	if len(dataList) > 0 {
		// OnConflict: If TxHash+LogIndex conflict， (DoNothing)
		err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&dataList).Error
		if err != nil {
			log.Printf("Database write failed: %v", err)
			return err
		}
		fmt.Printf("   -> Successfully stored %d transaction records\n", len(dataList))
	}

	return nil
}
