# ChainK8s — On-Chain Kubernetes Scheduler for Akash Network

ChainK8s is a Cosmos SDK module that extends the Akash Network node with on-chain Kubernetes scheduling. Providers register nodes via heartbeat transactions, and the chain automatically schedules user orders to the best-fit node.

## Architecture

```
Akash Chain Node (akash-0)
  ├── x/chaink8s module
  │     ├── EndBlock: runScheduler, updateSpotPrice, detectAndSlash
  │     └── gRPC QueryServer: /chaink8s.Query/*
  │
ck8s-monitor (one per provider node)
  ├── sendHeartbeat  → MsgNodeHeartbeat tx every 30s
  ├── handleBlock    → listen for chaink8s_bind events
  └── executePod     → create K8s Pod on bind event
  │
ck8s-query (HTTP API)
  └── GET /resources  → total available CPU/GPU/Mem
      GET /nodes      → per-node breakdown
```

## Prerequisites (new machine)

- Go 1.25+
- Docker
- Kubernetes cluster (tested with koordinator)
- Helm 3
- `kubectl` configured
- NVIDIA GPU + koordinator device plugin (for GPU scheduling)

## Deploy on a New Machine

### 1. Clone and build

```bash
git clone https://github.com/chriswong-6/chaink8s-node.git
cd chaink8s-node
go build -o build/akash        ./cmd/akash
go build -o build/ck8s-monitor ./cmd/ck8s-monitor
go build -o build/ck8s-query   ./cmd/ck8s-query
```

### 2. Initialize the Akash chain (first time only)

```bash
CHAIN_HOME=~/.akash-local
CHAIN_ID=local-test
MONIKER=node-1

./build/akash init $MONIKER --chain-id $CHAIN_ID --home $CHAIN_HOME
./build/akash keys add mykey --keyring-backend test --home $CHAIN_HOME
./build/akash genesis add-genesis-account mykey 100000000000uakt \
  --keyring-backend test --home $CHAIN_HOME
./build/akash genesis gentx mykey 10000000uakt \
  --chain-id $CHAIN_ID --keyring-backend test --home $CHAIN_HOME
./build/akash genesis collect-gentxs --home $CHAIN_HOME

# Allow gRPC from other pods
sed -i 's/address = "localhost:9190"/address = "0.0.0.0:9190"/' \
  $CHAIN_HOME/config/app.toml
```

### 3. Register a Provider

```bash
PROVIDER_ADDR=$(./build/akash keys show mykey -a \
  --keyring-backend test --home ~/.akash-local)

./build/akash tx provider create provider.yaml \
  --from mykey --chain-id local-test \
  --keyring-backend test --home ~/.akash-local \
  --fees 5000uakt -y
```

### 4. Deploy all components to Kubernetes

```bash
helm install ck8s ./helm/ck8s \
  --set monitor.providerAddr=$PROVIDER_ADDR \
  --set monitor.k8sNodeName=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
```

### 5. Verify

```bash
kubectl -n ck8s get pods
# NAME                            READY   STATUS
# akash-0                         1/1     Running
# ck8s-monitor-xxx                1/1     Running
# ck8s-query-xxx                  1/1     Running

kubectl -n ck8s port-forward svc/ck8s-query 8080:8080 &
curl http://localhost:8080/resources
```

## Helm Values

| Key | Default | Description |
|-----|---------|-------------|
| `monitor.providerAddr` | (required) | Provider bech32 address |
| `monitor.nodeID` | `node-1` | Chain logical node identifier |
| `monitor.k8sNodeName` | `lmdrive` | `kubectl get nodes` name |
| `monitor.heartbeat` | `30s` | Heartbeat interval |
| `monitor.podNamespace` | `default` | Namespace for user workload Pods |
| `query.service.type` | `ClusterIP` | Set `NodePort` for external access |
| `query.service.nodePort` | `` | NodePort number (e.g. `30080`) |
| `akash.hostDataPath` | `/home/lmdrive/.akash-local` | Host path for chain data |
| `buildPath` | `/home/lmdrive/node/build` | Host path for compiled binaries |

## Query API

```bash
# Total available resources (sum of all nodes)
GET /resources
→ {"cpu_cores":15,"cpu_milli":15700,"gpu":1,"gpu_mem_mib":24564,"mem_gib":40,...}

# Per-node breakdown
GET /nodes
→ [{"node_id":"node-1","free_cpu":15700,"free_gpu":1,"free_gpu_mem_mb":24564,...}]
```
