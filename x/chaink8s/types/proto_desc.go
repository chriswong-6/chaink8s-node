package types

// 注册合成 protobuf 文件描述符，并提供 Descriptor() 方法实现。
//
// gogoproto 的 table-driven 序列化器需要每个消息类型提供：
//   1. proto.RegisterType 注册（已在 node.go init() 中完成）
//   2. Descriptor() ([]byte, []int) 方法——返回 gzip 压缩的 FileDescriptorProto 字节
//      以及该消息在文件中的路径索引
//
// 这里通过程序化构建 FileDescriptorProto，序列化后 gzip，供 Descriptor() 返回。

import (
	"bytes"
	"compress/gzip"

	msgv1 "cosmossdk.io/api/cosmos/msg/v1"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// fileDescriptorChaink8sTx 是 gzip 压缩的 FileDescriptorProto 字节
// 在 init() 中初始化，供 Descriptor() 方法使用
var fileDescriptorChaink8sTx []byte

func init() {
	registerChaink8sFileDescriptor()
}

func registerChaink8sFileDescriptor() {
	// 如果已注册则跳过
	if _, err := protoregistry.GlobalFiles.FindFileByPath("chaink8s/tx.proto"); err == nil {
		return
	}

	fdp := buildFileDescriptorProto()

	// 序列化并 gzip，用于 Descriptor() 方法
	raw, err := proto.Marshal(fdp)
	if err != nil {
		panic("chaink8s: marshal FileDescriptorProto: " + err.Error())
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		panic("chaink8s: gzip write: " + err.Error())
	}
	w.Close()
	fileDescriptorChaink8sTx = buf.Bytes()

	// 注册到 protoregistry.GlobalFiles（供 HybridResolver 使用）
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		panic("chaink8s: protodesc.NewFile: " + err.Error())
	}
	if err := protoregistry.GlobalFiles.RegisterFile(fd); err != nil {
		panic("chaink8s: RegisterFile: " + err.Error())
	}

	// 同时注册到 gogoproto 的旧式文件注册表（供 table_unmarshal 使用）
	gogoproto.RegisterFile("chaink8s/tx.proto", fileDescriptorChaink8sTx)
}

func msgSignerOpts(signerField string) *descriptorpb.MessageOptions {
	// 设置 cosmos.msg.v1.signer 扩展，告知 SDK 哪个字段是签名者地址
	opts := &descriptorpb.MessageOptions{}
	proto.SetExtension(opts, msgv1.E_Signer, []string{signerField})
	return opts
}

func buildFileDescriptorProto() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("chaink8s/tx.proto"),
		Package: proto.String("chaink8s"),
		Syntax:  proto.String("proto3"),
		// 依赖 cosmos/msg/v1/msg.proto（提供 cosmos.msg.v1.signer 扩展）
		Dependency: []string{"cosmos/msg/v1/msg.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("pkg.akt.dev/node/x/chaink8s/types"),
		},
		// 消息顺序决定 Descriptor() 的路径索引 []int{N}
		// 0: MsgNodeHeartbeat
		// 1: MsgNodeHeartbeatResponse
		// 2: MsgNodeClaim
		// 3: MsgNodeClaimResponse
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:    proto.String("MsgNodeHeartbeat"),
				Options: msgSignerOpts("provider"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("provider", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("node_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("free_cpu", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_mem", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu_mem_mb", 6, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu_core", 7, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				},
			},
			{Name: proto.String("MsgNodeHeartbeatResponse")},
			{
				Name:    proto.String("MsgNodeClaim"),
				Options: msgSignerOpts("provider"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("provider", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("node_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("claim_cpu", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("claim_mem", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("claim_gpu", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("purpose", 6, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				},
			},
			{Name: proto.String("MsgNodeClaimResponse")},
			// ── Query 消息（索引 4-10）─────────────────────────────────
			// 4: NodeInfo
			{
				Name: proto.String("NodeInfo"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("provider", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("node_id", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("free_cpu", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_mem", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("reputation_score", 6, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu_core", 7, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("slash_count", 8, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu_mem_mb", 9, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("total_cpu", 10, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("total_mem", 11, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("alloc_cpu", 12, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("alloc_mem", 13, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				},
			},
			// 5: QueryNodesRequest
			{
				Name: proto.String("QueryNodesRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("provider", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				},
			},
			// 6: QueryNodesResponse
			{
				Name: proto.String("QueryNodesResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					fieldRepeatedMsg("nodes", 1, ".chaink8s.NodeInfo"),
				},
			},
			// 7: QuerySpotPriceRequest
			{Name: proto.String("QuerySpotPriceRequest")},
			// 8: QuerySpotPriceResponse
			{
				Name: proto.String("QuerySpotPriceResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("price_per_cpu_milli", 1, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_cpu_total", 2, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("pending_orders", 3, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("price_per_gpu", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("free_gpu_total", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
				},
			},
			// 9: QueryBoundOrdersRequest
			{Name: proto.String("QueryBoundOrdersRequest")},
			// 10: QueryBoundOrdersResponse
			{
				Name: proto.String("QueryBoundOrdersResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					fieldRepeatedMsg("orders", 1, ".chaink8s.BoundOrderInfo"),
				},
			},
			// 11: BoundOrderInfo
			{
				Name: proto.String("BoundOrderInfo"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("order_id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("provider", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("node_id", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					field("req_cpu", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("req_mem", 5, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("req_gpu", 6, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("req_gpu_core", 7, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("req_gpu_mem_mb", 8, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					field("image", 9, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("Msg"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("NodeHeartbeat"),
						InputType:  proto.String(".chaink8s.MsgNodeHeartbeat"),
						OutputType: proto.String(".chaink8s.MsgNodeHeartbeatResponse"),
					},
					{
						Name:       proto.String("NodeClaim"),
						InputType:  proto.String(".chaink8s.MsgNodeClaim"),
						OutputType: proto.String(".chaink8s.MsgNodeClaimResponse"),
					},
				},
			},
			{
				Name: proto.String("Query"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("Nodes"),
						InputType:  proto.String(".chaink8s.QueryNodesRequest"),
						OutputType: proto.String(".chaink8s.QueryNodesResponse"),
					},
					{
						Name:       proto.String("SpotPrice"),
						InputType:  proto.String(".chaink8s.QuerySpotPriceRequest"),
						OutputType: proto.String(".chaink8s.QuerySpotPriceResponse"),
					},
					{
						Name:       proto.String("BoundOrders"),
						InputType:  proto.String(".chaink8s.QueryBoundOrdersRequest"),
						OutputType: proto.String(".chaink8s.QueryBoundOrdersResponse"),
					},
				},
			},
		},
	}
}

func field(name string, number int32, typ descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Type:   typ.Enum(),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
}

// fieldRepeatedMsg returns a repeated message field descriptor.
func fieldRepeatedMsg(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(typeName),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
	}
}

// fieldRepeatedString returns a repeated string field descriptor.
func fieldRepeatedString(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
	}
}
