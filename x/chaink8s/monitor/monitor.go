// Package monitor implements the Provider-side ChainK8s Monitor.
//
// Responsibilities:
//  1. Heartbeat loop  — periodically broadcast MsgNodeHeartbeat to keep the
//     node visible to the on-chain scheduler.
//  2. Event listener  — subscribe to CometBFT new-block events and react to
//     "chaink8s_bind" events that match this provider+node.
//  3. Pod execution   — when a bind event arrives, create a K8s Pod that
//     satisfies the resource requirements of the scheduled Order.
package monitor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	cmthttp "github.com/cometbft/cometbft/rpc/client/http"
	ctypes "github.com/cometbft/cometbft/rpc/core/types"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"pkg.akt.dev/go/sdkutil"
	"pkg.akt.dev/node/app"
	ck8stypes "pkg.akt.dev/node/x/chaink8s/types"
)

// Config holds all runtime configuration for the Monitor.
type Config struct {
	// Chain connection
	NodeRPC  string // CometBFT RPC endpoint, e.g. "http://localhost:26657"
	NodeGRPC string // Cosmos gRPC endpoint, e.g. "localhost:9190"
	NodeREST string // Cosmos REST endpoint, e.g. "http://localhost:1317"
	ChainID  string

	// Signing
	KeyName        string
	KeyringDir     string
	KeyringBackend string // "test" | "os" | "file"

	// Provider identity
	ProviderAddr string // bech32 address
	NodeID       string // chain logical node identifier (used in MsgNodeHeartbeat)
	K8sNodeName  string // actual K8s node name (kubectl get nodes); defaults to os.Hostname()

	// Behaviour
	HeartbeatPeriod time.Duration // default 30s
	DefaultImage    string        // K8s Pod image for scheduled workloads
	PodNamespace    string        // K8s namespace to create Pods in

	// K8s auth
	Kubeconfig string // path to kubeconfig; empty → in-cluster → ~/.kube/config
}

// Monitor is the Provider-side daemon that bridges chain events to K8s.
type Monitor struct {
	cfg      Config
	rpc      *cmthttp.HTTP
	k8s      kubernetes.Interface
	grpcConn *grpc.ClientConn

	// Cosmos SDK signing helpers (initialised once in New)
	clientCtx client.Context
	factory   tx.Factory
}

// New creates and connects a Monitor but does not start it yet.
func New(cfg Config) (*Monitor, error) {
	if cfg.HeartbeatPeriod == 0 {
		cfg.HeartbeatPeriod = 30 * time.Second
	}
	if cfg.DefaultImage == "" {
		cfg.DefaultImage = "nginx:alpine"
	}
	if cfg.PodNamespace == "" {
		cfg.PodNamespace = "default"
	}
	if cfg.K8sNodeName == "" {
		if h, err := os.Hostname(); err == nil {
			cfg.K8sNodeName = h
		} else {
			cfg.K8sNodeName = cfg.NodeID
		}
	}

	// ── CometBFT RPC client ──────────────────────────────────────────────
	rpc, err := cmthttp.New(cfg.NodeRPC, "/websocket")
	if err != nil {
		return nil, fmt.Errorf("monitor: rpc client: %w", err)
	}

	// ── Cosmos SDK encoding + keyring ────────────────────────────────────
	encCfg := makeEncCfg()

	backend := cfg.KeyringBackend
	if backend == "" {
		backend = keyring.BackendTest
	}
	kr, err := keyring.New("akash", backend, cfg.KeyringDir, os.Stdin, encCfg.Codec)
	if err != nil {
		return nil, fmt.Errorf("monitor: keyring: %w", err)
	}

	// ── gRPC connection ──────────────────────────────────────────────────
	grpcConn, err := grpc.Dial(cfg.NodeGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("monitor: grpc dial: %w", err)
	}

	// ── Fetch initial account info ───────────────────────────────────────
	authClient := authtypes.NewQueryClient(grpcConn)
	accResp, err := authClient.Account(context.Background(), &authtypes.QueryAccountRequest{
		Address: cfg.ProviderAddr,
	})
	if err != nil {
		return nil, fmt.Errorf("monitor: query account %s: %w", cfg.ProviderAddr, err)
	}
	var baseAcc authtypes.AccountI
	if err := encCfg.InterfaceRegistry.UnpackAny(accResp.Account, &baseAcc); err != nil {
		return nil, fmt.Errorf("monitor: unpack account: %w", err)
	}

	clientCtx := client.Context{}.
		WithChainID(cfg.ChainID).
		WithCodec(encCfg.Codec).
		WithInterfaceRegistry(encCfg.InterfaceRegistry).
		WithTxConfig(encCfg.TxConfig).
		WithKeyring(kr).
		WithNodeURI(cfg.NodeRPC).
		WithGRPCClient(grpcConn)

	factory := tx.Factory{}.
		WithChainID(cfg.ChainID).
		WithKeybase(kr).
		WithTxConfig(encCfg.TxConfig).
		WithAccountNumber(baseAcc.GetAccountNumber()).
		WithSequence(baseAcc.GetSequence()).
		WithGas(200000).
		WithFees("500uakt").
		WithSignMode(signing.SignMode_SIGN_MODE_DIRECT)

	// ── K8s client ───────────────────────────────────────────────────────
	k8sClient, err := newK8sClient(cfg.Kubeconfig)
	if err != nil {
		// K8s is optional; log warning but don't fail
		log.Printf("WRN monitor: k8s client unavailable (%v) — bind events will be logged only", err)
		k8sClient = nil
	}

	return &Monitor{
		cfg:       cfg,
		rpc:       rpc,
		k8s:       k8sClient,
		grpcConn:  grpcConn,
		clientCtx: clientCtx,
		factory:   factory,
	}, nil
}

// Run starts the heartbeat loop and chain event listener.
// Blocks until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) error {
	if err := m.rpc.Start(); err != nil {
		return fmt.Errorf("monitor: rpc start: %w", err)
	}
	defer m.rpc.Stop() //nolint:errcheck

	blockEvents, err := m.rpc.Subscribe(ctx, "ck8s-monitor-block", "tm.event='NewBlock'", 512)
	if err != nil {
		return fmt.Errorf("monitor: subscribe newblock: %w", err)
	}

	// Subscribe to Tx events to catch deployment/order close events
	txEvents, err := m.rpc.Subscribe(ctx, "ck8s-monitor-tx", "tm.event='Tx'", 512)
	if err != nil {
		return fmt.Errorf("monitor: subscribe tx: %w", err)
	}

	ticker := time.NewTicker(m.cfg.HeartbeatPeriod)
	defer ticker.Stop()

	// 启动对账：补建重启前已调度但 Pod 不存在的工作负载
	m.reconcileBoundOrders(ctx)

	// Send first heartbeat immediately
	m.sendHeartbeat(ctx)

	log.Printf("INF monitor: running  provider=%s  node=%s  heartbeat=%s",
		m.cfg.ProviderAddr, m.cfg.NodeID, m.cfg.HeartbeatPeriod)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.sendHeartbeat(ctx)
		case ev, ok := <-blockEvents:
			if !ok {
				return fmt.Errorf("monitor: block event channel closed")
			}
			m.handleBlock(ev)
		case ev, ok := <-txEvents:
			if !ok {
				return fmt.Errorf("monitor: tx event channel closed")
			}
			m.handleTx(ev)
		}
	}
}

// ── Heartbeat ────────────────────────────────────────────────────────────────

func (m *Monitor) sendHeartbeat(ctx context.Context) {
	cpu, mem, gpu, gpuMemMB := m.readNodeResources(ctx)
	msg := ck8stypes.NewMsgNodeHeartbeat(
		mustAccAddr(m.cfg.ProviderAddr),
		m.cfg.NodeID,
		cpu, mem, gpu, gpuMemMB,
	)

	// Sync sequence from chain before signing
	m.refreshSequence()

	txBytes, err := m.signTx(ctx, msg)
	if err != nil {
		log.Printf("ERR monitor: sign heartbeat: %v", err)
		return
	}

	code, txhash, rawlog := m.broadcastTx(txBytes)
	if code != 0 {
		log.Printf("ERR monitor: heartbeat tx code=%d log=%s", code, rawlog)
		return
	}
	// Optimistically increment local sequence for the next tx
	m.factory = m.factory.WithSequence(m.factory.Sequence() + 1)
	log.Printf("INF monitor: heartbeat ok  txhash=%.12s  cpu=%d  mem=%d  gpu=%d  gpu_mem=%dMi", txhash, cpu, mem, gpu, gpuMemMB)
}

// ── Chain event handling ──────────────────────────────────────────────────────

func (m *Monitor) handleBlock(ev ctypes.ResultEvent) {
	// CometBFT WebSocket delivers finalize_block_events as flattened
	// ev.Events map: "type.attr" → []string

	// chaink8s_bind → create Pod
	if attrs := parseEventAttrs(ev.Events, "chaink8s_bind"); len(attrs) > 0 {
		provider := attrs["provider"]
		nodeID := attrs["node_id"]
		orderID := attrs["order_id"]
		if provider == m.cfg.ProviderAddr && nodeID == m.cfg.NodeID {
			reqGPU      := parseAttrInt64(attrs, "req_gpu")
			reqGPUCore  := parseAttrInt64(attrs, "req_gpu_core")
			reqGPUMemMB := parseAttrInt64(attrs, "req_gpu_mem_mb")
			image       := attrs["image"]
			log.Printf("INF monitor: chaink8s_bind  order=%s  provider=%s  node=%s  gpu=%d  gpu-core=%d  gpu-mem=%dMi  image=%s",
				orderID, provider, nodeID, reqGPU, reqGPUCore, reqGPUMemMB, image)
			m.executePod(orderID, reqGPU, reqGPUCore, reqGPUMemMB, image)
		}
	}

	// chaink8s_unbind → delete Pod
	if attrs := parseEventAttrs(ev.Events, "chaink8s_unbind"); len(attrs) > 0 {
		orderID := attrs["order_id"]
		reason := attrs["reason"]
		if orderID != "" {
			log.Printf("INF monitor: chaink8s_unbind  order=%s  reason=%s", orderID, reason)
			dseq, gseq, _ := parseOrderIDParts(orderID)
			if dseq != "" {
				m.deletePodsForGroup(dseq, gseq)
			}
		}
	}
}

// handleTx processes Tx events to detect deployment/order/group close events
// and delete the corresponding K8s Pods.
//
// Relevant event types:
//   - akash.deployment.v1.EventDeploymentClosed → delete all pods for that dseq
//   - akash.deployment.v1.EventGroupClosed      → delete pods for dseq+gseq
//   - akash.market.v1.EventOrderClosed          → delete pod for dseq+gseq+oseq
func (m *Monitor) handleTx(ev ctypes.ResultEvent) {
	// EventDeploymentClosed: id is JSON {"owner":"...","dseq":"173"}
	if attrs := parseEventAttrs(ev.Events, "akash.deployment.v1.EventDeploymentClosed"); len(attrs) > 0 {
		if id := attrs["id"]; id != "" {
			var idMap map[string]interface{}
			if err := json.Unmarshal([]byte(id), &idMap); err == nil {
				dseq := jsonStr(idMap, "dseq")
				owner := jsonStr(idMap, "owner")
				if dseq != "" {
					log.Printf("INF monitor: EventDeploymentClosed  owner=%s dseq=%s", owner, dseq)
					m.deletePodsForDeployment(dseq)
				}
			}
		}
	}

	// EventGroupClosed: id is JSON {"owner":"...","dseq":"173","gseq":1}
	if attrs := parseEventAttrs(ev.Events, "akash.deployment.v1.EventGroupClosed"); len(attrs) > 0 {
		if id := attrs["id"]; id != "" {
			var idMap map[string]interface{}
			if err := json.Unmarshal([]byte(id), &idMap); err == nil {
				dseq := jsonStr(idMap, "dseq")
				gseq := jsonStr(idMap, "gseq")
				if dseq != "" {
					log.Printf("INF monitor: EventGroupClosed  dseq=%s gseq=%s", dseq, gseq)
					m.deletePodsForGroup(dseq, gseq)
				}
			}
		}
	}

	// EventOrderClosed: id is JSON {"owner":"...","dseq":"...","gseq":...,"oseq":...}
	if attrs := parseEventAttrs(ev.Events, "akash.market.v1.EventOrderClosed"); len(attrs) > 0 {
		if id := attrs["id"]; id != "" {
			var idMap map[string]interface{}
			if err := json.Unmarshal([]byte(id), &idMap); err == nil {
				dseq := jsonStr(idMap, "dseq")
				gseq := jsonStr(idMap, "gseq")
				oseq := jsonStr(idMap, "oseq")
				if dseq != "" {
					log.Printf("INF monitor: EventOrderClosed  dseq=%s gseq=%s oseq=%s", dseq, gseq, oseq)
					m.deletePodsForGroup(dseq, gseq)
				}
			}
		}
	}
}

// parseEventAttrs extracts attributes for a given event type from the
// CometBFT ResultEvent.Events map (keys are "type.attr" → []string).
func parseEventAttrs(events map[string][]string, eventType string) map[string]string {
	prefix := eventType + "."
	out := make(map[string]string)
	for k, vals := range events {
		if strings.HasPrefix(k, prefix) && len(vals) > 0 {
			attr := strings.TrimPrefix(k, prefix)
			out[attr] = vals[0]
		}
	}
	return out
}

// jsonStr safely extracts a string from a JSON-decoded map, handling both
// string and numeric types (e.g. dseq may come as "173" or 173).
func jsonStr(m map[string]interface{}, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// ── Pod execution ─────────────────────────────────────────────────────────────

// executePod creates a K8s Pod for the given order.
// orderID format: "akash1.../dseq/gseq/oseq"
//
// image 为用户在 SDL placement attributes 中指定的容器镜像；
// 为空时回退到 m.cfg.DefaultImage。
//
// GPU 模式选择：
//   - reqGPUCore>0：分数模式，直接写 koordinator.sh/gpu-core + gpu-memory，bypass webhook
//   - reqGPUCore=0，reqGPU>0：整块模式，写 nvidia.com/gpu:N，由 webhook 转换
func (m *Monitor) executePod(orderID string, reqGPU, reqGPUCore, reqGPUMemMB int64, image string) {
	if m.k8s == nil {
		log.Printf("WRN monitor: K8s client not available — skipping pod creation for order %s", orderID)
		return
	}

	if image == "" {
		image = m.cfg.DefaultImage
	}

	podName := orderIDToPodName(orderID)
	dseq, gseq, _ := parseOrderIDParts(orderID)

	labels := map[string]string{
		"app":           "ck8s-workload",
		"ck8s-order":    sanitizeLabel(orderID),
		"ck8s-provider": sanitizeLabel(m.cfg.ProviderAddr),
		"ck8s-node":     m.cfg.NodeID,
		"ck8s-dseq":     dseq,
		"ck8s-gseq":     gseq,
	}
	// 分数模式需要告知 koordinator 使用 HAMi-core vGPU 隔离
	if reqGPUCore > 0 {
		labels["koordinator.sh/gpu-isolation-provider"] = "HAMi-core"
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: m.cfg.PodNamespace,
			Labels:    labels,
			Annotations: map[string]string{
				"ck8s/order-id":       orderID,
				"ck8s/provider":       m.cfg.ProviderAddr,
				"ck8s/scheduled":      time.Now().UTC().Format(time.RFC3339),
				"ck8s/req-gpu-core":   fmt.Sprintf("%d", reqGPUCore),
				"ck8s/req-gpu-mem-mb": fmt.Sprintf("%d", reqGPUMemMB),
			},
		},
		Spec: corev1.PodSpec{
			// 分数模式（reqGPUCore>0）直接用 koordinator 资源，必须指定 koord-scheduler
			// 整块模式由 phantom-webhook 注入 schedulerName
			SchedulerName: func() string {
				if reqGPUCore > 0 {
					return "koord-scheduler"
				}
				return ""
			}(),
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				buildContainer(image, reqGPU, reqGPUCore, reqGPUMemMB),
			},
		},
	}

	created, err := m.k8s.CoreV1().Pods(m.cfg.PodNamespace).Create(
		context.Background(), pod, metav1.CreateOptions{},
	)
	if err != nil {
		// Pod already exists is idempotent — not an error
		if strings.Contains(err.Error(), "already exists") {
			log.Printf("INF monitor: pod already exists  name=%s  order=%s (skipping)", podName, orderID)
			return
		}
		log.Printf("ERR monitor: create pod %s: %v", podName, err)
		return
	}
	log.Printf("INF monitor: pod created  name=%s  namespace=%s  order=%s",
		created.Name, created.Namespace, orderID)
}

// ── Startup reconciliation ────────────────────────────────────────────────────

// reconcileBoundOrders 在 Monitor 启动时对账：
// 查询链上属于本 provider+node 的所有 BoundOrderInfo，
// 对比 K8s 中已存在的 Pod，缺失的补建。
// 保证 Monitor 重启后不丢失已调度的工作负载。
func (m *Monitor) reconcileBoundOrders(ctx context.Context) {
	if m.k8s == nil {
		return
	}

	// 1. 查链上已绑定的 Order
	qc := ck8stypes.NewQueryClient(m.grpcConn)
	resp, err := qc.BoundOrders(ctx, &ck8stypes.QueryBoundOrdersRequest{})
	if err != nil {
		log.Printf("WRN monitor: reconcile: query bound orders: %v", err)
		return
	}

	// 2. 列出 K8s 中本 provider 的所有 Pod，建立 orderID → 是否存活 的集合
	pods, err := m.k8s.CoreV1().Pods(m.cfg.PodNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app=ck8s-workload,ck8s-provider=%s",
			sanitizeLabel(m.cfg.ProviderAddr)),
	})
	if err != nil {
		log.Printf("WRN monitor: reconcile: list pods: %v", err)
		return
	}
	existing := make(map[string]bool, len(pods.Items))
	for _, pod := range pods.Items {
		if id, ok := pod.Annotations["ck8s/order-id"]; ok {
			// 只计入非终态 Pod（Running / Pending）
			if pod.Status.Phase != corev1.PodFailed && pod.Status.Phase != corev1.PodSucceeded {
				existing[id] = true
			}
		}
	}

	// 3. 补建缺失的 Pod
	recreated := 0
	for _, order := range resp.Orders {
		if order.Provider != m.cfg.ProviderAddr || order.NodeID != m.cfg.NodeID {
			continue
		}
		if existing[order.OrderID] {
			continue
		}
		log.Printf("INF monitor: reconcile: recreating pod  order=%s  gpu=%d  gpu-core=%d  gpu-mem=%dMi  image=%s",
			order.OrderID, order.ReqGPU, order.ReqGPUCore, order.ReqGPUMemMB, order.Image)
		m.executePod(order.OrderID, order.ReqGPU, order.ReqGPUCore, order.ReqGPUMemMB, order.Image)
		recreated++
	}

	log.Printf("INF monitor: reconcile done  checked=%d  recreated=%d",
		len(resp.Orders), recreated)
}

// ── Pod deletion ──────────────────────────────────────────────────────────────

// deletePodsForDeployment deletes all Pods with ck8s-dseq=dseq label.
// Called when an entire deployment is closed.
func (m *Monitor) deletePodsForDeployment(dseq string) {
	if m.k8s == nil {
		return
	}
	selector := fmt.Sprintf("ck8s-dseq=%s,ck8s-provider=%s", dseq, sanitizeLabel(m.cfg.ProviderAddr))
	m.deletePodsWithSelector(selector)
}

// deletePodsForGroup deletes all Pods with ck8s-dseq+ck8s-gseq labels.
// Called when a deployment group or order is closed.
func (m *Monitor) deletePodsForGroup(dseq, gseq string) {
	if m.k8s == nil {
		return
	}
	selector := fmt.Sprintf("ck8s-dseq=%s,ck8s-gseq=%s,ck8s-provider=%s",
		dseq, gseq, sanitizeLabel(m.cfg.ProviderAddr))
	m.deletePodsWithSelector(selector)
}

func (m *Monitor) deletePodsWithSelector(selector string) {
	pods, err := m.k8s.CoreV1().Pods(m.cfg.PodNamespace).List(
		context.Background(),
		metav1.ListOptions{LabelSelector: selector},
	)
	if err != nil {
		log.Printf("ERR monitor: list pods selector=%q: %v", selector, err)
		return
	}
	if len(pods.Items) == 0 {
		log.Printf("INF monitor: no pods found for selector=%q", selector)
		return
	}
	policy := metav1.DeletePropagationForeground
	for _, pod := range pods.Items {
		err := m.k8s.CoreV1().Pods(m.cfg.PodNamespace).Delete(
			context.Background(),
			pod.Name,
			metav1.DeleteOptions{PropagationPolicy: &policy},
		)
		if err != nil {
			log.Printf("ERR monitor: delete pod %s: %v", pod.Name, err)
			continue
		}
		log.Printf("INF monitor: pod deleted  name=%s  selector=%q", pod.Name, selector)
	}
}

// ── Tx helpers ────────────────────────────────────────────────────────────────

func (m *Monitor) signTx(ctx context.Context, msg sdk.Msg) ([]byte, error) {
	builder, err := m.factory.BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("build unsigned tx: %w", err)
	}
	if err := tx.Sign(ctx, m.factory, m.cfg.KeyName, builder, true); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return m.clientCtx.TxConfig.TxEncoder()(builder.GetTx())
}

func (m *Monitor) broadcastTx(txBytes []byte) (code int, txhash, rawlog string) {
	body := fmt.Sprintf(`{"tx_bytes":%q,"mode":"BROADCAST_MODE_SYNC"}`,
		base64.StdEncoding.EncodeToString(txBytes))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.cfg.NodeREST+"/cosmos/tx/v1beta1/txs",
		bytes.NewBufferString(body))
	if err != nil {
		log.Printf("ERR monitor: broadcast build request: %v", err)
		return 1, "", err.Error()
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("ERR monitor: broadcast http: %v", err)
		return 1, "", err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		b, _ := io.ReadAll(resp.Body)
		log.Printf("ERR monitor: broadcast server error %d: %s", resp.StatusCode, string(b))
		return 1, "", fmt.Sprintf("server error %d", resp.StatusCode)
	}

	b, _ := io.ReadAll(resp.Body)
	var result struct {
		TxResponse struct {
			Code   int    `json:"code"`
			TxHash string `json:"txhash"`
			RawLog string `json:"raw_log"`
		} `json:"tx_response"`
	}
	if err := json.Unmarshal(b, &result); err != nil {
		log.Printf("ERR monitor: broadcast parse response: %v", err)
		return 1, "", err.Error()
	}
	return result.TxResponse.Code, result.TxResponse.TxHash, result.TxResponse.RawLog
}

// refreshSequence re-reads the on-chain sequence number so the monitor
// stays in sync after restarts or failed broadcasts.
func (m *Monitor) refreshSequence() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	authClient := authtypes.NewQueryClient(m.grpcConn)
	accResp, err := authClient.Account(ctx, &authtypes.QueryAccountRequest{
		Address: m.cfg.ProviderAddr,
	})
	if err != nil {
		log.Printf("WRN monitor: refreshSequence: %v", err)
		return
	}
	encCfg := makeEncCfg()
	var baseAcc authtypes.AccountI
	if err := encCfg.InterfaceRegistry.UnpackAny(accResp.Account, &baseAcc); err != nil {
		log.Printf("WRN monitor: refreshSequence unpack: %v", err)
		return
	}
	m.factory = m.factory.WithSequence(baseAcc.GetSequence())
}

// ── System resource reading ───────────────────────────────────────────────────

// readNodeResources returns the capacity available for chain scheduling.
//
// 链上只做总容量记账：上报「K8s allocatable - 非 chain Pod 已请求量」。
// GPU 信息直接从 koordinator 扩展资源读取，不依赖 nvidia-smi：
//   koordinator.sh/gpu         → GPU 总 core 单位（100 = 1块GPU）
//   koordinator.sh/gpu-memory  → GPU 总显存（如 "24564Mi"）
func (m *Monitor) readNodeResources(ctx context.Context) (cpuMilli, memBytes, gpuCount, gpuMemMB int64) {
	// ── 1. 获取 K8s 节点 allocatable ───────────────────────────────────────
	node, err := m.k8s.CoreV1().Nodes().Get(ctx, m.cfg.K8sNodeName, metav1.GetOptions{})
	if err != nil {
		log.Printf("WRN readNodeResources: get node %s: %v; falling back to /proc", m.cfg.K8sNodeName, err)
		cpuMilli, memBytes = readNodeResourcesFallback()
		return
	}

	allocCPU := node.Status.Allocatable.Cpu().MilliValue()
	allocMem := node.Status.Allocatable.Memory().Value()

	// ── 2. GPU：从 koordinator 扩展资源读取（不需要 nvidia-smi）────────────
	if gpuCoreQ, ok := node.Status.Allocatable["koordinator.sh/gpu"]; ok {
		// 100 core 单位 = 1 块物理 GPU
		gpuCount = gpuCoreQ.Value() / 100
	}
	if gpuMemQ, ok := node.Status.Allocatable["koordinator.sh/gpu-memory"]; ok {
		gpuMemMB = gpuMemQ.Value() / (1024 * 1024) // bytes → MiB
	}

	// ── 3. 列出本节点所有 Pod，汇总非 chain Pod 的 CPU/Mem requests ─────────
	pods, err := m.k8s.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: "spec.nodeName=" + m.cfg.K8sNodeName,
	})
	if err != nil {
		log.Printf("WRN readNodeResources: list pods: %v; using allocatable as-is", err)
		cpuMilli, memBytes = allocCPU, allocMem
		return
	}

	var extCPU, extMem int64
	for i := range pods.Items {
		pod := &pods.Items[i]
		if strings.HasPrefix(pod.Name, "ck8s-") {
			continue
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		for _, c := range pod.Spec.Containers {
			extCPU += c.Resources.Requests.Cpu().MilliValue()
			extMem += c.Resources.Requests.Memory().Value()
		}
	}

	log.Printf("DBG readNodeResources: node=%s cpu=%dm mem=%dMi gpu=%d gpuMem=%dMi pods=%d",
		m.cfg.K8sNodeName, allocCPU-extCPU, (allocMem-extMem)>>20, gpuCount, gpuMemMB, len(pods.Items))

	cpuMilli = allocCPU - extCPU
	memBytes = allocMem - extMem
	if cpuMilli < 0 {
		cpuMilli = 0
	}
	if memBytes < 0 {
		memBytes = 0
	}
	return
}

// readNodeResourcesFallback reads /proc/meminfo MemTotal when K8s is unavailable.
func readNodeResourcesFallback() (cpuMilli, memBytes int64) {
	cpuMilli = int64(runtime.NumCPU()) * 1000

	f, err := os.Open("/proc/meminfo")
	if err != nil {
		memBytes = 4 << 30
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, _ := strconv.ParseInt(fields[1], 10, 64)
				memBytes = kb * 1024
			}
			break
		}
	}
	if memBytes == 0 {
		memBytes = 4 << 30
	}
	return
}

// ── Internal helpers ──────────────────────────────────────────────────────────

func makeEncCfg() sdkutil.EncodingConfig {
	var basics []module.AppModuleBasic
	for _, b := range app.ModuleBasics() {
		basics = append(basics, b)
	}
	encCfg := sdkutil.MakeEncodingConfig(basics...)
	encCfg.InterfaceRegistry.RegisterImplementations((*sdk.Msg)(nil),
		&ck8stypes.MsgNodeHeartbeat{},
		&ck8stypes.MsgNodeClaim{},
	)
	return encCfg
}

func newK8sClient(kubeconfig string) (kubernetes.Interface, error) {
	var cfg *rest.Config
	var err error
	switch {
	case kubeconfig != "":
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	default:
		cfg, err = rest.InClusterConfig()
		if err != nil {
			cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		}
	}
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

func mustAccAddr(bech32 string) sdk.AccAddress {
	addr, err := sdk.AccAddressFromBech32(bech32)
	if err != nil {
		panic(err)
	}
	return addr
}

// orderIDToPodName converts an Akash OrderID string (owner/dseq/gseq/oseq)
// to a valid DNS-1123 pod name.
func orderIDToPodName(orderID string) string {
	parts := strings.Split(orderID, "/")
	if len(parts) >= 4 {
		// Use last 3 parts: dseq-gseq-oseq
		return "ck8s-" + strings.Join(parts[len(parts)-3:], "-")
	}
	safe := strings.NewReplacer("/", "-", ".", "-").Replace(orderID)
	if len(safe) > 60 {
		safe = safe[len(safe)-60:]
	}
	return "ck8s-" + safe
}

// parseOrderIDParts extracts dseq, gseq, oseq from an OrderID string.
// Format: "owner/dseq/gseq/oseq"
func parseOrderIDParts(orderID string) (dseq, gseq, oseq string) {
	parts := strings.Split(orderID, "/")
	if len(parts) >= 4 {
		return parts[len(parts)-3], parts[len(parts)-2], parts[len(parts)-1]
	}
	return "", "", ""
}

// sanitizeLabel makes a value safe for K8s label values (max 63 chars).
func sanitizeLabel(v string) string {
	v = strings.NewReplacer("/", "-", ".", "-").Replace(v)
	if len(v) > 63 {
		v = v[len(v)-63:]
	}
	return v
}

// buildContainer builds a K8s container spec.
//
// GPU 资源模式（两种互斥）：
//   - 分数模式（reqGPUCore>0）：直接写 koordinator.sh/gpu-core + gpu-memory
//     koord-scheduler 按百分比调度，HAMi 注入 NVIDIA_VISIBLE_DEVICES
//   - 整块模式（reqGPUCore=0，reqGPU>0）：写 nvidia.com/gpu:N
//     phantom-webhook 转换为 gpu-core:N×100 + gpu-memory（全显存）
func buildContainer(image string, reqGPU, reqGPUCore, reqGPUMemMB int64) corev1.Container {
	limits := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("500m"),
		corev1.ResourceMemory: resource.MustParse("256Mi"),
	}
	requests := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("100m"),
		corev1.ResourceMemory: resource.MustParse("128Mi"),
	}

	if reqGPUCore > 0 {
		// 分数模式：直接写 koordinator 格式，bypass webhook 转换
		coreQty := resource.MustParse(fmt.Sprintf("%d", reqGPUCore))
		memQty  := resource.MustParse(fmt.Sprintf("%dMi", reqGPUMemMB))
		one     := resource.MustParse("1")
		limits["koordinator.sh/gpu-core"]   = coreQty
		limits["koordinator.sh/gpu-memory"] = memQty
		limits["koordinator.sh/gpu.shared"] = one
		requests["koordinator.sh/gpu-core"]   = coreQty
		requests["koordinator.sh/gpu-memory"] = memQty
		requests["koordinator.sh/gpu.shared"] = one
	} else if reqGPU > 0 {
		// 整块模式：写 nvidia.com/gpu，由 phantom-webhook 转换
		gpuQty := resource.MustParse(fmt.Sprintf("%d", reqGPU))
		limits["nvidia.com/gpu"]   = gpuQty
		requests["nvidia.com/gpu"] = gpuQty
	}

	return corev1.Container{
		Name:  "workload",
		Image: image,
		Resources: corev1.ResourceRequirements{
			Limits:   limits,
			Requests: requests,
		},
	}
}

// parseAttrInt64 parses an int64 from event attributes; returns 0 on missing/error.
func parseAttrInt64(attrs map[string]string, key string) int64 {
	if v, ok := attrs[key]; ok {
		var n int64
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}

// detectGPUCount returns the number of physical GPUs via nvidia-smi.
func detectGPUCount() int64 {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output()
	if err != nil {
		return 0
	}
	count := int64(0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

// detectGPUTotalMemMB returns total free GPU memory in MiB across all GPUs via nvidia-smi.
// Returns 0 if nvidia-smi is unavailable.
func detectGPUTotalMemMB() int64 {
	out, err := exec.Command("nvidia-smi", "--query-gpu=memory.free", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0
	}
	total := int64(0)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var mb int64
		fmt.Sscanf(line, "%d", &mb)
		total += mb
	}
	return total
}
