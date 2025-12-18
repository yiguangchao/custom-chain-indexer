package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"

	"custom-chiain-indexer/token"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	// ---------------------------------------------------------
	// 1. Connect nodes (Connections)
	// ---------------------------------------------------------
	rpcUrl := "https://mainnet.infura.io/v3/96ca8d4da9ef40c29975cad96332357b"
	client, err := ethclient.Dial(rpcUrl)
	if err != nil {
		log.Fatalf("Unable to connect to node: %v", err)
	}
	fmt.Println(">>> Successfully connected to the Ethereum network")

	// ---------------------------------------------------------
	// 2. Set filtering criteria (Filter)
	// ---------------------------------------------------------
	// USDT contract address
	contractAddress := common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")

	// Get the latest block number and look up 10 blocks ahead
	header, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		log.Fatal(err)
	}
	blockNumber := header.Number.Int64()
	fromBlock := big.NewInt(blockNumber - 10)

	query := ethereum.FilterQuery{
		FromBlock: fromBlock,
		ToBlock:   nil,
		Addresses: []common.Address{contractAddress},
	}

	fmt.Printf(">>> Start scanning blocks: %d to %d ...\n", fromBlock, blockNumber)
	logs, err := client.FilterLogs(context.Background(), query)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf(">>> Scanning completed, found %d logs\n", len(logs))

	contractAbi, err := abi.JSON(strings.NewReader(token.Erc20ABI))
	if err != nil {
		log.Fatal("Invalid ABI:", err)
	}

	type TransferEvent struct {
		Value *big.Int
	}

	fmt.Println("\n>>> Start parsing detailed data:")
	fmt.Println("--------------------------------------------------")

	for _, vLog := range logs {
		if vLog.Topics[0].Hex() != contractAbi.Events["Transfer"].ID.Hex() {
			continue
		}

		var event TransferEvent
		err := contractAbi.UnpackIntoInterface(&event, "Transfer", vLog.Data)
		if err != nil {
			log.Printf("Failed to parse data: %v", err)
			continue
		}

		from := common.HexToAddress(vLog.Topics[1].Hex())
		to := common.HexToAddress(vLog.Topics[2].Hex())

		fmt.Printf(
			"Blocks: %d | TX: %s\n   from: %s\n   to: %s\n   amount: %s (unit)\n",
			vLog.BlockNumber,
			vLog.TxHash.Hex(),
			from.Hex(),
			to.Hex(),
			event.Value.String(),
		)
		fmt.Println("--------------------------------------------------")
	}
}
