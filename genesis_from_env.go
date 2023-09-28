package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/core"
)

type Genesis struct {
	ChainId string
	Clique  struct {
		Period string
		Epoch  string
	}
	Alloc     map[string]string
	Signers   []string
	GasLimit  string
	Timestamp string
}

var genesis Genesis

func getJsonFromEnv() (string, bool) {

	envConfigActive := false

	genesis = Genesis{
		ChainId: "37550",
		Clique: struct {
			Period string
			Epoch  string
		}{
			Period: "15",
			Epoch:  "30000",
		},
		GasLimit:  "15000000",
		Alloc:     make(map[string]string),
		Timestamp: "0",
	}

	parsers := []func(string, string, *Genesis) bool{
		parseChainId,
		parseSigner,
		parseAlloc,
		parseGasLimit,
		parseCliquePeriod,
		parseCliqueEpoch,
		parseTimestamp,
	}
ENV:
	for _, env := range os.Environ() {

		// split into k and v
		e := strings.Split(env, "=")
		k, v := e[0], e[1]

		const minLength = len(`QAN_`)
		// we are only interested in QAN_ prefixed keys
		if len(k) < minLength {
			continue
		}

		for _, parser := range parsers {
			if parser(k, v, &genesis) {
				if !envConfigActive {
					envConfigActive = true
				}
				continue ENV
			}
		}
	}

	if !envConfigActive {
		return "", false
	}

	return getjson(), true
}

/// PARSERS START ///

var qanPrefixes = []string{
	"QAN_GENESIS_",
	"QAN_", // backward compatible
}

// qanGenesisEnv checks if envKey is QAN genesis environment and return the key after trim down the QAN_* prefix
func qanGenesisEnv(envKey string) (string, bool) {
	for _, prefix := range qanPrefixes {
		if strings.HasPrefix(envKey, prefix) {
			return strings.TrimPrefix(envKey, prefix), true
		}
	}
	return "", false
}

func parseChainId(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if k == "CHAINID" {
		g.ChainId = v
		return true
	}
	return false
}

func parseSigner(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if strings.Index(k, "SIGNER_") == 0 {
		v = strings.Replace(v, "0x", "", 1)
		if len(v) == 40 {
			v = strings.ToLower(v)
			g.Signers = append(g.Signers, v)
			return true
		}
	}
	return false
}

func parseAlloc(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if strings.Index(k, "ALLOC_") == 0 {
		k = strings.Replace(k, "ALLOC_", "", 1)
		k = strings.Replace(k, "0x", "", 1)
		if len(k) == 40 {
			k = strings.ToLower(k)
			g.Alloc[k] = v
			return true
		}
	}
	return false
}

func parseGasLimit(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if k == "GASLIMIT" {
		g.GasLimit = v
		return true
	}
	return false
}

func parseCliquePeriod(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if k == "CLIQUE_PERIOD" {
		g.Clique.Period = v
		return true
	}
	return false
}

func parseCliqueEpoch(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if k == "CLIQUE_EPOCH" {
		g.Clique.Epoch = v
		return true
	}
	return false
}

func parseTimestamp(k, v string, g *Genesis) bool {
	k, ok := qanGenesisEnv(k)
	if !ok {
		return false
	}
	if k == "TIMESTAMP" {
		g.Timestamp = v
		return true
	}
	return false
}

/// PARSERS END ///

/// TOJSON START ///

var template = `
{
    "config": {
        "chainId": {{CHAINID}},
        "homesteadBlock": 0,
        "eip150Block": 0,
        "eip155Block": 0,
        "eip158Block": 0,
        "byzantiumBlock": 0,
        "constantinopleBlock": 0,
        "petersburgBlock": 0,
        "istanbulBlock": 0,
        "berlinBlock": 0,
        "londonBlock": 0,
        "clique": {
            "period": {{CLIQUE.PERIOD}},
            "epoch": {{CLIQUE.EPOCH}}
        }
    },
    "alloc": {
        "0000000000000000000000000000000000000001": { "balance": "1" },
        "0000000000000000000000000000000000000002": { "balance": "1" },
        "0000000000000000000000000000000000000003": { "balance": "1" },
        "0000000000000000000000000000000000000004": { "balance": "1" },
        "0000000000000000000000000000000000000005": { "balance": "1" },
        "0000000000000000000000000000000000000006": { "balance": "1" },
        "0000000000000000000000000000000000000007": { "balance": "1" },
        "0000000000000000000000000000000000000008": { "balance": "1" }
        {{ALLOC}}
    },
    "difficulty": "1",
    "extraData": "0x0000000000000000000000000000000000000000000000000000000000000000{{SIGNERS}}0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "gasLimit": "{{GASLIMIT}}",
    "timestamp": "{{TIMESTAMP}}"
}
`

func getjson() string {
	template = strings.Replace(template, "{{CHAINID}}", genesis.ChainId, 1)
	template = strings.Replace(template, "{{CLIQUE.PERIOD}}", genesis.Clique.Period, 1)
	template = strings.Replace(template, "{{CLIQUE.EPOCH}}", genesis.Clique.Epoch, 1)
	template = strings.Replace(template, "{{SIGNERS}}", strings.Join(genesis.Signers, ""), 1)
	template = strings.Replace(template, "{{GASLIMIT}}", genesis.GasLimit, 1)
	template = strings.Replace(template, "{{TIMESTAMP}}", genesis.Timestamp, 1)
	alloc := ""
	for addr, balance := range genesis.Alloc {
		alloc += fmt.Sprintf(`,"%s": { "balance": "%s" }`, addr, balance)
	}
	template = strings.Replace(template, "{{ALLOC}}", alloc, 1)
	err := os.MkdirAll("/data/private", 0777)
	if err == nil {
		os.WriteFile("/data/private/genesis.json", []byte(template), 0777)
	}
	return template
}

/// TOJSON END ///

func Env2CoreGenesis() (*core.Genesis, bool) {
	genesisJSON, found := getJsonFromEnv()
	if !found {
		log.Println("ENV config not found for genesis")
		return nil, false
	}

	// Parse the Genesis JSON content
	config := core.Genesis{}
	if err := json.Unmarshal([]byte(genesisJSON), &config); err != nil {
		log.Fatalln("Error parsing Genesis JSON:", err)
	}
	return &config, true
}
