package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	cflags "pkg.akt.dev/go/cli/flags"
	"pkg.akt.dev/node/x/chaink8s/types"
)

// GetQueryChaink8sCmd returns the root query command for chaink8s.
func GetQueryChaink8sCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chaink8s",
		Short: "ChainK8s query subcommands",
	}
	cmd.PersistentFlags().String(cflags.FlagGRPC, "localhost:9190", "gRPC endpoint (host:port)")
	cmd.AddCommand(
		getCmdQueryNodes(),
		getCmdQuerySpotPrice(),
		getCmdQueryBoundOrders(),
	)
	return cmd
}

func getCmdQueryNodes() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "nodes",
		Short: "List all registered nodes and their available resources",
		Example: `  akash chaink8s nodes
  akash chaink8s nodes --provider akash1...`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			grpcAddr, _ := cmd.Flags().GetString(cflags.FlagGRPC)
			provider, _ := cmd.Flags().GetString("provider")

			conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("grpc dial %s: %w", grpcAddr, err)
			}
			defer conn.Close()

			resp := &types.QueryNodesResponse{}
			if err := conn.Invoke(cmd.Context(), "/chaink8s.Query/Nodes",
				&types.QueryNodesRequest{Provider: provider}, resp); err != nil {
				return err
			}

			out, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
	cmd.Flags().String("provider", "", "Filter by provider bech32 address")
	cmd.Flags().String(cflags.FlagGRPC, "localhost:9190", "gRPC endpoint (host:port)")
	return cmd
}

func getCmdQuerySpotPrice() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "spot-price",
		Short: "Show current spot market prices for CPU and GPU",
		RunE: func(cmd *cobra.Command, _ []string) error {
			grpcAddr, _ := cmd.Flags().GetString(cflags.FlagGRPC)

			conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("grpc dial %s: %w", grpcAddr, err)
			}
			defer conn.Close()

			resp := &types.QuerySpotPriceResponse{}
			if err := conn.Invoke(cmd.Context(), "/chaink8s.Query/SpotPrice",
				&types.QuerySpotPriceRequest{}, resp); err != nil {
				return err
			}

			out, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
	cmd.Flags().String(cflags.FlagGRPC, "localhost:9190", "gRPC endpoint (host:port)")
	return cmd
}

func getCmdQueryBoundOrders() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bound-orders",
		Short: "List all orders that have been scheduled to a node",
		RunE: func(cmd *cobra.Command, _ []string) error {
			grpcAddr, _ := cmd.Flags().GetString(cflags.FlagGRPC)

			conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				return fmt.Errorf("grpc dial %s: %w", grpcAddr, err)
			}
			defer conn.Close()

			resp := &types.QueryBoundOrdersResponse{}
			if err := conn.Invoke(cmd.Context(), "/chaink8s.Query/BoundOrders",
				&types.QueryBoundOrdersRequest{}, resp); err != nil {
				return err
			}

			out, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(out))
			return nil
		},
	}
	cmd.Flags().String(cflags.FlagGRPC, "localhost:9190", "gRPC endpoint (host:port)")
	return cmd
}
