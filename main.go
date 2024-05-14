package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/forta-network/go-multicall"
	"github.com/AlecAivazis/survey/v2"
)

var (
	BalancesByChain map[string][]BalanceData
	mutex           sync.Mutex
)

func getBalance(chain Chain, token Token, wallets []Wallet, wg *sync.WaitGroup) {
	defer wg.Done()

	caller, err := multicall.Dial(context.Background(), chain.RPC)
	if err != nil {
		log.Printf("Error creating caller for %s: %v\n", chain.RPC, err)
		return
	}

	var abi string
	var methodName string
	if (token.Symbol == "ETH" || token.Symbol == "MATIC" || token.Symbol == "BNB") { // native
		abi = ETH_ABI
		methodName = "getEthBalance"
	} else {
		abi = ERC20_ABI
		methodName = "balanceOf"
	}

	contract, err := multicall.NewContract(abi, token.Address)
	if err != nil {
		log.Printf("Error creating contract for %s: %v\n", token.Symbol, err)
		return
	}

	var calls []*multicall.Call
	for _, wallet := range wallets {
		calls = append(
			calls,
			contract.NewCall(
				new(balanceOutput),
				methodName,
				common.HexToAddress(wallet.Address),
			).Name(wallet.Address),
		)
	}

	walletsResults, err := caller.Call(nil, calls...)
	if err != nil {
		log.Printf("Error when calling %s contract method %s: %v\n", chain.Name, token.Symbol, err)
		return
	}

	mutex.Lock()
	defer mutex.Unlock()

	for i, walletResult := range walletsResults {
		balance := walletResult.Outputs.(*balanceOutput).Balance
		balanceFloat := new(big.Float).SetInt(balance)
		exp := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(token.Decimals)), nil)
		balanceFloat.Quo(balanceFloat, new(big.Float).SetInt(exp))

		if _, ok := BalancesByChain[chain.Name]; !ok {
			BalancesByChain[chain.Name] = make([]BalanceData, len(wallets))
		}

		BalancesByChain[chain.Name][i].Index = i
		BalancesByChain[chain.Name][i].Address = walletResult.CallName
		if BalancesByChain[chain.Name][i].Tokens == nil {
			BalancesByChain[chain.Name][i].Tokens = make(map[string]string)
		}
		BalancesByChain[chain.Name][i].Tokens[token.Symbol] = balanceFloat.Text('f', -1)
	}
}

func writeToCSV(chain Chain, balanceByChain []BalanceData) error {
	file, err := os.Create("results/" + chain.Name + ".csv")
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	headers := []string{"№", "Address"}
	for _, token := range chain.Tokens {
		headers = append(headers, token.Symbol)
	}
	if err := writer.Write(headers); err != nil {
		return err
	}

	for _, balance := range balanceByChain {
		record := make([]string, len(headers))
		record[0] = fmt.Sprintf("%d", balance.Index)
		record[1] = balance.Address

		for j, tokenSymbol := range headers[2:] {
			record[j+2] = balance.Tokens[tokenSymbol]
		}

		if err := writer.Write(record); err != nil {
			return err
		}
	}

	fmt.Printf("The results of %d %s were successfully written to results/%s.csv\n", len(balanceByChain), chain.Name, chain.Name)
	return nil
}

func main() {
	chains, err := readChainsFromConfig("config.yaml")
	if err != nil {
		log.Fatalf("Error when reading networks from config: %v\n", err)
	}
	wallets, err := readWalletsFromFile("wallets.txt")
	if err != nil {
		log.Fatalf("Error reading wallets from file: %v\n", err)
	}

	fmt.Printf("Found %d networks and %d wallets\n", len(chains), len(wallets))

	if _, err := os.Stat("results"); os.IsNotExist(err) {
        os.Mkdir("results", 0755)
    }

	var selectedChain string
	options := []string{"All"}
	for _, chain := range chains {
		options = append(options, chain.Name)
	}

	prompt := &survey.Select{
		Message: "Select network:",
		Options: options,
	}
	err = survey.AskOne(prompt, &selectedChain)
	if err != nil {
		log.Fatalf("Error when selecting network: %v\n", err)
	}
	var chainsToProcess []Chain
	if selectedChain == "All" {
		chainsToProcess = chains
	} else {
		for _, chain := range chains {
			if chain.Name == selectedChain {
				chainsToProcess = append(chainsToProcess, chain)
				break
			}
		}
	}

	BalancesByChain = make(map[string][]BalanceData)
	mutex = sync.Mutex{}

	startTime := time.Now()
	var wg sync.WaitGroup
	for _, chain := range chainsToProcess {
		for _, token := range chain.Tokens {
			wg.Add(1)
			go getBalance(chain, token, wallets, &wg)
		}
	}
	wg.Wait()

	for _, chain := range chainsToProcess {
		if err := writeToCSV(chain, BalancesByChain[chain.Name]); err != nil {
			log.Fatalf("Error writing to CSV file: %v\n", err)
		}
	}

	fmt.Printf("Lead time: %s\n", time.Since(startTime))
}
