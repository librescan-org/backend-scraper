# backend-scraper

This module collects detailed blockchain data from an archive node and stores it in a relational database.  
LibreScan works with any OpenEthereum derivative blockchain node, which exposes the trace_* RPC namespace in a compatible manner.
The node exposing the RPC endpoint must be running in full-archive mode.

Some example nodes are:

- [QuickNode](https://www.quicknode.com?tap_a=67226-09396e&tap_s=4155448-b52731&utm_source=affiliate&utm_campaign=generic&utm_content=affiliate_landing_page&utm_medium=generic)
- [LlamaNodes](https://llamarpc.com/eth)
- [Alchemy](https://alchemy.com)
- [Infura](https://infura.io)

## Getting started

### Required environment variables

The RPC client's access URL. Must be http or https. Websocket protocol (wss) is not supported at this time. The example URL is just for demonstration's sake:

```
RPC_URL="https://eth.llamarpc.com"
```

Postgres database connection details. All values are just examples to show the expected value formats:

```
POSTGRES_DB="postgres"  
POSTGRES_HOST="localhost"  
POSTGRES_PORT="5432"  
POSTGRES_USER="postgres"  
POSTGRES_PASSWORD="123"
```

### Optional environment variables (examples show the default values)

If you want to scrape a chain that is not Ethereum Mainnet, provide the chain's genesis json file:

```
GENESIS_JSON_PATH=""
```

Periodic status logging is enabled by default and is set to 15 second. The value is a Go duration string, for example "1s" for one second. To disable logging, set to "0". To set a specific duration for the logging frequency, see the official Go documentation for [time.ParseDuration](https://pkg.go.dev/time#ParseDuration). Negative durations are rejected.

```
LOG_FREQUENCY="15s"
```
