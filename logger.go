package main

import (
	"log"
	"time"
)

type logData struct {
	BlockNumber           uint64
	Transactions          uint32
	ProcessedTransactions uint32
}
type Logger chan logData

func NewLogger(frequency time.Duration) Logger {
	logger := make(chan logData)
	go func() {
		ticker := time.NewTicker(frequency)
		for range ticker.C {
			data := <-logger
			log.Printf("Block number: %v, processed %v transaction(s) out of %v",
				data.BlockNumber,
				data.ProcessedTransactions,
				data.Transactions)
		}
	}()
	return logger
}
func (logger Logger) Send(data logData) {
	select {
	case logger <- data:
	default:
	}
}
