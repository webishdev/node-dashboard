package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/webishdev/node_dashboard/internal/metrics"
	"github.com/webishdev/node_dashboard/internal/server"
)

var (
	port              int
	nodeExporterAddrs []string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "node-dashboard",
	Short: "A dashboard for monitoring node metrics",
	Long:  `A web dashboard that displays system metrics like CPU, memory, and disk usage by reading from node_exporter.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Parse node configurations
		var nodeConfigs []metrics.NodeConfig
		for i, addr := range nodeExporterAddrs {
			// Check if the address contains a comma (name,url format)
			if strings.Contains(addr, ",") {
				parts := strings.SplitN(addr, ",", 2) // Split into at most 2 parts
				if len(parts) == 2 {
					nodeConfigs = append(nodeConfigs, metrics.NodeConfig{Name: strings.TrimSpace(parts[0]), URL: strings.TrimSpace(parts[1])})
				} else {
					// Fallback if split didn't work as expected
					nodeConfigs = append(nodeConfigs, metrics.NodeConfig{Name: fmt.Sprintf("Node-%d", i+1), URL: addr})
				}
			} else {
				// Default name if not provided (just URL)
				nodeConfigs = append(nodeConfigs, metrics.NodeConfig{Name: fmt.Sprintf("Node-%d", i+1), URL: addr})
			}
		}

		// Start the dashboard server
		srv := server.New(port, nodeConfigs)
		if err := srv.Start(); err != nil {
			fmt.Printf("Error starting server: %v\n", err)
			os.Exit(1)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	// Define flags for configuration
	rootCmd.Flags().IntVarP(&port, "port", "p", 8080, "Port to listen on")
	rootCmd.Flags().StringSliceVarP(&nodeExporterAddrs, "node-exporter", "n", []string{"http://localhost:9100"}, "Node exporter addresses (format: 'name,url' or just 'url')")
}

// main function - kept small and clean as required
func main() {
	Execute()
}
