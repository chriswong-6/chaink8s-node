package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	// AnnotationNodeClaim Pod 必须携带此 annotation，值格式：<provider>/<nodeID>
	AnnotationNodeClaim = "chaink8s.io/node-claim"

	// claimKeyPrefix 链上 NodeClaim 存储前缀（与 keeper 中 NodeClaimKey 对应）
	claimKeyPrefix = "/chaink8s/claims/"
)

// systemNamespaces 这些 namespace 的 Pod 豁免检查
var systemNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
}

// Handler 是 Webhook HTTP 处理器
type Handler struct {
	etcd *clientv3.Client
}

// NewHandler 创建 Handler，etcdAddr 是 etcd Adapter 地址（如 "localhost:12379"）
func NewHandler(etcdAddr string) (*Handler, error) {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{etcdAddr},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to etcd adapter %s: %w", etcdAddr, err)
	}
	return &Handler{etcd: cli}, nil
}

// ServeHTTP 实现 http.Handler，路由到 /validate
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/validate" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h.handleValidate(w, r)
}

func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		http.Error(w, "decode admission review failed", http.StatusBadRequest)
		return
	}

	review.Response = h.validate(review.Request)
	review.Response.UID = review.Request.UID

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(review); err != nil {
		log.Printf("webhook: encode response: %v", err)
	}
}

func (h *Handler) validate(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	// 只处理 Pod 资源
	if req.Resource.Resource != "pods" {
		return allow()
	}

	// 系统 namespace 豁免
	if systemNamespaces[req.Namespace] {
		return allow()
	}

	// 解析 Pod
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return deny(fmt.Sprintf("decode pod: %v", err))
	}

	// 检查 annotation
	claimVal, ok := pod.Annotations[AnnotationNodeClaim]
	if !ok {
		return deny(fmt.Sprintf(
			"pod must have annotation %q (format: <provider>/<nodeID>); "+
				"pods must be scheduled via the ChainK8s on-chain scheduler",
			AnnotationNodeClaim,
		))
	}

	// 验证格式 provider/nodeID
	parts := strings.SplitN(claimVal, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return deny(fmt.Sprintf(
			"annotation %q value %q invalid: expected <provider>/<nodeID>",
			AnnotationNodeClaim, claimVal,
		))
	}
	provider, nodeID := parts[0], parts[1]

	// 向 etcd Adapter 查询链上 NodeClaim 是否存在
	claimKey := claimKeyPrefix + provider + "/" + nodeID
	if err := h.checkClaimExists(claimKey); err != nil {
		return deny(fmt.Sprintf(
			"NodeClaim %q not found on chain: %v",
			claimVal, err,
		))
	}

	log.Printf("webhook: allowed pod %s/%s (claim %s)", req.Namespace, req.Name, claimVal)
	return allow()
}

// checkClaimExists 向 etcd Adapter 查询 key 是否存在
func (h *Handler) checkClaimExists(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := h.etcd.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("etcd get: %w", err)
	}
	if resp.Count == 0 {
		return fmt.Errorf("key not found")
	}
	return nil
}

func allow() *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{Allowed: true}
}

func deny(reason string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result: &metav1.Status{
			Code:    403,
			Message: reason,
		},
	}
}
