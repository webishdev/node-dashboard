package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/webishdev/node_dashboard/internal/server"
)

var (
	port             int
	nodeExporterAddr string
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "node-dashboard",
	Short: "A dashboard for monitoring node metrics",
	Long:  `A web dashboard that displays system metrics like CPU, memory, and disk usage by reading from node_exporter.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Start the dashboard server
		srv := server.New(port, nodeExporterAddr)
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
	rootCmd.Flags().StringVarP(&nodeExporterAddr, "node-exporter", "n", "http://localhost:9100", "Node exporter address")
}

// main function - kept small and clean as required
func main() {
	Execute()
}
