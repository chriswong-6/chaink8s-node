// ck8s-monitor — Provider-side ChainK8s Monitor daemon
//
// Connects to the local Akash chain, periodically broadcasts MsgNodeHeartbeat
// to keep the node registered, and listens for "chaink8s_bind" events to
// create K8s Pods for scheduled Orders.
//
// Usage:
//
//	ck8s-monitor \
//	  --provider  akash1yd9vah8q5vajuyxntmqnlgch55x883h05x0hlu \
//	  --node-id   node-1 \
//	  --key-name  mykey \
//	  [--heartbeat 30s] \
//	  [--image nginx:alpine] \
//	  [--kubeconfig ~/.kube/config]
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pkg.akt.dev/node/x/chaink8s/monitor"
)

func main() {
	log.SetFlags(log.Ltime | log.Lshortfile)

	var (
		nodeRPC        = flag.String("rpc", "http://localhost:26657", "CometBFT RPC endpoint")
		nodeGRPC       = flag.String("grpc", "localhost:9190", "Cosmos gRPC endpoint")
		nodeREST       = flag.String("rest", "http://localhost:1317", "Cosmos REST endpoint")
		chainID        = flag.String("chain-id", "local-test", "Chain ID")
		providerAddr   = flag.String("provider", "", "Provider bech32 address (required)")
		nodeID         = flag.String("node-id", "", "Node identifier / hostname (required)")
		keyName        = flag.String("key-name", "mykey", "Keyring key name")
		keyringDir     = flag.String("keyring-dir", "/home/lmdrive/.akash-local", "Keyring directory")
		keyringBackend = flag.String("keyring-backend", "test", "Keyring backend: test|os|file")
		heartbeat      = flag.Duration("heartbeat", 30*time.Second, "Heartbeat interval")
		defaultImage   = flag.String("image", "nginx:alpine", "Default K8s Pod image for scheduled workloads")
		podNamespace   = flag.String("namespace", "default", "K8s namespace to create Pods in")
		kubeconfig     = flag.String("kubeconfig", "", "Path to kubeconfig (empty = in-cluster or ~/.kube/config)")
		k8sNode        = flag.String("k8s-node", "", "K8s node name (kubectl get nodes); defaults to os.Hostname()")
	)
	flag.Parse()

	if *providerAddr == "" {
		log.Fatal("--provider is required")
	}
	if *nodeID == "" {
		h, _ := os.Hostname()
		*nodeID = h
		log.Printf("INF --node-id not set, using hostname: %s", *nodeID)
	}

	cfg := monitor.Config{
		NodeRPC:         *nodeRPC,
		NodeGRPC:        *nodeGRPC,
		NodeREST:        *nodeREST,
		ChainID:         *chainID,
		ProviderAddr:    *providerAddr,
		NodeID:          *nodeID,
		K8sNodeName:     *k8sNode,
		KeyName:         *keyName,
		KeyringDir:      *keyringDir,
		KeyringBackend:  *keyringBackend,
		HeartbeatPeriod: *heartbeat,
		DefaultImage:    *defaultImage,
		PodNamespace:    *podNamespace,
		Kubeconfig:      *kubeconfig,
	}

	m, err := monitor.New(cfg)
	if err != nil {
		log.Fatalf("monitor init: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Println("=== ck8s-monitor starting ===")
	if err := m.Run(ctx); err != nil {
		log.Fatalf("monitor: %v", err)
	}
	log.Println("=== ck8s-monitor stopped ===")
}
