package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"log"
	"math/big"
	"strconv"
	"time"

	storage "github.com/librescan-org/backend-db"
	"github.com/librescan-org/backend-db/database"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

func startScraper(ctx context.Context, client *ethclient.Client) {
	var chainId *big.Int
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) (err error) {
		chainId, err = client.ChainID(ctx)
		return
	}, "ChainID")

	// Get the most recent archived block's number from the database, to continue scraping from where we left off.
	var repo storage.Storage = database.LoadRepository()

	var latestStoredBlockNumber *storage.BlockNumber
	retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
		latestStoredBlockNumber, err = repo.GetLatestBlockNumber()
		return
	}, "latestArchivedBlockNumber: GetLatestBlockNumber")

	parsedDebugBlockNumber, err := strconv.ParseUint(env_DEBUG_START_AT_BLOCK_NUMBER, 10, 64)
	var nextBlockNumberToScrape uint64
	if err == nil {
		nextBlockNumberToScrape = parsedDebugBlockNumber
	} else if latestStoredBlockNumber != nil {
		nextBlockNumberToScrape = *latestStoredBlockNumber + 1
	}

	// This is an edge case handler flag since genesis block has no parent block.
	checkParentBlock := nextBlockNumberToScrape != 0

	// Get the latest block number for our initial sync reference.
	lastKnownBlockHeight := mustGetMostRecentBlockNumber(ctx, client)

	// Main program loop.
	for {
		// We only need to update the block number iterator when we run out of known existing block numbers.
		if nextBlockNumberToScrape > lastKnownBlockHeight {
			// Keep checking for new block(s).
			for {
				lastKnownBlockHeight = mustGetMostRecentBlockNumber(ctx, client)
				if nextBlockNumberToScrape <= lastKnownBlockHeight {
					break
				}

				// No new blocks exist, slow down the request frequency.
				time.Sleep(time.Second)
			}
		}
		blockToStore := mustGetLatestBlockByNumber(ctx, client, nextBlockNumberToScrape)
		var previouslyStoredBlock *storage.Block
		if checkParentBlock {
			retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
				previouslyStoredBlock, err = repo.GetBlockByNumber(nextBlockNumberToScrape - 1)
				return
			}, "previouslyArchivedBlock: GetBlockByNumber")
		} else {
			checkParentBlock = true
		}

		// If the block we want to scrape is a child of the previously stored block, it is safe to scrape.
		// Otherwise, chain reorganization happened, which means we must find out how many blocks need to be
		// replaced in the database, and perform the replacement.
		if previouslyStoredBlock == nil || blockToStore.ParentHash() == previouslyStoredBlock.Hash {
			scrapeBlock(ctx, chainId.Uint64(), client, repo, blockToStore)
		} else {
			// Start by initializing the list with the latest known reorganized block as the first element.
			reorganizedRpcBlocks := []*types.Block{blockToStore}

			// Start collecting reorganized blocks in descending order, starting from the block before the latest known reorganized block (first block already in the list).
			previousPotentiallyReorganizedBlockNumber := blockToStore.NumberU64() - 1

			// Collect all reorganized blocks.
			for {

				// Retrieve two blocks at the same height, from different sources, to compare their hashes:
				// - the currently known actual block from RPC
				// - the block stored in the database
				previousPotentiallyReorganizedBlockFromRpc := mustGetLatestBlockByNumber(ctx, client, previousPotentiallyReorganizedBlockNumber)
				var previousPotentiallyReorganizedBlockFromDatabase *storage.Block
				retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
					previousPotentiallyReorganizedBlockFromDatabase, err = repo.GetBlockByNumber(previousPotentiallyReorganizedBlockNumber)
					return
				}, "blockFromDatabase: GetBlockByNumber")

				// If both block hashes are equal, we have run out of reorganized blocks to collect.
				if previousPotentiallyReorganizedBlockFromDatabase.Hash == previousPotentiallyReorganizedBlockFromRpc.Hash() {
					break
				}

				// Theoretically a "reorgception" is possible albeit extremely unlikely.
				// This means that a chain reorganization happens while we are in the process of reacting to the currently known reorganization.
				if reorganizedRpcBlocks[len(reorganizedRpcBlocks)-1].ParentHash() != previousPotentiallyReorganizedBlockFromRpc.Hash() {
					log.Fatal("unhandled edge case: overlapping reorganization")
				}

				// Keep collecting the reorganized blocks in descending order
				reorganizedRpcBlocks = append(reorganizedRpcBlocks, previousPotentiallyReorganizedBlockFromRpc)

				// We have to process the blocks in descending order to find the latest stable block (not affected by the reorganization).
				previousPotentiallyReorganizedBlockNumber--
			}

			// All the orphan blocks were identified and collected. Prepare the list of orphan block numbers to delete from the database.
			blockNumbersToDelete := make([]storage.BlockNumber, 0, len(reorganizedRpcBlocks)-1)
			for i, block := range reorganizedRpcBlocks {
				if i == 0 {
					// The first block in the list is not part of the database, skip it.
					continue
				}
				blockNumbersToDelete = append(blockNumbersToDelete, block.NumberU64())
			}

			// Delete all orphan blocks and their references from the database.
			retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
				err := repo.DeleteBlockAndAllReferences(blockNumbersToDelete...)
				if err != nil {
					return err
				}
				return repo.Commit(ctx)
			}, "DeleteBlockAndAllBlockIdReferences")

			// Complete the reorganization handling by storing the reorganized blocks in the database.
			for i := len(reorganizedRpcBlocks) - 1; i >= 0; i-- {
				scrapeBlock(ctx, chainId.Uint64(), client, repo, reorganizedRpcBlocks[i])
			}
		}
		nextBlockNumberToScrape++
	}
}

func mustGetMostRecentBlockNumber(ctx context.Context, client *ethclient.Client) (mostRecentBlockNumber storage.BlockNumber) {
	var err error
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
		mostRecentBlockNumber, err = client.BlockNumber(ctx)
		return err
	}, "getMostRecentBlockNumber: BlockNumber")
	return
}

func mustGetLatestBlockByNumber(ctx context.Context, client *ethclient.Client, number uint64) (block *types.Block) {
	var err error
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
		block, err = client.BlockByNumber(ctx, new(big.Int).SetUint64(number))
		return err
	}, "getBlockByNumber: BlockByNumber")
	return
}

func scrapeBlock(ctx context.Context, chainId uint64, client *ethclient.Client, repo storage.Storage, block *types.Block) {
	blockNumber := block.NumberU64()
	var err error
	addressIdToEtherBalance := make(map[storage.AddressId]*big.Int)
	addressIdToErc20TokenBalanceDeltas := make(map[storage.AddressId]map[storage.AddressId]*big.Int)
	if blockNumber == 0 {
		genesisAllocMap := loadGenesisConfig(ctx, repo).Alloc
		genesisAddresses := make([]common.Address, 0, len(genesisAllocMap))
		for genesisAddress := range genesisAllocMap {
			genesisAddresses = append(genesisAddresses, genesisAddress)
		}
		var addressIds []storage.AddressId
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			addressIds, err = repo.StoreAddress(genesisAddresses...)
			return err
		}, "genesis block: StoreAddress")
		for addressIdIndex, genesisAddress := range genesisAddresses {
			addressIdToEtherBalance[addressIds[addressIdIndex]] = genesisAllocMap[genesisAddress].Balance
		}
	}
	blockHeader := block.Header()
	mainBlockMiner := getMiner(ctx, chainId, client, blockHeader)
	var blockReceipts []*types.Receipt
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
		blockReceipts, err = client.BlockReceipts(ctx, rpc.BlockNumberOrHashWithNumber(rpc.BlockNumber(blockNumber)))
		return err
	}, "BlockReceipts")
	rewardsByMiner := make(map[common.Address]*big.Int)
	rewardsByUncleBlock := make(map[common.Hash]*big.Int)
	if !*isPoaChain && blockNumber != 0 {
		rewardsByMiner, rewardsByUncleBlock = getBlockRewards(ctx, chainId, client, block, blockReceipts)
	}
	retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
		return repo.StoreBlock(&storage.Block{
			Hash:            blockHeader.Hash(),
			Number:          blockHeader.Number.Uint64(),
			Nonce:           block.Nonce(),
			Sha3Uncles:      block.UncleHash(),
			LogsBloom:       blockHeader.Bloom,
			StateRoot:       blockHeader.Root,
			MinerAddressId:  getAddressId(ctx, repo, mainBlockMiner),
			Difficulty:      blockHeader.Difficulty,
			TotalDifficulty: calculateTotalDifficulty(ctx, repo, blockHeader.Difficulty),
			Size:            block.Size(),
			ExtraData:       blockHeader.Extra,
			GasLimit:        blockHeader.GasLimit,
			GasUsed:         blockHeader.GasUsed,
			Timestamp:       blockHeader.Time,
			BaseFeePerGas:   blockHeader.BaseFee,
			MixHash:         blockHeader.MixDigest,
			StaticReward:    rewardsByMiner[mainBlockMiner],
		})
	}, "StoreBlock")
	uncles := block.Uncles()
	if blockNumber == 0 && len(uncles) != 0 {
		log.Fatal("genesis block cannot have uncle(s)")
	}
	for i, uncle := range uncles {
		uncleMiner := uncle.Coinbase
		retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
			err = repo.StoreUncle(&storage.Uncle{
				Position:       uint8(i),
				Hash:           uncle.Hash(),
				UncleHeight:    uncle.Number.Uint64(),
				BlockHeight:    blockNumber,
				ParentHash:     uncle.ParentHash,
				MinerAddressId: getAddressId(ctx, repo, uncleMiner),
				Difficulty:     uncle.Difficulty,
				GasLimit:       uncle.GasLimit,
				GasUsed:        uncle.GasUsed,
				Timestamp:      uncle.Time,
				Reward:         rewardsByUncleBlock[uncle.Hash()],
			})
			return
		}, "StoreUncle")
	}
	transactions := block.Transactions()
	for transactionIndex, tx := range transactions {
		receipt := blockReceipts[transactionIndex]
		if receipt.TransactionIndex != uint(transactionIndex) {
			panic("receipts are not ordered")
		}
		txTo := tx.To()
		var txToAddressId *storage.AddressId
		if txTo != nil {
			id := getAddressId(ctx, repo, *txTo)
			txToAddressId = &id
		}
		var transactionIds []storage.TransactionId
		txHash := tx.Hash()
		txFrom, err := client.TransactionSender(ctx, tx, blockHeader.Hash(), receipt.TransactionIndex)
		if err != nil {
			log.Fatal(err)
		}
		txFromAddressId := getAddressId(ctx, repo, txFrom)
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			transactionIds, err = repo.StoreTransaction(&storage.Transaction{
				BlockNumber:   blockNumber,
				Hash:          txHash,
				Nonce:         tx.Nonce(),
				Index:         uint64(transactionIndex),
				FromAddressId: txFromAddressId,
				ToAddressId:   txToAddressId,
				Value:         tx.Value(),
				Gas:           tx.Gas(),
				GasPrice:      tx.GasPrice(),
				GasTipCap:     tx.GasTipCap(),
				GasFeeCap:     tx.GasFeeCap(),
				Input:         tx.Data(),
				Type:          tx.Type(),
			})
			return err
		}, "StoreTransaction")

		accessList := tx.AccessList()
		var storageKeys []*storage.StorageKey
		for _, accessTuple := range accessList {
			for _, storageKey := range accessTuple.StorageKeys {
				storageKeys = append(storageKeys, &storage.StorageKey{
					TransactionId: transactionIds[0],
					AddressId:     getAddressId(ctx, repo, accessTuple.Address),
					StorageKey:    new(big.Int).SetBytes(storageKey[:]),
				})
			}
		}
		if len(storageKeys) != 0 {
			retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
				return repo.StoreStorageKey(storageKeys...)
			}, "StoreStorageKey")
		}

		var contractAddressId *storage.AddressId
		if receipt.ContractAddress != zeroAddress {
			addressId := getAddressId(ctx, repo, receipt.ContractAddress)
			contractAddressId = &addressId
			scrapeContract(ctx, client, repo, receipt.ContractAddress, addressId, transactionIds[0])
		}
		for _, log := range receipt.Logs {
			contractAddressId := getAddressId(ctx, repo, log.Address)
			var topic0Id *storage.Topic0Id
			var indexedValues [3]*[32]byte
			hasAnyTopic := len(log.Topics) != 0
			if hasAnyTopic {
				var topic0Ids []storage.Topic0Id
				retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
					topic0Ids, err = repo.StoreTopic0(&storage.EventType{
						Hash: log.Topics[0],
					})
					if err == nil {
						topic0Id = &topic0Ids[0]
					}
					return err
				}, "StoreTopic0")

				if len(log.Topics) > 1 {
					for iValue := range log.Topics[1:] {
						value := [32]byte(log.Topics[iValue+1])
						indexedValues[iValue] = &value
					}
				}
			}
			logId := storage.LogId{
				TransactionId: transactionIds[0],
				LogIndex:      uint64(log.Index),
			}
			retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
				err = repo.StoreLog(&storage.Log{
					LogId:     logId,
					AddressId: contractAddressId,
					Topic0Id:  topic0Id,
					Topic1:    indexedValues[0],
					Topic2:    indexedValues[1],
					Topic3:    indexedValues[2],
					Data:      log.Data,
				})
				return err
			}, "StoreLog")
			if !hasAnyTopic {
				continue
			}
			switch log.Topics[0] {
			case erc20TransferTopic:
				value := new(big.Int).SetBytes(log.Data)
				if value.Cmp(maxUint256bit) == 1 {
					// value cannot fit into 256 bit, definitely not an ERC20 transfer
					break
				}
				var erc20Details *erc20Details
				var erc20Token *storage.Erc20Token
				retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) (err error) {
					erc20Token, err = repo.GetErc20TokenByAddressId(contractAddressId)
					return
				}, "GetErc20TokenByAddressId")
				if erc20Token == nil {
					retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
						erc20Details, err = getErc20Details(ctx, client, log.Address, block.Number())
						return err
					}, "getErc20Details")
					if erc20Details == nil {
						break
					}
					retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
						return repo.StoreErc20Token(&storage.Erc20Token{
							AddressId:   contractAddressId,
							Symbol:      erc20Details.Symbol,
							Name:        erc20Details.Name,
							Decimals:    erc20Details.Decimals,
							TotalSupply: erc20Details.TotalSupply,
						})
					}, "StoreErc20Token")
				}
				erc20TokenTransfer := storage.Erc20TokenTransfer{
					LogId:          logId,
					TokenAddressId: contractAddressId,
					FromAddressId:  getAddressId(ctx, repo, common.BytesToAddress(log.Topics[1][:])),
					ToAddressId:    getAddressId(ctx, repo, common.BytesToAddress(log.Topics[2][:])),
					Value:          value}
				retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
					return repo.StoreErc20TokenTransfer(&erc20TokenTransfer)
				}, "StoreErc20TokenTransfer")
				addErc20TokenTransferDeltas(&erc20TokenTransfer, addressIdToErc20TokenBalanceDeltas)
			}
		}
		transactionTraces, addressToStateChange := traceTransaction(ctx, client, repo, transactionIds[0], receipt)
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			return repo.StoreReceipt(&storage.Receipt{
				TransactionId:     transactionIds[0],
				CumulativeGasUsed: receipt.CumulativeGasUsed,
				GasUsed:           receipt.GasUsed,
				ContractAddressId: contractAddressId,
				PostState:         common.BytesToHash(receipt.PostState),
				Status:            receipt.Status == 1,
				EffectiveGasPrice: receipt.EffectiveGasPrice,
			})
		}, "StoreReceipt")
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			return repo.StoreTrace(transactionTraces...)
		}, "StoreTrace")
		var stateChanges []*storage.StateChange
		var storageChanges []*storage.StorageChange
		for address, stateDiff := range addressToStateChange {
			var balanceBefore, balanceAfter *big.Int
			var nonceBefore, nonceAfter *uint64
			if stateDiff.stateChange != nil {
				balanceBefore = stateDiff.stateChange.BalanceBefore
				balanceAfter = stateDiff.stateChange.BalanceAfter
				nonceBefore = stateDiff.stateChange.NonceBefore
				nonceAfter = stateDiff.stateChange.NonceAfter
			}
			stateChanges = append(stateChanges, &storage.StateChange{
				TransactionId: transactionIds[0],
				AddressId:     address,
				BalanceBefore: balanceBefore,
				BalanceAfter:  balanceAfter,
				NonceBefore:   nonceBefore,
				NonceAfter:    nonceAfter,
			})
			for _, storageChange := range stateDiff.storageChanges {
				storageChanges = append(storageChanges, &storage.StorageChange{
					TransactionId:  transactionIds[0],
					AddressId:      address,
					StorageAddress: storageChange.StorageAddress,
					ValueBefore:    storageChange.ValueBefore,
					ValueAfter:     storageChange.ValueAfter,
				})
			}
		}

		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			return repo.StoreStateChange(stateChanges...)
		}, "StoreStateChange")

		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			return repo.StoreStorageChange(storageChanges...)
		}, "StoreStorageChange")

		for addressId, stateDiff := range addressToStateChange {
			if stateDiff.stateChange != nil {
				addressIdToEtherBalance[addressId] = stateDiff.stateChange.BalanceAfter
			}
		}
		if logger != nil {
			logger.Send(logData{
				BlockNumber:           blockNumber,
				Transactions:          uint32(len(transactions)),
				ProcessedTransactions: uint32(transactionIndex) + 1,
			})
		}
	}

	// Apply the reward deltas on the ether balances.
	for address, reward := range rewardsByMiner {
		var addressIds []storage.AddressId
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			addressIds, err = repo.StoreAddress(address)
			return err
		}, "getMainnetBlockReward: StoreAddress")
		if _, hasStateDiffBalance := addressIdToEtherBalance[addressIds[0]]; hasStateDiffBalance {
			addressIdToEtherBalance[addressIds[0]].Add(addressIdToEtherBalance[addressIds[0]], reward)
		} else {
			var lastEtherBalance *storage.EtherBalance
			retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
				lastEtherBalance, err = repo.GetLastStoredEtherBalance(addressIds[0])
				return
			}, "GetLastStoredEtherBalance")
			if lastEtherBalance == nil {
				addressIdToEtherBalance[addressIds[0]] = reward
			} else {
				if lastEtherBalance.BlockNumber > blockNumber {
					panic("programming error")
				}
				if lastEtherBalance.BlockNumber != blockNumber {
					addressIdToEtherBalance[addressIds[0]] = new(big.Int).Add(lastEtherBalance.Balance, reward)
				}
			}
		}
	}

	// Prepare all ether balances for bulk insert
	var etherBalances []*storage.EtherBalance
	for addressId, balance := range addressIdToEtherBalance {
		etherBalances = append(etherBalances, &storage.EtherBalance{
			BlockNumber: blockNumber,
			AddressId:   addressId,
			Balance:     balance,
		})
	}

	retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
		return repo.StoreEtherBalance(etherBalances...)
	}, "StoreEtherBalance")

	// Apply delta on the pervious ERC-20 token balances (the most recent updated balances) and store the new balances in a new record
	var erc20TokenBalances []*storage.Erc20TokenBalance
	var lastStoredErc20TokenBalance *storage.Erc20TokenBalance
	for holderAddressId, balanceDeltas := range addressIdToErc20TokenBalanceDeltas {
		for tokenAddressId, balanceDelta := range balanceDeltas {
			retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
				lastStoredErc20TokenBalance, err = repo.GetLastStoredErc20TokenBalance(holderAddressId, tokenAddressId)
				return err
			}, "GetLastStoredErc20TokenBalance")
			previousBalance := big.NewInt(0)
			if lastStoredErc20TokenBalance != nil {
				if lastStoredErc20TokenBalance.BlockNumber == blockNumber {
					// current block's balance change has already been added for this address, skip
					continue
				}
				previousBalance = lastStoredErc20TokenBalance.Balance
			}
			erc20TokenBalances = append(erc20TokenBalances, &storage.Erc20TokenBalance{
				BlockNumber:    blockNumber,
				AddressId:      holderAddressId,
				TokenAddressId: tokenAddressId,
				Balance:        new(big.Int).Add(previousBalance, balanceDelta),
			})
		}
	}
	retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
		return repo.StoreErc20TokenBalance(erc20TokenBalances...)
	}, "StoreEtherBalance")

	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
		return repo.Commit(ctx)
	}, "Commit")
}

// calculateTotalDifficulty returns the current block's difficulty added to the previous block's total difficulty,
// which is the current block's total difficulty.
func calculateTotalDifficulty(ctx context.Context, repo storage.Storage, currentBlockDifficulty *big.Int) *big.Int {
	var latestStoredBlockNumber *uint64
	retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
		latestStoredBlockNumber, err = repo.GetLatestBlockNumber()
		return
	}, "GetLatestBlockNumber")
	result := new(big.Int).Set(currentBlockDifficulty)
	if latestStoredBlockNumber != nil {
		var previousBlock *storage.Block
		retryUntilSuccessOrContextDone(ctx,
			func(context.Context) (err error) {
				previousBlock, err = repo.GetBlockByNumber(*latestStoredBlockNumber)
				return
			}, "GetBlockByNumber")
		result.Add(result, previousBlock.TotalDifficulty)
	}
	return result
}

// scrapeContract stores the contract entity with all its dependencies.
func scrapeContract(ctx context.Context, client *ethclient.Client, repo storage.Storage, address common.Address, addressId storage.AddressId, transactionId storage.TransactionId) {
	var contract *storage.Contract

	var err error
	retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
		contract, err = repo.GetContractByAddressId(addressId)
		return err
	}, "GetContractByAddressId")

	if contract != nil {
		return
	}

	var bytecode []byte
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
		bytecode, err = client.CodeAt(ctx, address, nil)
		return err
	}, "CodeAt")

	if len(bytecode) == 0 {
		return
	}

	var byteCodeIds []storage.BytecodeId
	retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
		byteCodeIds, err = repo.StoreBytecode(bytecode)
		return err
	}, "StoreBytecode")

	retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
		return repo.StoreContract(&storage.Contract{
			AddressId:     addressId,
			TransactionId: transactionId,
			BytecodeId:    byteCodeIds[0],
		})
	}, "StoreContract")
}
func addErc20TokenTransferDeltas(transfer *storage.Erc20TokenTransfer, toMap map[storage.AddressId]map[storage.AddressId]*big.Int) {
	if toMap == nil {
		toMap = make(map[storage.AddressId]map[storage.AddressId]*big.Int)
	}
	if toMap[transfer.FromAddressId] == nil {
		toMap[transfer.FromAddressId] = make(map[storage.AddressId]*big.Int)
	}
	if toMap[transfer.ToAddressId] == nil {
		toMap[transfer.ToAddressId] = make(map[storage.AddressId]*big.Int)
	}
	if toMap[transfer.FromAddressId][transfer.TokenAddressId] == nil {
		toMap[transfer.FromAddressId][transfer.TokenAddressId] = new(big.Int)
	}
	if toMap[transfer.ToAddressId][transfer.TokenAddressId] == nil {
		toMap[transfer.ToAddressId][transfer.TokenAddressId] = new(big.Int)
	}
	toMap[transfer.ToAddressId][transfer.TokenAddressId].Add(toMap[transfer.ToAddressId][transfer.TokenAddressId], transfer.Value)
	toMap[transfer.FromAddressId][transfer.TokenAddressId].Sub(toMap[transfer.FromAddressId][transfer.TokenAddressId], transfer.Value)
}
func loadGenesisConfig(ctx context.Context, repo storage.Storage) *core.Genesis {
	coreGenesis, envGenesisFound := Env2CoreGenesis()
	fmt.Printf("%#v : %#v\n", envGenesisFound, &coreGenesis)
	if envGenesisFound {
		return coreGenesis
	}
	if env_GENESIS_JSON_PATH == "" {
		return core.DefaultGenesisBlock()
	}

	// Read the Genesis JSON file
	genesisJSON, err := os.ReadFile("genesis.json")
	if err != nil {
		log.Fatalln("Error reading Genesis JSON:", err)
	}

	// Parse the Genesis JSON content
	config := core.Genesis{}
	if err := json.Unmarshal(genesisJSON, &config); err != nil {
		log.Fatalln("Error parsing Genesis JSON:", err)
	}
	return &config
}
