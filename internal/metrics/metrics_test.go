package metrics

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRoundToDecimal(t *testing.T) {
	tests := []struct {
		value    float64
		decimals int
		expected float64
	}{
		{3.14159, 2, 3.14},
		{3.14159, 3, 3.142},
		{3.14159, 0, 3},
		{2.5, 0, 3},
		{2.4, 0, 2},
	}

	for _, test := range tests {
		result := roundToDecimal(test.value, test.decimals)
		if result != test.expected {
			t.Errorf("roundToDecimal(%f, %d) = %f, expected %f",
				test.value, test.decimals, result, test.expected)
		}
	}
}

// cpuExposition builds a node_exporter scrape with two cores, each with the given idle and user seconds
func cpuExposition(idle0, user0, idle1, user1 float64) string {
	return fmt.Sprintf(`# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} %g
node_cpu_seconds_total{cpu="0",mode="user"} %g
node_cpu_seconds_total{cpu="1",mode="idle"} %g
node_cpu_seconds_total{cpu="1",mode="user"} %g
node_memory_MemTotal_bytes 1000
node_memory_MemAvailable_bytes 250
`, idle0, user0, idle1, user1)
}

func scrape(t *testing.T, c *NodeExporterCollector, exposition string) *MetricsData {
	t.Helper()
	return scrapeAt(t, c, exposition, time.Unix(1_700_000_000, 0))
}

func scrapeAt(t *testing.T, c *NodeExporterCollector, exposition string, at time.Time) *MetricsData {
	t.Helper()
	raw, err := parseMetrics(strings.NewReader(exposition))
	if err != nil {
		t.Fatalf("parseMetrics failed: %v", err)
	}
	return c.toMetricsData(raw, at)
}

func TestCPUUsage(t *testing.T) {
	c := &NodeExporterCollector{}

	// First scrape: average since boot, 300 idle of 400 total seconds
	data := scrape(t, c, cpuExposition(100, 100, 200, 0))
	if data.CPU != 25 {
		t.Errorf("Expected since-boot CPU usage 25, got %v", data.CPU)
	}
	if data.CPUCores != 2 {
		t.Errorf("Expected 2 CPU cores, got %d", data.CPUCores)
	}
	if data.Memory != 75 {
		t.Errorf("Expected memory usage 75, got %v", data.Memory)
	}

	// Second scrape: 10s pass on both cores, core 0 fully busy, core 1 idle
	data = scrape(t, c, cpuExposition(100, 110, 210, 0))
	if data.CPU != 50 {
		t.Errorf("Expected CPU usage 50 between scrapes, got %v", data.CPU)
	}

	// Third scrape: both cores idle
	data = scrape(t, c, cpuExposition(110, 110, 220, 0))
	if data.CPU != 0 {
		t.Errorf("Expected CPU usage 0 between scrapes, got %v", data.CPU)
	}
}

func TestCPUUsageCounterReset(t *testing.T) {
	c := &NodeExporterCollector{}

	scrape(t, c, cpuExposition(1000, 1000, 1000, 1000))

	// After a reboot the counters start again, so fall back to the since-boot average
	data := scrape(t, c, cpuExposition(30, 10, 30, 10))
	if data.CPU != 25 {
		t.Errorf("Expected since-boot CPU usage 25 after counter reset, got %v", data.CPU)
	}
}

func TestCPUUsageNoCPUMetrics(t *testing.T) {
	c := &NodeExporterCollector{}

	data := scrape(t, c, "node_memory_MemTotal_bytes 1000\n")
	if data.CPU != 0 {
		t.Errorf("Expected CPU usage 0 without CPU metrics, got %v", data.CPU)
	}
}

func TestCPUUsageClamped(t *testing.T) {
	c := &NodeExporterCollector{}

	if usage := c.cpuUsage(-10, 100); usage != 100 {
		t.Errorf("Expected CPU usage clamped to 100, got %v", usage)
	}
	if usage := c.cpuUsage(200, 150); usage != 0 {
		t.Errorf("Expected CPU usage clamped to 0, got %v", usage)
	}
}

func TestDiskUsage(t *testing.T) {
	tests := []struct {
		name       string
		exposition string
		disk       float64
		sizeGB     float64
	}{
		{
			name: "root mountpoint is preferred over larger filesystems",
			exposition: `node_filesystem_size_bytes{device="/dev/sda1",device_error="",fstype="ext4",mountpoint="/"} 107374182400
node_filesystem_free_bytes{device="/dev/sda1",device_error="",fstype="ext4",mountpoint="/"} 80530636800
node_filesystem_size_bytes{device="/dev/sdb1",device_error="",fstype="ext4",mountpoint="/data"} 1073741824000
node_filesystem_free_bytes{device="/dev/sdb1",device_error="",fstype="ext4",mountpoint="/data"} 1073741824000
`,
			disk:   25,
			sizeGB: 100,
		},
		{
			name: "container without root mountpoint falls back to the largest device",
			exposition: `node_filesystem_size_bytes{device="/dev/vda1",device_error="",fstype="ext4",mountpoint="/etc/hostname"} 107374182400
node_filesystem_free_bytes{device="/dev/vda1",device_error="",fstype="ext4",mountpoint="/etc/hostname"} 53687091200
node_filesystem_size_bytes{device="/dev/vda1",device_error="",fstype="ext4",mountpoint="/etc/hosts"} 107374182400
node_filesystem_free_bytes{device="/dev/vda1",device_error="",fstype="ext4",mountpoint="/etc/hosts"} 53687091200
node_filesystem_size_bytes{device="tmpfs",device_error="",fstype="tmpfs",mountpoint="/run"} 1073741824
node_filesystem_free_bytes{device="tmpfs",device_error="",fstype="tmpfs",mountpoint="/run"} 1073741824
`,
			disk:   50,
			sizeGB: 100,
		},
		{
			name: "devices with errors are skipped",
			exposition: `node_filesystem_size_bytes{device="/dev/sdb1",device_error="permission denied",fstype="ext4",mountpoint="/"} 0
node_filesystem_size_bytes{device="/dev/sda1",device_error="",fstype="ext4",mountpoint="/data"} 107374182400
node_filesystem_free_bytes{device="/dev/sda1",device_error="",fstype="ext4",mountpoint="/data"} 26843545600
`,
			disk:   75,
			sizeGB: 100,
		},
		{
			name:       "no filesystem metrics",
			exposition: "node_memory_MemTotal_bytes 1000\n",
			disk:       0,
			sizeGB:     0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := scrape(t, &NodeExporterCollector{}, test.exposition)
			if data.Disk != test.disk {
				t.Errorf("Expected disk usage %v, got %v", test.disk, data.Disk)
			}
			if data.DiskSizeGB != test.sizeGB {
				t.Errorf("Expected disk size %v GB, got %v", test.sizeGB, data.DiskSizeGB)
			}
		})
	}
}

func TestNetRates(t *testing.T) {
	c := &NodeExporterCollector{}
	base := time.Unix(1_700_000_000, 0)

	// First scrape has nothing to compare against
	if rx, tx := c.netRates(1000, 500, base); rx != 0 || tx != 0 {
		t.Errorf("Expected 0 on first scrape, got %v/%v", rx, tx)
	}

	if rx, tx := c.netRates(11000, 2500, base.Add(5*time.Second)); rx != 2000 || tx != 400 {
		t.Errorf("Expected 2000/400 B/s, got %v/%v", rx, tx)
	}

	// Counters reset by a reboot
	if rx, tx := c.netRates(100, 50, base.Add(10*time.Second)); rx != 0 || tx != 0 {
		t.Errorf("Expected 0 after counter reset, got %v/%v", rx, tx)
	}

	// No time passed
	if rx, tx := c.netRates(200, 100, base.Add(10*time.Second)); rx != 0 || tx != 0 {
		t.Errorf("Expected 0 without elapsed time, got %v/%v", rx, tx)
	}
}

func TestNetDevicesExcluded(t *testing.T) {
	exposition := func(eth0 float64) string {
		return fmt.Sprintf(`node_network_receive_bytes_total{device="lo"} 1e9
node_network_receive_bytes_total{device="eth0"} %g
node_network_receive_bytes_total{device="veth123"} 1e9
node_network_receive_bytes_total{device="docker0"} 1e9
node_network_transmit_bytes_total{device="lo"} 1e9
node_network_transmit_bytes_total{device="eth0"} %g
`, eth0, eth0/2)
	}

	c := &NodeExporterCollector{}
	base := time.Unix(1_700_000_000, 0)
	scrapeAt(t, c, exposition(1000), base)
	data := scrapeAt(t, c, exposition(11000), base.Add(10*time.Second))

	if data.NetRxBps != 1000 || data.NetTxBps != 500 {
		t.Errorf("Expected only eth0 to count (1000/500 B/s), got %v/%v", data.NetRxBps, data.NetTxBps)
	}
}

func TestNodeInfo(t *testing.T) {
	scrapedAt := time.Unix(1_700_000_000, 0)

	t.Run("linux", func(t *testing.T) {
		data := scrapeAt(t, &NodeExporterCollector{}, `node_uname_info{domainname="(none)",machine="x86_64",nodename="web-1",release="6.8.0-45-generic",sysname="Linux",version="#45"} 1
node_os_info{id="ubuntu",name="Ubuntu",pretty_name="Ubuntu 24.04.1 LTS",version="24.04.1 LTS (Noble Numbat)",version_id="24.04"} 1
node_time_seconds 1.7000000025e+09
node_boot_time_seconds 1.6999136e+09
node_time_zone_offset_seconds{time_zone="CET"} 3600
`, scrapedAt)

		if data.OSName != "Ubuntu 24.04.1 LTS" || data.Kernel != "6.8.0-45-generic" || data.Arch != "x86_64" {
			t.Errorf("Unexpected OS info: %q %q %q", data.OSName, data.Kernel, data.Arch)
		}
		if data.Uptime != 86402500*time.Millisecond {
			t.Errorf("Expected uptime 1d 2.5s, got %v", data.Uptime)
		}
		if data.ClockOffset != 2500*time.Millisecond {
			t.Errorf("Expected clock offset 2.5s, got %v", data.ClockOffset)
		}
		if name, offset := data.NodeTime.Zone(); name != "CET" || offset != 3600 || data.TimeZone != "CET" {
			t.Errorf("Expected node time in CET, got %s %d", name, offset)
		}
	})

	t.Run("darwin without node_os_info", func(t *testing.T) {
		data := scrapeAt(t, &NodeExporterCollector{}, `node_uname_info{machine="arm64",nodename="mac",release="25.0.0",sysname="Darwin",version="Darwin Kernel"} 1
`, scrapedAt)

		if data.OSName != "Darwin" || data.Arch != "arm64" {
			t.Errorf("Expected Darwin/arm64 fallback, got %q %q", data.OSName, data.Arch)
		}
		if !data.NodeTime.IsZero() || data.Uptime != 0 || data.ClockOffset != 0 {
			t.Errorf("Expected no time info, got %v %v %v", data.NodeTime, data.Uptime, data.ClockOffset)
		}
	})
}
