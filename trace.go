package main

import (
	"context"
	"encoding/hex"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

type TraceType = string

type stateChange struct {
	BalanceBefore *big.Int
	BalanceAfter  *big.Int
	NonceBefore   *uint64
	NonceAfter    *uint64
}
type storageChange struct {
	StorageAddress *big.Int
	ValueBefore    *big.Int
	ValueAfter     *big.Int
}
type stateDiff struct {
	stateChange    *stateChange
	storageChanges []*storageChange
}
type StateChangeType = string

const (
	StateChangeInitial      StateChangeType = "+"
	StateChangeModify       StateChangeType = "*"
	StateChangeSelfDestruct StateChangeType = "-"
)
const (
	TraceTypeCall    TraceType = "call"
	TraceTypeCreate  TraceType = "create"
	TraceTypeSuicide TraceType = "suicide"
)

func traceTransaction(ctx context.Context, client *ethclient.Client, repo storage.Storage, transactionId storage.TransactionId, receipt *types.Receipt) ([]*storage.TraceAction, map[storage.AddressId]stateDiff) {
	var traceReplayTransaction map[string]any
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
		return client.Client().CallContext(ctx, &traceReplayTransaction, "trace_replayTransaction", receipt.TxHash.Hex(), []string{"trace", "stateDiff"})
	}, "Client().CallContext: trace_replayTransaction")

	// parse traces
	traces := traceReplayTransaction["trace"].([]any)
	traceActionsNormalized := make([]*storage.TraceAction, 0, len(traces))
	for i, trace := range traces {
		traceActionsNormalized = append(traceActionsNormalized, transformTraceMapToStruct(ctx, repo, trace.(map[string]any), transactionId, uint16(i), receipt))
	}

	// parse stateDiff
	addressToStateDiff := make(map[storage.AddressId]stateDiff)
	for address, diff := range traceReplayTransaction["stateDiff"].(map[string]any) {
		typedDiff := diff.(map[string]any)
		var addressIds []storage.AddressId
		retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
			addressIds, err = repo.StoreAddress(common.HexToAddress(address))
			return err
		}, "transformTraceMapToStruct->StoreAddress (from)")
		var balanceBefore, balanceAfter *big.Int
		var success bool
		var stateChangePtr *stateChange
		balance, hasBalanceKey := typedDiff["balance"].(map[string]any)
		if hasBalanceKey {
			switch {
			case balance[StateChangeInitial] != nil:
				balanceBefore = new(big.Int)
				balanceAfter, success = new(big.Int).SetString(balance[StateChangeInitial].(string), 0)
				if !success {
					panic("programming error")
				}
			case balance[StateChangeModify] != nil:
				balanceSum := balance[StateChangeModify].(map[string]any)
				balanceBefore, success = new(big.Int).SetString(balanceSum["from"].(string), 0)
				if !success {
					panic("programming error")
				}
				balanceAfter, success = new(big.Int).SetString(balanceSum["to"].(string), 0)
				if !success {
					panic("programming error")
				}
			case balance[StateChangeSelfDestruct] != nil:
				balanceBefore, success = new(big.Int).SetString(balance[StateChangeSelfDestruct].(string), 0)
				if !success {
					panic("programming error")
				}
				balanceAfter = new(big.Int)
			default:
				panic("unknown statediff balance format")
			}
			nonce, hasNonceKey := typedDiff["nonce"].(map[string]any)
			stateChangePtr = &stateChange{
				BalanceBefore: balanceBefore,
				BalanceAfter:  balanceAfter,
			}
			if hasNonceKey {
				switch {
				case nonce[StateChangeInitial] != nil:
					nonceAfter, err := strconv.ParseUint(nonce[StateChangeInitial].(string)[2:], 16, 64)
					if err != nil {
						panic(err)
					}
					stateChangePtr.NonceAfter = &nonceAfter
				case nonce[StateChangeModify] != nil:
					minMaxNonces := nonce[StateChangeModify].(map[string]any)
					fromNonceVal, err := strconv.ParseUint(minMaxNonces["from"].(string)[2:], 16, 64)
					if err != nil {
						panic(err)
					}
					stateChangePtr.NonceBefore = &fromNonceVal
					nonceAfter, err := strconv.ParseUint(minMaxNonces["to"].(string)[2:], 16, 64)
					if err != nil {
						panic(err)
					}
					stateChangePtr.NonceAfter = &nonceAfter
				case nonce[StateChangeSelfDestruct] != nil:
					val, err := strconv.ParseUint(nonce[StateChangeSelfDestruct].(string)[2:], 16, 64)
					if err != nil {
						panic(err)
					}
					stateChangePtr.NonceBefore = &val
					stateChangePtr.NonceAfter = &val
				default:
					panic("unknown statediff nonce format")
				}
			}
		}

		// parse storage changes
		storageValues := typedDiff["storage"].(map[string]any)
		var storageChanges []*storageChange
		for storageAddress, change := range storageValues {
			typedChange := change.(map[string]any)
			storageAddressBigInt, success := new(big.Int).SetString(storageAddress, 0)
			if !success {
				panic("programming error")
			}
			storageChange := storageChange{
				StorageAddress: storageAddressBigInt,
			}
			if typedChange[StateChangeInitial] == nil {
				valueBefore, success := new(big.Int).SetString(typedChange[StateChangeModify].(map[string]any)["from"].(string), 0)
				if !success {
					panic("programming error")
				}
				valueAfter, success := new(big.Int).SetString(typedChange[StateChangeModify].(map[string]any)["to"].(string), 0)
				if !success {
					panic("programing error")
				}
				storageChange.ValueBefore = valueBefore
				storageChange.ValueAfter = valueAfter
			} else {
				valueAfter, success := new(big.Int).SetString(typedChange[StateChangeInitial].(string), 0)
				if !success {
					panic("programing error")
				}
				storageChange.ValueBefore = new(big.Int)
				storageChange.ValueAfter = valueAfter
			}
			storageChanges = append(storageChanges, &storageChange)
		}
		addressToStateDiff[addressIds[0]] = stateDiff{
			stateChange:    stateChangePtr,
			storageChanges: storageChanges,
		}
	}
	return traceActionsNormalized, addressToStateDiff //, transactionSuccessFul
}
func transformTraceMapToStruct(ctx context.Context, repo storage.Storage, mapToTransform map[string]any, transactionId storage.TransactionId, index uint16, receipt *types.Receipt) *storage.TraceAction {
	action := mapToTransform["action"].(map[string]any)
	var errStr *string
	if errorMessage, hasErrKey := mapToTransform["error"]; hasErrKey {
		s := errorMessage.(string)
		errStr = &s
	}
	switch mapToTransform["type"].(TraceType) {
	case TraceTypeCall:
		var input []byte
		var err error
		input, err = hex.DecodeString(action["input"].(string)[2:])
		if err != nil {
			panic(err)
		}
		var gas uint64
		gas, err = strconv.ParseUint(action["gas"].(string)[2:], 16, 64)
		if err != nil {
			panic(err)
		}
		value, success := new(big.Int).SetString(action["value"].(string), 0)
		if !success {
			panic("invalid trace response format")
		}
		var fromAddressIds []storage.AddressId
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			fromAddressIds, err = repo.StoreAddress(common.HexToAddress(action["from"].(string)))
			return err
		}, "transformTraceMapToStruct->StoreAddress (from)")
		var toAddressIds []storage.AddressId
		retryUntilSuccessOrContextDone(ctx, func(context.Context) error {
			toAddressIds, err = repo.StoreAddress(common.HexToAddress(action["to"].(string)))
			return err
		}, "transformTraceMapToStruct->StoreAddress (to)")
		return &storage.TraceAction{
			TransactionId: transactionId,
			Index:         index,
			From:          fromAddressIds[0],
			To:            toAddressIds[0],
			Type:          action["callType"].(string),
			Gas:           gas,
			Input:         input,
			Value:         value,
			Error:         errStr,
		}
	case TraceTypeCreate:
		value, success := new(big.Int).SetString(action["value"].(string)[2:], 16)
		if !success {
			panic("invalid trace response format")
		}
		gas, err := strconv.ParseUint(action["gas"].(string)[2:], 16, 64)
		if err != nil {
			panic(err)
		}
		var address common.Address
		if mapToTransform["result"] == nil {
			address = receipt.ContractAddress
		} else {
			address = common.HexToAddress(mapToTransform["result"].(map[string]any)["address"].(string))
		}
		return &storage.TraceAction{
			TransactionId: transactionId,
			Index:         index,
			Type:          TraceTypeCreate,
			From:          getAddressId(ctx, repo, common.HexToAddress(action["from"].(string))),
			To:            getAddressId(ctx, repo, address),
			Value:         value,
			Gas:           gas,
			Error:         errStr,
		}
	case TraceTypeSuicide:
		balance, success := new(big.Int).SetString(action["balance"].(string)[2:], 16)
		if !success {
			panic("invalid trace response format")
		}
		return &storage.TraceAction{
			TransactionId: transactionId,
			Index:         index,
			Type:          TraceTypeSuicide,
			From:          getAddressId(ctx, repo, common.HexToAddress(action["address"].(string))),
			To:            getAddressId(ctx, repo, common.HexToAddress(action["refundAddress"].(string))),
			Value:         balance,
			Error:         errStr,
		}
	default:
		panic("unhandled/unknown trace type")
	}
}
