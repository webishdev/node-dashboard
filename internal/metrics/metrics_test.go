package metrics

import (
	"testing"
)

func TestNewHistoricalStore(t *testing.T) {
	maxPoints := 100
	store := NewHistoricalStore(maxPoints)

	if store == nil {
		t.Fatal("NewHistoricalStore returned nil")
	}

	if store.maxPoints != maxPoints {
		t.Errorf("Expected maxPoints to be %d, got %d", maxPoints, store.maxPoints)
	}

	if store.data == nil {
		t.Error("Expected data map to be initialized")
	}
}

func TestAddDataPoint(t *testing.T) {
	store := NewHistoricalStore(5)
	nodeName := "test-node"

	metricsData := &MetricsData{
		CPU:    50.5,
		Memory: 75.2,
		Disk:   80.1,
	}

	// Add first data point
	store.AddDataPoint(nodeName, metricsData)

	historical := store.GetHistoricalData(nodeName)
	if historical == nil {
		t.Fatal("Expected historical data to be created")
	}

	if len(historical.CPU) != 1 {
		t.Errorf("Expected 1 CPU data point, got %d", len(historical.CPU))
	}

	if historical.CPU[0].Value != 50.5 {
		t.Errorf("Expected CPU value 50.5, got %f", historical.CPU[0].Value)
	}

	if len(historical.Memory) != 1 {
		t.Errorf("Expected 1 Memory data point, got %d", len(historical.Memory))
	}

	if historical.Memory[0].Value != 75.2 {
		t.Errorf("Expected Memory value 75.2, got %f", historical.Memory[0].Value)
	}
}

func TestMaxPointsLimit(t *testing.T) {
	maxPoints := 3
	store := NewHistoricalStore(maxPoints)
	nodeName := "test-node"

	// Add more data points than the limit
	for i := 0; i < 5; i++ {
		metricsData := &MetricsData{
			CPU:    float64(i * 10),
			Memory: float64(i * 20),
			Disk:   float64(i * 30),
		}
		store.AddDataPoint(nodeName, metricsData)
	}

	historical := store.GetHistoricalData(nodeName)

	// Should only keep the last 3 points
	if len(historical.CPU) != maxPoints {
		t.Errorf("Expected %d CPU data points, got %d", maxPoints, len(historical.CPU))
	}

	if len(historical.Memory) != maxPoints {
		t.Errorf("Expected %d Memory data points, got %d", maxPoints, len(historical.Memory))
	}

	if len(historical.Disk) != maxPoints {
		t.Errorf("Expected %d Disk data points, got %d", maxPoints, len(historical.Disk))
	}

	// Check that we kept the last values (20, 30, 40)
	expectedCPUValues := []float64{20, 30, 40}
	for i, expected := range expectedCPUValues {
		if historical.CPU[i].Value != expected {
			t.Errorf("Expected CPU value %f at index %d, got %f", expected, i, historical.CPU[i].Value)
		}
	}
}

func TestGetHistoricalDataNonExistentNode(t *testing.T) {
	store := NewHistoricalStore(10)

	historical := store.GetHistoricalData("non-existent-node")

	if historical == nil {
		t.Fatal("Expected empty historical data, got nil")
	}

	if len(historical.CPU) != 0 {
		t.Errorf("Expected 0 CPU data points for non-existent node, got %d", len(historical.CPU))
	}

	if len(historical.Memory) != 0 {
		t.Errorf("Expected 0 Memory data points for non-existent node, got %d", len(historical.Memory))
	}

	if len(historical.Disk) != 0 {
		t.Errorf("Expected 0 Disk data points for non-existent node, got %d", len(historical.Disk))
	}
}

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
