package server

import (
	"strings"
	"testing"
	"time"

	"github.com/webishdev/node-dashboard/internal/metrics"
)

func TestSplitSegments(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	points := []metrics.DataPoint{
		{Timestamp: base},
		{Timestamp: base.Add(10 * time.Second)},
		{Timestamp: base.Add(20 * time.Second)},
		{Timestamp: base.Add(5 * time.Minute)}, // gap
		{Timestamp: base.Add(5*time.Minute + 10*time.Second)},
	}

	segments := splitSegments(points, 10*time.Second)
	if len(segments) != 2 {
		t.Fatalf("Expected 2 segments, got %d", len(segments))
	}
	if len(segments[0]) != 3 || len(segments[1]) != 2 {
		t.Errorf("Expected segments of 3 and 2 points, got %d and %d", len(segments[0]), len(segments[1]))
	}
}

func TestTimeX(t *testing.T) {
	config := ChartConfig{Width: 301, Height: 64}
	start := time.Unix(1_700_000_000, 0)
	window := ChartWindow{Start: start, End: start.Add(100 * time.Second), Bucket: 0}

	tests := []struct {
		ts       time.Time
		expected float64
	}{
		{start, 0},
		{start.Add(50 * time.Second), 150},
		{start.Add(100 * time.Second), 300},
		{start.Add(-time.Hour), 0},  // clamped
		{start.Add(time.Hour), 300}, // clamped
	}

	for _, test := range tests {
		if x := timeX(test.ts, config, window); x != test.expected {
			t.Errorf("timeX(%v) = %v, expected %v", test.ts.Sub(start), x, test.expected)
		}
	}
}

func TestGenerateSVGLineChartGap(t *testing.T) {
	config := ChartConfig{Width: 300, Height: 64, Color: "#000000"}
	start := time.Unix(1_700_000_000, 0)
	window := ChartWindow{Start: start, End: start.Add(time.Hour), Bucket: 12 * time.Second}
	points := []metrics.DataPoint{
		{Timestamp: start, Value: 10},
		{Timestamp: start.Add(12 * time.Second), Value: 20},
		{Timestamp: start.Add(30 * time.Minute), Value: 30},
	}

	svg := GenerateSVGLineChart(points, config, window)

	// Two segments mean two "M" commands in both the area and the line path
	if count := strings.Count(svg, "M "); count != 4 {
		t.Errorf("Expected 4 move commands for 2 segments, got %d", count)
	}
}

func TestGenerateSVGLineChartEmpty(t *testing.T) {
	svg := GenerateSVGLineChart(nil, ChartConfig{Width: 300, Height: 64}, ChartWindow{})
	if !strings.Contains(svg, "No data") {
		t.Error("Expected empty chart placeholder")
	}
}

func TestFindMinMax(t *testing.T) {
	tests := []struct {
		name     string
		values   []float64
		percent  bool
		min, max float64
	}{
		{"low values use the minimum top", []float64{1.9, 3.0}, true, 0, minChartMax},
		{"top has headroom above the data", []float64{20, 40}, true, 0, 50},
		{"top is capped at 100", []float64{50, 95}, true, 0, 100},
		{"negative values lower the bottom", []float64{-5, 4}, true, -5, minChartMax},
		{"rates are not capped at 100", []float64{2000, 8000}, false, 0, 10000},
		{"low rates use the minimum rate top", []float64{10, 20}, false, 0, minRateChartMax},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			points := make([]metrics.DataPoint, len(test.values))
			for i, v := range test.values {
				points[i] = metrics.DataPoint{Value: v}
			}
			min, max := findMinMax([][]metrics.DataPoint{points}, test.percent)
			if min != test.min || max != test.max {
				t.Errorf("Expected range %v-%v, got %v-%v", test.min, test.max, min, max)
			}
		})
	}
}

func TestGenerateSVGMultiLineChart(t *testing.T) {
	config := ChartConfig{Width: 300, Height: 64}
	start := time.Unix(1_700_000_000, 0)
	window := ChartWindow{Start: start, End: start.Add(time.Minute), Bucket: 5 * time.Second}
	points := []metrics.DataPoint{
		{Timestamp: start, Value: 1000},
		{Timestamp: start.Add(5 * time.Second), Value: 2000},
	}

	svg := GenerateSVGMultiLineChart([]ChartSeries{
		{Points: points, Color: "#111111"},
		{Points: points, Color: "#222222"},
	}, config, window)

	// One line per series, but only the first series gets an area
	if !strings.Contains(svg, `stroke="#111111"`) || !strings.Contains(svg, `stroke="#222222"`) {
		t.Error("Expected a line for each series")
	}
	if count := strings.Count(svg, "fill=\"url(#"); count != 1 {
		t.Errorf("Expected 1 filled area, got %d", count)
	}

	empty := GenerateSVGMultiLineChart([]ChartSeries{{Color: "#111111"}, {Color: "#222222"}}, config, window)
	if !strings.Contains(empty, "No data") {
		t.Error("Expected empty chart placeholder when all series are empty")
	}
}
