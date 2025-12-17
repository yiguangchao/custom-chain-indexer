package main

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	// 1. Connect to node
	client, err := ethclient.Dial("https://mainnet.infura.io/v3/96ca8d4da9ef40c29975cad96332357b")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Connection successful！")

	// 2. Define the contract address for monitoring (USDT contract address)
	contractAddress := common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")

	// 3. Define query scope
	header, _ := client.HeaderByNumber(context.Background(), nil)
	currentBlock := header.Number.Int64()
	fromBlock := big.NewInt(currentBlock - 10)

	query := ethereum.FilterQuery{
		FromBlock: fromBlock,
		ToBlock:   nil,
		Addresses: []common.Address{contractAddress},
	}

	// 4. Initiate a query
	logs, err := client.FilterLogs(context.Background(), query)
	if err != nil {
		log.Fatal(err)
	}

	// 5. Traverse the results
	fmt.Printf("Block %d to %d discovered between %d  USDT related transactions", fromBlock, currentBlock, len(logs))

	for _, vLog := range logs {
		fmt.Printf("Block: %d | transaction hash: %s | Index: %d\n", vLog.BlockNumber, vLog.TxHash.Hex(), vLog.Index)
	}
}
