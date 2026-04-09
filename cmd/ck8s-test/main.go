// ck8s-test 端到端联调测试工具
// 测试流程：
//   1. 广播 MsgNodeHeartbeat → 链上写入节点资源
//   2. 等待上链，查询 EndBlock 事件（心跳/调度/Spot定价）
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/cosmos/cosmos-sdk/types/module"
	"pkg.akt.dev/go/sdkutil"
	ck8stypes "pkg.akt.dev/node/x/chaink8s/types"
	"pkg.akt.dev/node/app"
)

const (
	nodeGRPC   = "localhost:9190"
	nodeREST   = "http://localhost:1317"
	nodeRPC    = "http://localhost:26657"
	chainID    = "local-test"
	keyName    = "mykey"
	keyringDir = "/home/lmdrive/.akash-local"
)

func main() {
	log.SetFlags(0)
	log.Println("=== ChainK8s 端到端测试 ===\n")

	// 使用 akash 完整编码配置（包含所有模块的类型注册，与 keyring 兼容）
	// ModuleBasics 是 map[string]AppModuleBasic，转为 slice
	var basics []module.AppModuleBasic
	for _, b := range app.ModuleBasics() {
		basics = append(basics, b)
	}
	encCfg := sdkutil.MakeEncodingConfig(basics...)
	// 额外注册 chaink8s 消息类型
	encCfg.InterfaceRegistry.RegisterImplementations((*sdk.Msg)(nil),
		&ck8stypes.MsgNodeHeartbeat{},
		&ck8stypes.MsgNodeClaim{},
	)
	cdc := encCfg.Codec
	txConfig := encCfg.TxConfig
	interfaceRegistry := encCfg.InterfaceRegistry

	// 连接 gRPC
	grpcConn, err := grpc.Dial(nodeGRPC, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc dial: %v", err)
	}
	defer grpcConn.Close()

	// 打开 keyring
	kr, err := keyring.New("akash", keyring.BackendTest, keyringDir, os.Stdin, cdc)
	if err != nil {
		log.Fatalf("keyring: %v", err)
	}
	keyInfo, err := kr.Key(keyName)
	if err != nil {
		log.Fatalf("key %s: %v", keyName, err)
	}
	addr, err := keyInfo.GetAddress()
	if err != nil {
		log.Fatalf("get address: %v", err)
	}
	log.Printf("账户: %s\n", addr.String())

	// 查询账户信息
	authClient := authtypes.NewQueryClient(grpcConn)
	accResp, err := authClient.Account(context.Background(), &authtypes.QueryAccountRequest{
		Address: addr.String(),
	})
	if err != nil {
		log.Fatalf("query account: %v", err)
	}
	var baseAcc authtypes.AccountI
	if err := interfaceRegistry.UnpackAny(accResp.Account, &baseAcc); err != nil {
		log.Fatalf("unpack account: %v", err)
	}
	log.Printf("AccountNumber=%d Sequence=%d\n", baseAcc.GetAccountNumber(), baseAcc.GetSequence())

	// 构建 client.Context 和交易工厂
	clientCtx := client.Context{}.
		WithChainID(chainID).
		WithCodec(cdc).
		WithInterfaceRegistry(interfaceRegistry).
		WithTxConfig(txConfig).
		WithKeyring(kr).
		WithNodeURI(nodeRPC).
		WithGRPCClient(grpcConn)

	factory := tx.Factory{}.
		WithChainID(chainID).
		WithKeybase(kr).
		WithTxConfig(txConfig).
		WithAccountNumber(baseAcc.GetAccountNumber()).
		WithSequence(baseAcc.GetSequence()).
		WithGas(200000).
		WithFees("500uakt").
		WithSignMode(signing.SignMode_SIGN_MODE_DIRECT)

	// ── 步骤 1: 发送 MsgNodeHeartbeat ──────────────────────────────
	log.Println("--- 步骤1: 广播 MsgNodeHeartbeat ---")
	heartbeat := ck8stypes.NewMsgNodeHeartbeat(addr, "node-1", 4000, 8<<30, 1, 24576) // 24576 MiB = 24 GiB GPU mem

	txBytes, err := signTx(clientCtx, factory, heartbeat)
	if err != nil {
		log.Fatalf("sign heartbeat tx: %v", err)
	}

	code, txhash, rawlog := broadcastTx(txBytes)
	log.Printf("结果: code=%d txhash=%s", code, txhash)
	if code != 0 {
		log.Printf("错误: %s", rawlog)
		os.Exit(1)
	}
	log.Println("Heartbeat 交易已提交！等待上链...")

	// ── 步骤 2: 等待并检查事件 ─────────────────────────────────────
	time.Sleep(7 * time.Second)
	log.Println("\n--- 步骤2: 查询交易结果 ---")
	checkTx(txhash)

	// ── 步骤 3: 查询最新块 EndBlock 事件 ──────────────────────────
	log.Println("\n--- 步骤3: 检查 EndBlock 事件 ---")
	checkLatestBlockEvents()

	log.Println("\n=== 测试完成 ===")
}

func signTx(clientCtx client.Context, factory tx.Factory, msg sdk.Msg) ([]byte, error) {
	txBuilder, err := factory.BuildUnsignedTx(msg)
	if err != nil {
		return nil, fmt.Errorf("build unsigned tx: %w", err)
	}
	if err := tx.Sign(context.Background(), factory, keyName, txBuilder, true); err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return clientCtx.TxConfig.TxEncoder()(txBuilder.GetTx())
}

func broadcastTx(txBytes []byte) (code int, txhash, rawlog string) {
	body := fmt.Sprintf(`{"tx_bytes":%q,"mode":"BROADCAST_MODE_SYNC"}`,
		base64.StdEncoding.EncodeToString(txBytes))

	resp, err := http.Post(nodeREST+"/cosmos/tx/v1beta1/txs",
		"application/json", bytes.NewBufferString(body))
	if err != nil {
		log.Fatalf("broadcast http: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var result struct {
		TxResponse struct {
			Code   int    `json:"code"`
			TxHash string `json:"txhash"`
			RawLog string `json:"raw_log"`
		} `json:"tx_response"`
	}
	json.Unmarshal(b, &result)
	return result.TxResponse.Code, result.TxResponse.TxHash, result.TxResponse.RawLog
}

func checkTx(txhash string) {
	resp, err := http.Get(nodeREST + "/cosmos/tx/v1beta1/txs/" + txhash)
	if err != nil {
		log.Printf("query tx: %v", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var result struct {
		TxResponse struct {
			Code   int    `json:"code"`
			Height string `json:"height"`
			RawLog string `json:"raw_log"`
			Events []struct {
				Type       string `json:"type"`
				Attributes []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"attributes"`
			} `json:"events"`
		} `json:"tx_response"`
	}
	json.Unmarshal(b, &result)
	log.Printf("交易确认: code=%d height=%s", result.TxResponse.Code, result.TxResponse.Height)
	if result.TxResponse.Code != 0 {
		log.Printf("错误: %s", result.TxResponse.RawLog)
		return
	}
	for _, e := range result.TxResponse.Events {
		if e.Type == "node_heartbeat" {
			log.Printf("[事件] node_heartbeat:")
			for _, a := range e.Attributes {
				log.Printf("  %s = %s", a.Key, a.Value)
			}
		}
	}
}

func checkLatestBlockEvents() {
	resp, err := http.Get(nodeRPC + "/block_results")
	if err != nil {
		log.Printf("block_results: %v", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	var result struct {
		Result struct {
			Height             string `json:"height"`
			FinalizeBlockEvents []struct {
				Type       string `json:"type"`
				Attributes []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"attributes"`
			} `json:"finalize_block_events"`
		} `json:"result"`
	}
	json.Unmarshal(b, &result)
	log.Printf("最新块高度: %s", result.Result.Height)

	ck8sEvents := 0
	for _, e := range result.Result.FinalizeBlockEvents {
		switch e.Type {
		case "node_heartbeat", "chaink8s_bind", "spot_price_update", "node_slash", "node_claim":
			ck8sEvents++
			log.Printf("[ChainK8s] %s", e.Type)
			for _, a := range e.Attributes {
				log.Printf("  %s = %s", a.Key, a.Value)
			}
		}
	}
	if ck8sEvents == 0 {
		log.Println("（本块暂无 ChainK8s 事件，EndBlocker 在没有 OrderOpen 时不触发调度）")
	}
}
