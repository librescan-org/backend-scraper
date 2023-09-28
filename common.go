package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	storage "github.com/librescan-org/backend-db"
)

var isPoaChain *bool
var maxUint256bit = new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil)
var zeroAddress common.Address

func getAddressId(ctx context.Context, repo storage.Storage, address common.Address) storage.AddressId {
	var toAddressIds []storage.AddressId
	retryUntilSuccessOrContextDone(ctx, func(context.Context) (err error) {
		toAddressIds, err = repo.StoreAddress(address)
		return err
	}, "trace: StoreAddress")
	return toAddressIds[0]
}

func getMiner(ctx context.Context, chainId uint64, client *ethclient.Client, blockHeader *types.Header) common.Address {
	if isPoaChain == nil {
		// Check whether the chain's consensus is Proof Of Authority.
		var result any
		retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) (err error) {
			err = client.Client().CallContext(ctx, &result, "clique_getSigners", "0x1")
			if err == nil {
				t := true
				isPoaChain = &t
			} else if _, isRpcErr := err.(rpc.Error); isRpcErr {
				err = nil
				isPoaChain = new(bool)
			}
			return
		}, "Client().CallContext: clique_getSignersAtHash")
	}
	if *isPoaChain {
		var miner string
		if blockHeader.Number.Uint64() != 0 {
			var result any
			retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) error {
				return client.Client().CallContext(ctx, &result, "clique_getSigner", fmt.Sprintf("0x%x", blockHeader.Number))
			}, "Client().CallContext: clique_getSigner")
			miner = result.(string)
		}
		return common.HexToAddress(miner)
	} else {
		return blockHeader.Coinbase
	}
}
func isRpcOrAbiError(err error) bool {
	_, isRpcErr := err.(rpc.Error)
	if isRpcErr {
		return true
	}
	// Unfortunately no other way of checking for ABI related errors.
	return strings.HasPrefix(err.Error(), "abi: ")
}
func retryUntilSuccessOrContextDone(ctx context.Context, funcToRetry func(context.Context) error, errName string) {
	var err error
	for {
		select {
		case <-ctx.Done():
			log.Fatal("shutting down")
		default:
			if err = funcToRetry(ctx); err == nil {
				return
			}
			log.Printf("%s: %s\n", errName, err)
			time.Sleep(time.Second)
		}
	}
}
