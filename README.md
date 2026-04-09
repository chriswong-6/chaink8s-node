# chaink8s-node

Akash Network node with the `x/chaink8s` on-chain Kubernetes scheduler module.

The `chaink8s` module lets GPU/CPU providers register nodes on-chain, schedule workloads to K8s, and track resource availability transparently.

## Components

| Component | Description |
|-----------|-------------|
| `akash` | Modified Akash node binary with chaink8s module |
| `ck8s-monitor` | Runs on each K8s node; sends heartbeats and manages Pods |
| `ck8s-query` | HTTP API to query available CPU/GPU resources |

## Step 1: Initialize Chain Data (once per machine)

Run these on the host machine before deploying. The data directory is mounted into the K8s pod via hostPath.

```bash
# Pull the akash binary from our image to run init commands
docker run --rm -v /root/.akash-local:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash init mynode --chain-id local-test --home /home/akash

# Create a key (save the mnemonic!)
docker run --rm -v /root/.akash-local:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash keys add mykey --home /home/akash --keyring-backend test

# Fund the account in genesis
ADDR=$(docker run --rm -v /root/.akash-local:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash keys show mykey -a --home /home/akash --keyring-backend test)

docker run --rm -v /root/.akash-local:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash genesis add-genesis-account $ADDR 10000000000uakt --home /home/akash

docker run --rm -v /root/.akash-local:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash genesis gentx mykey 1000000uakt --chain-id local-test --home /home/akash --keyring-backend test

docker run --rm -v /root/.akash-local:/home/akash \
  ghcr.io/chriswong-6/chaink8s-node:main \
  akash genesis collect-gentxs --home /home/akash

# Register as provider (save the address printed here)
PROVIDER_ADDR=$ADDR
echo "Provider address: $PROVIDER_ADDR"
```

## Step 2: Deploy with Helm

**Prerequisites:** K8s cluster, `kubectl`, `helm`

```bash
helm repo add chaink8s https://chriswong-6.github.io/chaink8s-node
helm repo update

helm install ck8s chaink8s/ck8s \
  --namespace ck8s --create-namespace \
  --set akash.hostDataPath=/root/.akash-local \
  --set akash.chainID=local-test \
  --set monitor.providerAddr=<your-provider-bech32-address> \
  --set monitor.keyName=mykey \
  --set monitor.k8sNodeName=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
```

This deploys:
- Akash node (StatefulSet) using data from `hostDataPath`
- ck8s-monitor (Deployment) sending heartbeats every 30s
- ck8s-query (Deployment) serving HTTP on port 8080

## Query Resources

```bash
# Available CPU/GPU across all nodes
curl http://<ck8s-query-svc>:8080/resources

# Per-node breakdown
curl http://<ck8s-query-svc>:8080/nodes
```

## values.yaml Reference

| Key | Default | Description |
|-----|---------|-------------|
| `akash.hostDataPath` | `/home/lmdrive/.akash-local` | Chain data directory on host |
| `akash.chainID` | `local-test` | Chain ID |
| `monitor.providerAddr` | `""` | Provider bech32 address (required) |
| `monitor.keyName` | `mykey` | Key name in keyring |
| `monitor.k8sNodeName` | `""` | K8s node name for resource accounting (defaults to hostname) |
| `monitor.heartbeat` | `30s` | Heartbeat interval |
| `query.port` | `8080` | HTTP port for ck8s-query |

## Architecture

```
┌─────────────────────────────────────────────┐
│                K8s Cluster                  │
│  ┌──────────┐  ┌───────────┐  ┌──────────┐ │
│  │  akash   │  │ck8s-      │  │ck8s-     │ │
│  │  node    │←─│monitor    │  │query     │ │
│  │(chaink8s)│  │heartbeat  │  │HTTP API  │ │
│  └──────────┘  │pod mgmt   │  └──────────┘ │
│                └───────────┘                │
└─────────────────────────────────────────────┘
```

- `ck8s-monitor` reads K8s node allocatable resources, subtracts existing pod usage, sends heartbeat tx to chain
- `akash` node runs the chaink8s EndBlock scheduler: binds open orders to best available node
- `ck8s-monitor` watches chain for bound orders and creates/deletes K8s Pods accordingly
- `ck8s-query` exposes gRPC state over HTTP for external tooling
