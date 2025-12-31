# 🚀 EVM Enterprise-Grade Indexer (Go + Solidity)

![Go Version](https://img.shields.io/badge/Go-1.21-blue.svg)
![Docker](https://img.shields.io/badge/Docker-Enabled-blue.svg)
![License](https://img.shields.io/badge/license-MIT-green)
![Status](https://img.shields.io/badge/Status-Production_Ready-brightgreen)

A production-ready, fault-tolerant blockchain event indexer built with **Go**, **PostgreSQL**, and **Prometheus**.
Designed to monitor real-time EVM smart contract events (e.g., USDT Transfers), ensure data consistency during chain reorgs, and perform high-concurrency historical backfilling.

> **Why this project?**
> This project demonstrates a complete transition from Web2 to Web3 infrastructure, addressing critical challenges like **Chain Reorganization (Reorgs)**, **RPC Rate Limiting**, **Graceful Shutdown**, and **System Observability**.

## 🌟 Key Features

* **🛡️ Robust Reorg Handling**: Implements "Parent Hash Verification" to detect chain forks and automatically roll back dirty data to ensure strong consistency.
* **🛑 Graceful Shutdown**: Uses `context` and `signal.Notify` to ensure no data corruption occurs when the service is stopped (Ctrl+C or Docker Stop).
* **⚡ Multi-threaded Backfill**: specialized worker pool mode to fetch historical data concurrently (Producer-Consumer pattern).
* **📊 Observability Stack**: Integrated **Prometheus** & **Grafana** for real-time monitoring of Sync Lag, Block Height, and Transaction throughput.
* **⚙️ Advanced Configuration**: Uses **Viper** for hierarchical configuration (Environment Vars > Config File > Defaults).
* **📄 Pagination API**: Standardized RESTful API with `page` and `page_size` support.
* **🐳 Docker Native**: Full container orchestration with Docker Compose.

## 🛠 Tech Stack

* **Core**: Golang 1.21+, GORM (ORM), Gin (Web Framework)
* **Blockchain**: go-ethereum (geth) SDK
* **Database**: PostgreSQL 15
* **Ops**: Docker, Docker Compose, Viper (Config)
* **Monitoring**: Prometheus, Grafana

## 🏗 Architecture

```mermaid
graph TD
    subgraph "External World"
        ETH[Ethereum Network (RPC)]
        User[API Client]
    end

    subgraph "Docker Compose Cluster"
        subgraph "Go Service"
            Main[Main Controller]
            Config[Viper Config]
            
            subgraph "Worker Pool"
                RT[Realtime Syncer]
                BF[Backfill Workers]
            end
            
            API[Gin API Server]
        end

        DB[(PostgreSQL)]
        Prom[Prometheus]
        Graf[Grafana]
    end

    ETH <-->|JSON-RPC| RT
    ETH <-->|JSON-RPC| BF
    
    RT -->|Write/Rollback| DB
    BF -->|Batch Write| DB
    
    User -->|GET /transfers| API
    API -->|Query| DB
    
    Prom -->|Pull /metrics| API
    Graf -->|Visualize| Prom
```

## 🚀 Quick Start

### Prerequisites
* [Docker](https://www.docker.com/) & Docker Compose installed.
* An Ethereum RPC Endpoint (Infura/Alchemy).

### 1. Configuration
Create a `config.yaml` in the root directory (or use Environment Variables in Docker):

```yaml
server:
  port: "8080"
database:
  dsn: "host=db user=postgres password=mysecretpassword dbname=web3_indexer port=5432 sslmode=disable"
chain:
  rpc_url: "https://mainnet.infura.io/v3/YOUR_API_KEY"
  contract_address: "0xdAC17F958D2ee523a2206206994597C13D831ec7" # USDT
  start_block: 18000000
```

### 2. Run with Docker (Realtime Mode)
Start the entire stack (App + DB + Monitoring):

```bash
docker-compose up --build -d
```

Check the logs to see the syncing process:
```bash
docker-compose logs -f app
```

### 3. Access Dashboards
* **API Endpoint**: `http://localhost:8080/api/v1/transfers`
* **Grafana Dashboard**: `http://localhost:3000` (User: `admin` / Pass: `admin`)
* **Prometheus**: `http://localhost:9090`

---

## 🏎 Advanced Usage: Historical Backfill

To backfill old data (e.g., last year's transactions) using high-concurrency workers without stopping the database:

**Run a one-off backfill task inside the container network:**

```bash
# Backfill from block 17,000,000 to 17,005,000 with 5 concurrent workers
docker-compose run --rm app ./main -backfill -start=17000000 -end=17005000 -workers=5
```

> *Note: The backfill process uses `INSERT ON CONFLICT DO NOTHING`, so it's safe to run multiple times.*

---

## 📡 API Documentation

### Get Transfer Logs
Returns paginated list of ERC20 transfer events.

**Request:**
```http
GET /api/v1/transfers?address=0xdAC...&page=1&page_size=10
```

**Response:**
```json
{
  "data": [
    {
      "ID": 1,
      "TxHash": "0x123...",
      "BlockNumber": 18000001,
      "FromAddress": "0xabc...",
      "ToAddress": "0xdef...",
      "Amount": "100000000"
    }
  ],
  "total": 500,
  "page": 1,
  "page_size": 10,
  "total_pages": 50
}
```

## 🧪 Development & Testing

**Local Run (Non-Docker):**
```bash
# Ensure local Postgres is running
go run .
```

**Graceful Shutdown Test:**
Press `Ctrl+C` while the indexer is running. You will see:
> `🛑 Shutdown signal received. Closing application safely...`
> `✅ System exited successfully.`

## 📂 Project Structure

```text
.
├── config/             # Viper configuration logic
├── token/              # Smart Contract Go Bindings (ABIGen)
├── main.go             # Entry Point (Indexer & Backfill Logic)
├── server.go           # REST API & Graceful Shutdown
├── metrics.go          # Prometheus Metrics Definitions
├── Dockerfile          # Multi-stage Docker build
├── docker-compose.yml  # Orchestration
├── config.yaml         # Local config file
└── prometheus.yml      # Monitoring config
```

## 👨‍💻 Author

**GuangchaoYi**
* Full Stack Developer (Go / Java / Solidity)
* [https://github.com/yiguangchao]
