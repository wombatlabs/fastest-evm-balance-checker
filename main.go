package main

import (
    "context"
    "encoding/csv"
    "encoding/hex"
    "flag"
    "fmt"
    "log"
    "math"
    "math/big"
    "os"
    "sync"
	"sync/atomic"
    "time"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/crypto"
    "github.com/ethereum/go-ethereum/ethclient"
    "github.com/forta-network/go-multicall"
    "github.com/AlecAivazis/survey/v2"
    "github.com/tyler-smith/go-bip39"
    "github.com/miguelmota/go-ethereum-hdwallet"
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

// writeToCSV writes only those entries which have at least one token balance > 0
func writeToCSV(chain Chain, balanceByChain []BalanceData) error {
    // ensure results/ exists
    if _, err := os.Stat("results"); os.IsNotExist(err) {
        if err := os.Mkdir("results", 0755); err != nil {
            return err
        }
    }

    file, err := os.Create("results/" + chain.Name + ".csv")
    if err != nil {
        return err
    }
    defer file.Close()

    writer := csv.NewWriter(file)
    defer writer.Flush()

    // build headers
    headers := []string{"№", "Address"}
    for _, token := range chain.Tokens {
        headers = append(headers, token.Symbol)
    }
    if err := writer.Write(headers); err != nil {
        return err
    }

    nonZeroCount := 0
    zero := big.NewFloat(0)

    for _, balData := range balanceByChain {
        hasBalance := false
        // check each token balance
        for _, sym := range headers[2:] {
            raw := balData.Tokens[sym]
            f, ok := new(big.Float).SetString(raw)
            if !ok || f == nil {
                // failed parse → treat as zero
                f = zero
            }
            if f.Cmp(zero) > 0 {
                hasBalance = true
                break
            }
        }
        if !hasBalance {
            continue
        }

        // count and write the row
        nonZeroCount++
        record := make([]string, len(headers))
        record[0] = fmt.Sprintf("%d", balData.Index)
        record[1] = balData.Address
        for i, sym := range headers[2:] {
            record[i+2] = balData.Tokens[sym]
        }
        if err := writer.Write(record); err != nil {
            return err
        }
    }

    fmt.Printf("The results of %d non-zero entries for %s were written to results/%s.csv\n",
        nonZeroCount, chain.Name, chain.Name,
    )
    return nil
}

func main() {
    // ─── 0) Parse flags ─────────────────────────────────────────────────────────
    var generateMode bool
    var mnemonicMode bool
    flag.BoolVar(&generateMode, "generate", false,
        "generate random private keys (or mnemonics with --mnemonic) and check balances")
    flag.BoolVar(&mnemonicMode, "mnemonic", false,
        "when --generate is set, produce BIP-39 mnemonics instead of raw private keys")
    flag.Parse()

    // ─── 1) Load config & wallets ───────────────────────────────────────────────
    chains, err := readChainsFromConfig("config.yaml")
    if err != nil {
        log.Fatalf("Error when reading networks from config: %v\n", err)
    }
    wallets, err := readWalletsFromFile("wallets.txt")
    if err != nil {
        log.Fatalf("Error reading wallets from file: %v\n", err)
    }

    fmt.Printf("Found %d networks and %d wallets\n", len(chains), len(wallets))

    // ─── 2) Ensure results directory ────────────────────────────────────────────
    if _, err := os.Stat("results"); os.IsNotExist(err) {
        if err := os.Mkdir("results", 0755); err != nil {
            log.Fatalf("Could not create results/: %v\n", err)
        }
    }

    // ─── 3) Network selection (your existing survey code) ───────────────────────
    var selectedChain string
    options := []string{"All"}
    for _, chain := range chains {
        options = append(options, chain.Name)
    }
    prompt := &survey.Select{
        Message: "Select network:",
        Options: options,
    }
    if err := survey.AskOne(prompt, &selectedChain); err != nil {
        log.Fatalf("Error when selecting network: %v\n", err)
    }

    var chainsToProcess []Chain
    if selectedChain == "All" {
        chainsToProcess = chains
    } else {
        for _, chain := range chains {
            if chain.Name == selectedChain {
                chainsToProcess = append(chainsToProcess, chain)
            }
        }
    }

    // ─── 4) Generate Mode ────────────────────────────────────────────────────────
    if generateMode {
        for _, chain := range chainsToProcess {
            go generateAndCheck(chain, mnemonicMode)
        }
        // block forever so our generators keep running
        select {}
    }

    // ─── 5) Batch Mode (original multicall flow) ────────────────────────────────
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
            log.Fatalf("Error writing to CSV for %s: %v\n", chain.Name, err)
        }
    }

    fmt.Printf("Lead time: %s\n", time.Since(startTime))
}

func generateAndCheck(chain Chain, mnemonicMode bool) {
    client, err := ethclient.Dial(chain.RPC)
    if err != nil {
        log.Fatalf("Failed to connect to %s RPC: %v", chain.Name, err)
    }
    defer client.Close()

    // Banner
    mode := "raw key mode"
    if mnemonicMode {
        mode = "mnemonic mode"
    }
    fmt.Printf("🔄 [Generator] %s starting on %s (%s)\n", time.Now().Format(time.RFC3339), chain.Name, mode)

    // Counter for addresses checked
    var checked uint64

    // Ticker: every second, print and reset the counter
    go func() {
        ticker := time.NewTicker(time.Second)
        defer ticker.Stop()
        for range ticker.C {
            c := atomic.SwapUint64(&checked, 0)
            fmt.Printf("⌛ %s: checked %d addr/sec\n", chain.Name, c)
        }
    }()

    for {
        atomic.AddUint64(&checked, 1)

        var addr common.Address
        var privKeyHex, mn string

        if mnemonicMode {
            // BIP-39 entropy + mnemonic
            entropy, err := bip39.NewEntropy(128)
            if err != nil {
                log.Printf("mnemonic entropy err: %v", err)
                continue
            }
            m, err := bip39.NewMnemonic(entropy)
            if err != nil {
                log.Printf("mnemonic err: %v", err)
                continue
            }
            wallet, err := hdwallet.NewFromMnemonic(m)
            if err != nil {
                log.Printf("hdwallet err: %v", err)
                continue
            }
            path := hdwallet.MustParseDerivationPath("m/44'/60'/0'/0/0")
            account, err := wallet.Derive(path, false)
            if err != nil {
                log.Printf("derive err: %v", err)
                continue
            }
            priv, err := wallet.PrivateKeyHex(account)
            if err != nil {
                log.Printf("privkey err: %v", err)
                continue
            }
            mn = m
            privKeyHex = priv
            addr = common.HexToAddress(account.Address.Hex())
        } else {
            key, err := crypto.GenerateKey()
            if err != nil {
                log.Printf("keygen err: %v", err)
                continue
            }
            pkBytes := crypto.FromECDSA(key)
            privKeyHex = hex.EncodeToString(pkBytes)
            addr = crypto.PubkeyToAddress(key.PublicKey)
        }

        balWei, err := client.BalanceAt(context.Background(), addr, nil)
        if err != nil {
            log.Printf("balance err: %v", err)
            continue
        }

        if balWei.Cmp(big.NewInt(0)) > 0 {
            balEth := new(big.Float).Quo(new(big.Float).SetInt(balWei),
                big.NewFloat(math.Pow10(18)))
            fmt.Printf("\n🎉 [FOUND] %s\n", chain.Name)
            fmt.Printf("    Address:     %s\n", addr.Hex())
            if mnemonicMode {
                fmt.Printf("    Mnemonic:    %s\n", mn)
            }
            fmt.Printf("    Private key: %s\n", privKeyHex)
            fmt.Printf("    Balance:     %s %s\n\n", balEth.Text('f', 8), chain.Tokens[0].Symbol)
        }
        // no throttle; add time.Sleep(time.Millisecond*10) if you want to slow it down
		time.Sleep(time.Millisecond*10)
    }
}