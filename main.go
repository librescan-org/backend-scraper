package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	env_DEBUG_START_AT_BLOCK_NUMBER = os.Getenv("DEBUG_START_AT_BLOCK_NUMBER")
	env_RPC_URL                     = os.Getenv("RPC_URL")
	env_GENESIS_JSON_PATH           = os.Getenv("GENESIS_JSON_PATH")
	env_LOG_FREQUENCY               = os.Getenv("LOG_FREQUENCY")
	logFrequency                    time.Duration
	logger                          Logger
)

func main() {
	if env_RPC_URL == "" {
		log.Fatal("missing RPC_URL environment variable")
	}
	var err error
	if env_LOG_FREQUENCY == "" {
		env_LOG_FREQUENCY = "0"
	}
	logFrequency, err = time.ParseDuration(env_LOG_FREQUENCY)
	if err != nil {
		log.Fatalf("Invalid LOG_FREQUENCY: '%s'. Must be a valid duration", env_LOG_FREQUENCY)
	}
	if logFrequency < 0 {
		log.Fatal("LOG_FREQUENCY cannot represent a negative duration")
	}
	// Listen for OS interrupt signal (CTRL+C) to cancel the context.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	var client *ethclient.Client
	retryUntilSuccessOrContextDone(ctx, func(ctx context.Context) (err error) {
		client, err = ethclient.DialContext(ctx, env_RPC_URL)
		return err
	}, "Dial")

	defer client.Close()
	if logFrequency != 0 {
		logger = NewLogger(logFrequency)
	}
	startScraper(ctx, client)
}
