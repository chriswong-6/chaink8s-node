# chaink8s-node

Akash Network node extended with the `x/chaink8s` on-chain Kubernetes scheduler module.

Providers register GPU/CPU nodes on-chain via heartbeat transactions. The chain automatically schedules user orders to the best-fit node. All resource accounting is transparent and verifiable on-chain.

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│                     K8s Cluster                         │
│                                                         │
│  ┌─────────────────┐   ┌──────────────┐  ┌──────────┐  │
│  │   akash node    │   │ ck8s-monitor │  │ck8s-query│  │
│  │  (chaink8s mod) │◄──│              │  │ HTTP API │  │
│  │                 │   │ • heartbeat  │  │          │  │
│  │ • EndBlock:     │   │   every 30s  │  │ /resources│ │
│  │   schedule order│──►│ • watch bind │  │ /nodes   │  │
│  │   update price  │   │   events     │  └──────────┘  │
│  │   slash offline │   │ • create/del │                 │
│  └─────────────────┘   │   K8s Pods   │                 │
│                        └──────────────┘                 │
└─────────────────────────────────────────────────────────┘
```

1. `ck8s-monitor` reads K8s node allocatable resources, subtracts existing pod usage, sends `MsgNodeHeartbeat` tx to chain every 30s
2. Users submit orders to the chain (CPU/GPU/Mem requirements)
3. `akash` EndBlock scheduler picks the best node and emits a `chaink8s_bind` event
4. `ck8s-monitor` receives the bind event and creates a K8s Pod for the workload
5. `ck8s-query` exposes current resource availability over HTTP

---

## Prerequisites

- Kubernetes cluster (tested with koordinator for GPU scheduling)
- `kubectl` configured
- Helm 3
- Docker (for chain initialization)
- NVIDIA GPU + koordinator device plugin (for GPU workloads)

---

## Step 1: Initialize Chain Data (once per machine)

The chain data lives on the host and is mounted into the K8s pod. Use `docker run` to run the init commands — no local binary needed.

```bash
export CHAIN_HOME=/root/.akash-local
export CHAIN_ID=local-test

# Initialize chain directory
docker run --rm -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash init mynode --chain-id $CHAIN_ID --home /home/akash

# Create a key (save the mnemonic that is printed!)
docker run --rm -it -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash keys add mykey --home /home/akash --keyring-backend test

# Get the address
ADDR=$(docker run --rm -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash keys show mykey -a --home /home/akash --keyring-backend test)
echo "Address: $ADDR"

# Fund the address in genesis and finalize
docker run --rm -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash genesis add-genesis-account $ADDR 100000000000uakt --home /home/akash

docker run --rm -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash genesis gentx mykey 10000000uakt \
  --chain-id $CHAIN_ID --home /home/akash --keyring-backend test

docker run --rm -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash genesis collect-gentxs --home /home/akash

# Allow gRPC connections from other pods in the cluster
sed -i 's/address = "localhost:9190"/address = "0.0.0.0:9190"/' \
  $CHAIN_HOME/config/app.toml
```

---

## Step 2: Register Provider (once per machine)

Create a `provider.yaml` on the host:

```yaml
# $CHAIN_HOME/provider.yaml
host: https://your-provider-domain:8443
attributes:
  - key: region
    value: us-east
  - key: host
    value: akash
info:
  email: your@email.com
  website: ""
```

Register on-chain:

```bash
docker run --rm -v $CHAIN_HOME:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash tx provider create /home/akash/provider.yaml \
  --from mykey --chain-id $CHAIN_ID \
  --keyring-backend test --home /home/akash \
  --node http://localhost:26657 --fees 5000uakt -y
```

> Note: the node must already be running for this tx. Do this after Step 3 if it is the first time.

---

## Step 3: Deploy with Helm

```bash
helm repo add cfaas https://chriswong-6.github.io/CFaaS
helm repo update

helm install ck8s cfaas/ck8s \
  --namespace ck8s --create-namespace \
  --set akash.hostDataPath=/root/.akash-local \
  --set akash.chainID=local-test \
  --set monitor.providerAddr=<address from Step 1> \
  --set monitor.keyName=mykey \
  --set monitor.k8sNodeName=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
```

This deploys three components into the `ck8s` namespace:

| Pod | Role |
|-----|------|
| `akash-0` | Akash node with chaink8s module (StatefulSet) |
| `ck8s-monitor-*` | Heartbeat sender + Pod manager (Deployment) |
| `ck8s-query-*` | HTTP query API on port 8080 (Deployment) |

---

## Step 4: Verify

```bash
# Check all pods are running
kubectl -n ck8s get pods

# Expected output:
# NAME                            READY   STATUS
# akash-0                         1/1     Running
# ck8s-monitor-xxx                1/1     Running
# ck8s-query-xxx                  1/1     Running

# Watch monitor heartbeats (should see "heartbeat ok" every 30s)
kubectl -n ck8s logs deployment/ck8s-monitor -f

# Check resource availability
kubectl -n ck8s port-forward svc/ck8s-query 8080:8080 &
curl http://localhost:8080/resources
```

Expected `/resources` output:
```json
{"cpu_milli":15700,"cpu_cores":15,"mem_bytes":43945304064,"mem_gib":40,"gpu":1,"gpu_mem_mib":24564,"node_count":1}
```

---

## Query API

| Endpoint | Description |
|----------|-------------|
| `GET /resources` | Total available CPU/GPU/Mem across all nodes |
| `GET /nodes` | Per-node breakdown with reputation scores |

Example:
```bash
curl http://localhost:8080/nodes
```
```json
[
  {
    "node_id": "node-1",
    "provider": "akash1...",
    "free_cpu": 15700,
    "free_mem": 43945304064,
    "free_gpu": 1,
    "free_gpu_mem_mb": 24564,
    "reputation_score": 100
  }
]
```

---

## Helm values.yaml Reference

| Key | Default | Description |
|-----|---------|-------------|
| `akash.hostDataPath` | `/home/lmdrive/.akash-local` | Chain data directory on the host |
| `akash.chainID` | `local-test` | Chain ID |
| `monitor.providerAddr` | `""` | Provider bech32 address (required) |
| `monitor.keyName` | `mykey` | Key name in keyring |
| `monitor.keyringBackend` | `test` | Keyring backend |
| `monitor.k8sNodeName` | `""` | K8s node name for resource accounting; defaults to hostname |
| `monitor.heartbeat` | `30s` | Heartbeat interval |
| `monitor.podNamespace` | `default` | Namespace for scheduled workload Pods |
| `query.port` | `8080` | HTTP port for ck8s-query |
| `query.service.type` | `ClusterIP` | Set `NodePort` for external access |

---

## Troubleshooting

**heartbeat tx error: errUnknownField TagNum: 6**
The node binary and monitor binary versions are mismatched. Redeploy both from the same image tag.

**monitor CrashLoopBackOff**
The akash gRPC port is not ready yet. The init container waits for it automatically — wait a minute for `akash-0` to start.

**gpu=0 in heartbeat**
Check that koordinator device plugin is running and the node has `koordinator.sh/gpu` in `kubectl describe node`.

**reputation_score=0**
The node was slashed for missing heartbeats. Reputation recovers automatically: each successful heartbeat reduces slash count by 1.
