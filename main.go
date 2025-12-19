package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"

	"os"

	"custom-chiain-indexer/token"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	db.AutoMigrate(&TransferLog{})

	// ---------------------------------------------------------
	// B. Connect blockchain nodes
	// ---------------------------------------------------------
	rpcUrl := "https://mainnet.infura.io/v3/96ca8d4da9ef40c29975cad96332357b"
	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		log.Fatal(err)
	}

	// USDT contract
	contractAddress := common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")

	// Check the last 10 blocks
	header, _ := client.HeaderByNumber(context.Background(), nil)
	blockNumber := header.Number.Int64()
	fromBlock := big.NewInt(blockNumber - 10)

	query := ethereum.FilterQuery{
		FromBlock: fromBlock,
		ToBlock:   nil,
		Addresses: []common.Address{contractAddress},
	}

	logs, err := client.FilterLogs(context.Background(), query)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(">>> Scanning completed, found %d logs\n", len(logs))

	// ---------------------------------------------------------
	// 3. Core: parsing data (Parsing)
	// ---------------------------------------------------------
	contractAbi, _ := abi.JSON(strings.NewReader(token.Erc20ABI))

	// Temporary structures are used to parse the Data section
	type TransferEventData struct {
		Value *big.Int
	}

	var dataList []TransferLog

	for _, vLog := range logs {
		// Verify Event ID (Transfer)
		if vLog.Topics[0].Hex() != contractAbi.Events["Transfer"].ID.Hex() {
			continue
		}

		// 1. Analyze Data (Amount)
		var eventData TransferEventData
		err := contractAbi.UnpackIntoInterface(&eventData, "Transfer", vLog.Data)
		if err != nil {
			log.Printf("Parsing failed: %v", err)
			continue
		}

		// 2. Analyzing Topics (From, To)
		from := common.HexToAddress(vLog.Topics[1].Hex()).Hex()
		to := common.HexToAddress(vLog.Topics[2].Hex()).Hex()

		// 3. Build Model Object
		logModel := TransferLog{
			TxHash:      vLog.TxHash.Hex(),
			BlockNumber: vLog.BlockNumber,
			LogIndex:    vLog.Index,
			FromAddress: from,
			ToAddress:   to,
			Amount:      eventData.Value.String(),
		}

		dataList = append(dataList, logModel)
	}

	// 4. Batch Insert
	if len(dataList) > 0 {
		// Clause (clause. OnConflict...) Similar to SQL's INSERTIGNORE or ON DUPLICATE KEY UPDATE
		// Prevent duplicate processing of the same block from causing database errors
		result := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&dataList)

		if result.Error != nil {
			log.Printf("Failed to put in storage: %v", result.Error)
		} else {
			fmt.Printf(">>> Successfully stored in the warehouse %d pieces of data！\n", result.RowsAffected)
		}
	}
}
