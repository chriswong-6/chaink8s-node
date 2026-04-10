package cli

import (
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"pkg.akt.dev/node/x/chaink8s/types"
)

// GetTxChaink8sCmd returns the root tx command for chaink8s.
func GetTxChaink8sCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chaink8s",
		Short: "ChainK8s transaction subcommands",
	}
	cmd.AddCommand(
		getCmdTxNodeHeartbeat(),
		getCmdTxNodeClaim(),
	)
	return cmd
}

func getCmdTxNodeHeartbeat() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node-heartbeat",
		Short: "Report node resources to the chain (called by ck8s-monitor automatically)",
		Example: `  akash tx chaink8s node-heartbeat \
    --provider akash1... --node-id node-1 \
    --cpu 8000 --mem 17179869184 --gpu 1 --gpu-mem-mb 24576 \
    --from mykey --chain-id local-test --fees 5000uakt -y`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			providerStr, _ := cmd.Flags().GetString("provider")
			nodeID, _ := cmd.Flags().GetString("node-id")
			cpu, _ := cmd.Flags().GetInt64("cpu")
			mem, _ := cmd.Flags().GetInt64("mem")
			gpu, _ := cmd.Flags().GetInt64("gpu")
			gpuMemMB, _ := cmd.Flags().GetInt64("gpu-mem-mb")

			providerAddr, err := sdk.AccAddressFromBech32(providerStr)
			if err != nil {
				return fmt.Errorf("invalid provider address: %w", err)
			}

			msg := types.NewMsgNodeHeartbeat(providerAddr, nodeID, cpu, mem, gpu, gpuMemMB)
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	cmd.Flags().String("provider", "", "Provider bech32 address (required)")
	cmd.Flags().String("node-id", "", "Node identifier (required)")
	cmd.Flags().Int64("cpu", 0, "Available CPU in milli-cores (e.g. 8000 = 8 cores)")
	cmd.Flags().Int64("mem", 0, "Available memory in bytes")
	cmd.Flags().Int64("gpu", 0, "Number of available GPUs")
	cmd.Flags().Int64("gpu-mem-mb", 0, "Available GPU memory in MiB")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("node-id")

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func getCmdTxNodeClaim() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node-claim",
		Short: "Declare operator self-use of node resources (must be submitted on-chain)",
		Example: `  akash tx chaink8s node-claim \
    --provider akash1... --node-id node-1 \
    --cpu 2000 --mem 4294967296 --gpu 1 \
    --purpose "local-inference" \
    --from mykey --chain-id local-test --fees 5000uakt -y`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			providerStr, _ := cmd.Flags().GetString("provider")
			nodeID, _ := cmd.Flags().GetString("node-id")
			cpu, _ := cmd.Flags().GetInt64("cpu")
			mem, _ := cmd.Flags().GetInt64("mem")
			gpu, _ := cmd.Flags().GetInt64("gpu")
			purpose, _ := cmd.Flags().GetString("purpose")

			providerAddr, err := sdk.AccAddressFromBech32(providerStr)
			if err != nil {
				return fmt.Errorf("invalid provider address: %w", err)
			}

			msg := types.NewMsgNodeClaim(providerAddr, nodeID, cpu, mem, gpu, purpose)
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	cmd.Flags().String("provider", "", "Provider bech32 address (required)")
	cmd.Flags().String("node-id", "", "Node identifier (required)")
	cmd.Flags().Int64("cpu", 0, "CPU to claim in milli-cores")
	cmd.Flags().Int64("mem", 0, "Memory to claim in bytes")
	cmd.Flags().Int64("gpu", 0, "Number of GPUs to claim")
	cmd.Flags().String("purpose", "", "Description of self-use purpose")
	_ = cmd.MarkFlagRequired("provider")
	_ = cmd.MarkFlagRequired("node-id")

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// parseIntArg is a helper used in tests.
func parseIntArg(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

var _ = os.Stderr // keep import
var _ = parseIntArg
