# Node Dashboard

A modern web dashboard for monitoring system metrics from multiple nodes running Prometheus node_exporter. This application provides real-time visualization of CPU, memory, disk and network usage with historical data tracking and a responsive web interface.

## Features

- **Multi-Node Monitoring**: Monitor multiple nodes simultaneously by connecting to their node_exporter endpoints
- **Real-Time Metrics**: Display current CPU usage, memory consumption, disk utilization and network throughput
- **Historical Data**: Metrics are stored in a local SQLite database and shown as charts for the last hour, 24 hours, week or month, with configurable retention
- **Modern UI**: Clean, responsive dashboard built with Tailwind CSS
- **Auto-Refresh**: Configurable automatic page refresh for real-time monitoring
- **Authentication**: Optional basic authentication for secure access
- **Configurable Updates**: Separate configuration for dashboard refresh and data fetch frequencies

## Metrics Displayed

- **CPU Usage**: Percentage utilization with core count information
- **Memory Usage**: Percentage and total memory in MB
- **Disk Usage**: Percentage and total disk space in GB of the root filesystem (`/`), or of the largest filesystem if node_exporter reports no `/`
- **Network Throughput**: Receive and transmit rate in bytes per second, summed over the physical interfaces. Loopback and virtual interfaces (`lo`, `veth*`, `docker*`, `br-*`, `cni*`, `flannel*`, `cali*`, tunnels, …) are not counted
- **Node Info**: Operating system, kernel version, architecture, uptime and the node's clock. The clock offset to the dashboard is highlighted when it is one second or more. The distribution name comes from `node_os_info`, which node_exporter only provides on Linux; other systems show the kernel name instead

When node_exporter itself runs in a container, mount the host root so it reports the host's `/`, and use the host network so it sees the host's interfaces instead of the container's:

```bash
docker run -d --net=host --pid=host -v /:/host:ro,rslave quay.io/prometheus/node-exporter --path.rootfs=/host
```

In Kubernetes, set `hostNetwork: true` and `hostPID: true` on the node_exporter pod.

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

The dashboard is configured with a TOML file. Copy the example and adjust it:

```bash
cp node-dashboard.example.toml node-dashboard.toml

# Reads ./node-dashboard.toml
./node-dashboard

# Or pass the path explicitly
./node-dashboard /etc/node-dashboard/config.toml
```

### Configuration

```toml
port = 8080
node_exporters = [
  "http://node1:9100",
  "http://node2:9100",
]
update_frequency = 10
data_fetch_frequency = 5
data_dir = "/var/lib/node-dashboard"
retention_days = 7

[auth]
enabled = true
user = "admin"
password = "secret"
```

| Key | Default | Description |
|-----|---------|-------------|
| `port` | 8080 | Port to listen on |
| `node_exporters` | - | Node exporter base URLs (required); `/metrics` is appended |
| `update_frequency` | 10 | Dashboard update frequency in seconds |
| `data_fetch_frequency` | 5 | Node exporter data fetch frequency in seconds |
| `data_dir` | ./data | Directory for the metrics history database |
| `retention_days` | 30 | Number of days to keep metrics history |
| `auth.enabled` | false | Enable basic authentication |
| `auth.user` | - | Username for basic authentication (required when enabled) |
| `auth.password` | - | Password for basic authentication (required when enabled) |

Only `node_exporters` is required; omitted keys use their defaults. Unknown keys are rejected so typos do not go unnoticed.

### Metrics History

Every fetched sample is written to `node-dashboard.db` (SQLite) in the data directory, so history survives restarts. Samples older than the retention are deleted hourly. Use the range selector above the charts to switch between 1h, 24h, 1 week and 1 month; ranges longer than the retention are hidden. Longer ranges are averaged to about 300 points per chart. Databases created by an older version are upgraded on start; their earlier samples show no network throughput.

At the default 5 second fetch frequency, a month of history takes roughly 30-50 MB per node.

### Docker Usage

```bash
# Build Docker image
docker build -t node-dashboard .

# Run with Docker, mounting the config and keeping the metrics history in a named volume
docker run -p 8080:8080 \
  -v "$PWD/node-dashboard.toml:/app/config.toml:ro" \
  -v node-dashboard-data:/app/data \
  node-dashboard
```

The image reads `/app/config.toml`. Keep `port = 8080` there, since the health check and `EXPOSE` use that port. The default `data_dir = "./data"` resolves to the `/app/data` volume.

## Development

The project follows standard Go project layout:

- `cmd/node-dashboard/`: Main application entry point
- `internal/metrics/`: Metrics collection and processing
- `internal/storage/`: SQLite storage for the metrics history
- `internal/server/`: Web server and dashboard handling
- `internal/server/templates/`: HTML templates

### Project Structure

```
├── cmd/node-dashboard/     # Main application
├── internal/
│   ├── metrics/           # Metrics collection
│   ├── storage/           # SQLite metrics history
│   └── server/            # Web server and dashboard
├── Dockerfile             # Container build configuration
├── Makefile              # Build automation
└── README.md             # This file
```

## License

This project is open source and available under standard licensing terms.