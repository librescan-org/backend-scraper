package main

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/metachris/eth-go-bindings/erc20"
)

var erc20TransferTopic = common.HexToHash("ddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef")

// var erc20ApprovalTopic = common.HexToHash("8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925")
type erc20Details struct {
	Symbol      string
	Name        string
	Decimals    uint8
	TotalSupply *big.Int
}

func getErc20Details(ctx context.Context, client *ethclient.Client, address common.Address, blockNumber *big.Int) (*erc20Details, error) {
	erc20Caller, err := erc20.NewErc20Caller(address, client)
	if err != nil {
		panic(err)
	}
	totalSupply, err := erc20Caller.TotalSupply(&bind.CallOpts{
		From:        address,
		BlockNumber: blockNumber,
		Context:     ctx,
	})
	if err != nil {
		if isRpcOrAbiError(err) {
			return nil, nil
		}
		return nil, err
	}
	_, err = erc20Caller.BalanceOf(&bind.CallOpts{
		BlockNumber: blockNumber,
		Context:     ctx,
	}, address)
	if err != nil {
		if isRpcOrAbiError(err) {
			return nil, nil
		}
		return nil, err
	}
	symbol, err := erc20Caller.Symbol(&bind.CallOpts{
		BlockNumber: blockNumber,
		Context:     ctx,
	})
	if err != nil {
		if !isRpcOrAbiError(err) {
			return nil, err
		}
	}
	name, err := erc20Caller.Name(&bind.CallOpts{
		BlockNumber: blockNumber,
		Context:     ctx,
	})
	if err != nil {
		if !isRpcOrAbiError(err) {
			return nil, err
		}
	}
	decimals, err := erc20Caller.Decimals(&bind.CallOpts{
		BlockNumber: blockNumber,
		Context:     ctx,
	})
	if err != nil {
		if !isRpcOrAbiError(err) {
			return nil, err
		}
	}
	return &erc20Details{
		Symbol:      symbol,
		Name:        name,
		Decimals:    decimals,
		TotalSupply: totalSupply,
	}, nil
}
