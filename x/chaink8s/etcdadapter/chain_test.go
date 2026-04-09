package etcdadapter

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// --- buildPodSpec GPU resource injection ---

func TestBuildPodSpecNoGPU(t *testing.T) {
	spec := buildPodSpec("pod-1", "default", "order/1/1/1", "provider-abc", "node-xyz", 2000, 4096, 0, 0, 0)

	require.Equal(t, "pod-1", spec.Name)
	require.Len(t, spec.Containers, 1)

	c := spec.Containers[0]
	require.Empty(t, c.Resources.Limits, "CPU-only pod should have no GPU limits")
	require.Equal(t, "0", spec.Annotations["ck8s/req-gpu"])
}

func TestBuildPodSpecWithGPU(t *testing.T) {
	// 整块模式：reqGPUCore=0，reqGPU=2 → nvidia.com/gpu（由 webhook 转换）
	spec := buildPodSpec("pod-gpu", "default", "order/1/1/2", "provider-abc", "node-xyz", 2000, 4096, 2, 0, 0)

	require.Len(t, spec.Containers, 1)
	c := spec.Containers[0]

	require.Equal(t, "2", c.Resources.Limits["nvidia.com/gpu"])
	require.Equal(t, "2", c.Resources.Requests["nvidia.com/gpu"])
	require.Equal(t, "2", spec.Annotations["ck8s/req-gpu"])
	require.Equal(t, "2000", spec.Annotations["ck8s/req-cpu"])
	require.Equal(t, "4096", spec.Annotations["ck8s/req-mem"])
	// 整块模式不应有 HAMi label
	require.Empty(t, spec.Labels["koordinator.sh/gpu-isolation-provider"])
}

func TestBuildPodSpecFractionalGPU(t *testing.T) {
	// 分数模式：reqGPUCore=40，reqGPUMemMB=4096 → 直接写 koordinator 格式
	spec := buildPodSpec("pod-frac", "default", "order/1/1/5", "provider-abc", "node-xyz", 2000, 4096, 1, 40, 4096)

	require.Len(t, spec.Containers, 1)
	c := spec.Containers[0]

	require.Equal(t, "40", c.Resources.Limits["koordinator.sh/gpu-core"])
	require.Equal(t, "4096Mi", c.Resources.Limits["koordinator.sh/gpu-memory"])
	require.Equal(t, "1", c.Resources.Limits["koordinator.sh/gpu.shared"])
	require.Empty(t, c.Resources.Limits["nvidia.com/gpu"], "fractional mode must not set nvidia.com/gpu")
	require.Equal(t, "HAMi-core", spec.Labels["koordinator.sh/gpu-isolation-provider"])
}

func TestBuildPodSpecSingleGPU(t *testing.T) {
	spec := buildPodSpec("pod-gpu1", "default", "order/1/1/3", "provider-abc", "node-xyz", 1000, 2048, 1, 0, 0)
	c := spec.Containers[0]
	require.Equal(t, "1", c.Resources.Limits["nvidia.com/gpu"])
}

func TestBuildPodSpecLabels(t *testing.T) {
	spec := buildPodSpec("pod-label", "default", "akash/owner/1/dseq/2/gseq/3", "provABC", "nodeXYZ", 500, 1024, 0, 0, 0)

	require.Equal(t, "ck8s-workload", spec.Labels["app"])
	require.NotEmpty(t, spec.Labels["ck8s-order"])
	require.NotEmpty(t, spec.Labels["ck8s-provider"])
	require.Equal(t, "nodeXYZ", spec.Labels["ck8s-node"])
}

func TestBuildPodSpecJSONRoundtrip(t *testing.T) {
	spec := buildPodSpec("pod-json", "default", "order/1/1/4", "provider-abc", "node-xyz", 2000, 4096, 4, 0, 0)

	bz, err := json.Marshal(spec)
	require.NoError(t, err)

	var decoded PodSpec
	require.NoError(t, json.Unmarshal(bz, &decoded))
	require.Equal(t, "4", decoded.Containers[0].Resources.Limits["nvidia.com/gpu"])
	require.Equal(t, "Pending", decoded.Status)
}

// --- parseAttrInt64 ---

func TestParseAttrInt64(t *testing.T) {
	attrs := map[string]string{
		"req_gpu": "4",
		"req_cpu": "2000",
		"req_mem": "8192",
	}

	require.Equal(t, int64(4), parseAttrInt64(attrs, "req_gpu"))
	require.Equal(t, int64(2000), parseAttrInt64(attrs, "req_cpu"))
	require.Equal(t, int64(8192), parseAttrInt64(attrs, "req_mem"))
	require.Equal(t, int64(0), parseAttrInt64(attrs, "missing_key"))
}

func TestParseAttrInt64ZeroGPU(t *testing.T) {
	attrs := map[string]string{"req_gpu": "0"}
	require.Equal(t, int64(0), parseAttrInt64(attrs, "req_gpu"))
}

// --- orderIDToPodName ---

func TestOrderIDToPodName(t *testing.T) {
	// Standard Akash order ID format: owner/dseq/gseq/oseq
	name := orderIDToPodName("akash1abc/15831/1/1")
	require.Contains(t, name, "ck8s-")
	require.NotEmpty(t, name)
}

func TestOrderIDToPodNameShort(t *testing.T) {
	name := orderIDToPodName("simple-order")
	require.Contains(t, name, "ck8s-simple-order")
}
