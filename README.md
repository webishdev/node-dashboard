# Node Dashboard

A modern web dashboard for monitoring system metrics from multiple nodes running Prometheus node_exporter. This application provides real-time visualization of CPU, memory, and disk usage with historical data tracking and a responsive web interface.

## Features

- **Multi-Node Monitoring**: Monitor multiple nodes simultaneously by connecting to their node_exporter endpoints
- **Real-Time Metrics**: Display current CPU usage, memory consumption, and disk utilization
- **Historical Data**: Track and visualize metric trends over time with configurable data retention
- **Modern UI**: Clean, responsive dashboard built with Tailwind CSS
- **Auto-Refresh**: Configurable automatic page refresh for real-time monitoring
- **Authentication**: Optional basic authentication for secure access
- **Configurable Updates**: Separate configuration for dashboard refresh and data fetch frequencies

## Metrics Displayed

- **CPU Usage**: Percentage utilization with core count information
- **Memory Usage**: Percentage and total memory in MB
- **Disk Usage**: Percentage and total disk space in GB

## Installation & Usage

### Prerequisites

- Go 1.19 or later
- Access to one or more nodes running Prometheus node_exporter

### Building the Application

```bash
# Development build
make build

# Production build (Linux executable)
make build-prod
```

### Running the Dashboard

```bash
# Basic usage with single node
./node-dashboard --node-exporter http://localhost:9100/metrics

# Multiple nodes
./node-dashboard --node-exporter http://node1:9100/metrics --node-exporter http://node2:9100/metrics

# Custom port and update frequencies
./node-dashboard --port 8080 --node-exporter http://localhost:9100/metrics --update-frequency 15 --data-fetch-frequency 10

# With authentication
./node-dashboard --node-exporter http://localhost:9100/metrics --enable-auth --auth-user admin --auth-password secret
```

### Configuration Options

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--port` | `-p` | 8080 | Port to listen on |
| `--node-exporter` | `-n` | - | Node exporter URLs (can be specified multiple times) |
| `--update-frequency` | `-u` | 10 | Dashboard update frequency in seconds |
| `--data-fetch-frequency` | `-d` | 5 | Node exporter data fetch frequency in seconds |
| `--enable-auth` | - | false | Enable basic authentication |
| `--auth-user` | - | - | Username for basic authentication |
| `--auth-password` | - | - | Password for basic authentication |

### Docker Usage

```bash
# Build Docker image
docker build -t node-dashboard .

# Run with Docker
docker run -p 8080:8080 node-dashboard --node-exporter http://host.docker.internal:9100/metrics
```

## Development

The project follows standard Go project layout:

- `cmd/node-dashboard/`: Main application entry point
- `internal/metrics/`: Metrics collection and processing
- `internal/server/`: Web server and dashboard handling
- `internal/server/templates/`: HTML templates

### Project Structure

```
├── cmd/node-dashboard/     # Main application
├── internal/
│   ├── metrics/           # Metrics collection and storage
│   └── server/            # Web server and dashboard
├── Dockerfile             # Container build configuration
├── Makefile              # Build automation
└── README.md             # This file
```

## License

This project is open source and available under standard licensing terms.