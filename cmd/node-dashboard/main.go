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
	port               int
	nodeExporterAddrs  []string
	updateFrequency    int
	dataFetchFrequency int
	enableAuth         bool
	authUser           string
	authPassword       string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "node-dashboard",
	Short: "A dashboard for monitoring node metrics",
	Long:  `A web dashboard that displays system metrics like CPU, memory, and disk usage by reading from node_exporter.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Check if node_exporter URLs are provided
		if len(nodeExporterAddrs) == 0 {
			cmd.Help()
			return
		}

		// Validate authentication parameters
		if enableAuth {
			if authUser == "" || authPassword == "" {
				fmt.Println("Error: --auth-user and --auth-password are required when --enable-auth is set")
				os.Exit(1)
			}
		}

		// Parse node configurations (URLs only, no name support)
		var nodeConfigs []metrics.NodeConfig
		for _, addr := range nodeExporterAddrs {
			// Only accept URLs, no name,url format
			nodeConfigs = append(nodeConfigs, metrics.NodeConfig{URL: strings.TrimSpace(addr)})
		}

		// Start the dashboard server
		srv := server.New(port, nodeConfigs, updateFrequency, dataFetchFrequency, enableAuth, authUser, authPassword)
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
	rootCmd.Flags().StringSliceVarP(&nodeExporterAddrs, "node-exporter", "n", []string{}, "Node exporter addresses (URLs only)")
	rootCmd.Flags().IntVarP(&updateFrequency, "update-frequency", "u", 30, "Dashboard update frequency in seconds")
	rootCmd.Flags().IntVarP(&dataFetchFrequency, "data-fetch-frequency", "d", 30, "Node exporter data fetch frequency in seconds")
	rootCmd.Flags().BoolVar(&enableAuth, "enable-auth", false, "Enable basic authentication")
	rootCmd.Flags().StringVar(&authUser, "auth-user", "", "Username for basic authentication (required when auth is enabled)")
	rootCmd.Flags().StringVar(&authPassword, "auth-password", "", "Password for basic authentication (required when auth is enabled)")
}

// main function - kept small and clean as required
func main() {
	Execute()
}
