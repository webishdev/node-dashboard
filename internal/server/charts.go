package server

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/webishdev/node-dashboard/internal/metrics"
)

// ChartConfig holds configuration for SVG chart generation
type ChartConfig struct {
	Width   int
	Height  int
	Color   string
	Percent bool // the y axis is capped at 100 and starts at minChartMax
}

// ChartSeries is one line of a chart
type ChartSeries struct {
	Points []metrics.DataPoint
	Color  string
}

// ChartWindow is the time span a chart covers and the averaging interval of its points
type ChartWindow struct {
	Start  time.Time
	End    time.Time
	Bucket time.Duration
}

const (
	// minChartMax is the smallest top of the y axis, so noise around 0% is not blown up
	minChartMax = 10.0
	// minRateChartMax is the smallest top of the y axis for throughput charts, 1 KB/s
	minRateChartMax = 1000.0
)

// Line colors of the network chart, matching the legend in the template
const (
	netRxColor = "#0EA5E9" // sky-500
	netTxColor = "#F59E0B" // amber-500
)

// chartCounter makes gradient IDs unique when several charts share a page
var chartCounter atomic.Uint64

// GenerateSVGLineChart creates an inline SVG line chart from data points, placed by timestamp within the window
func GenerateSVGLineChart(dataPoints []metrics.DataPoint, config ChartConfig, window ChartWindow) string {
	return GenerateSVGMultiLineChart([]ChartSeries{{Points: dataPoints, Color: config.Color}}, config, window)
}

// GenerateSVGMultiLineChart creates an inline SVG chart with one line per series on a shared y axis.
// Only the first series gets a filled area, so overlapping lines stay readable.
func GenerateSVGMultiLineChart(series []ChartSeries, config ChartConfig, window ChartWindow) string {
	points := make([][]metrics.DataPoint, len(series))
	empty := true
	for i, s := range series {
		points[i] = s.Points
		if len(s.Points) > 0 {
			empty = false
		}
	}
	if empty {
		return generateEmptyChart(config)
	}

	// Find min and max values for scaling
	minVal, maxVal := findMinMax(points, config.Percent)

	var defs, paths strings.Builder
	for i, s := range series {
		if len(s.Points) == 0 {
			continue
		}

		// Split the data where samples are missing so outages show as gaps
		var linePaths, areaPaths []string
		for _, segment := range splitSegments(s.Points, window.Bucket) {
			coords := segmentCoords(segment, config, window, minVal, maxVal)
			linePaths = append(linePaths, generatePathData(coords))
			areaPaths = append(areaPaths, generateAreaPath(coords, config))
		}

		if i == 0 {
			gradientID := fmt.Sprintf("gradient-%s-%d", strings.ReplaceAll(s.Color, "#", ""), chartCounter.Add(1))
			fmt.Fprintf(&defs, `
			<linearGradient id="%s" x1="0%%" y1="0%%" x2="0%%" y2="100%%">
				<stop offset="0%%" style="stop-color:%s;stop-opacity:0.35" />
				<stop offset="100%%" style="stop-color:%s;stop-opacity:0" />
			</linearGradient>`, gradientID, s.Color, s.Color)
			fmt.Fprintf(&paths, `
		<path d="%s" fill="url(#%s)"/>`, strings.Join(areaPaths, " "), gradientID)
		}
		fmt.Fprintf(&paths, `
		<path d="%s" stroke="%s" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" fill="none" vector-effect="non-scaling-stroke"/>`,
			strings.Join(linePaths, " "), s.Color)
	}

	// Create the complete SVG
	return fmt.Sprintf(`<svg class="w-full h-16" viewBox="0 0 %d %d" preserveAspectRatio="none" xmlns="http://www.w3.org/2000/svg">
		<defs>%s
		</defs>%s
	</svg>`, config.Width, config.Height, defs.String(), paths.String())
}

// generateEmptyChart creates an empty chart when no data is available
func generateEmptyChart(config ChartConfig) string {
	return fmt.Sprintf(`<svg class="w-full h-16" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">
		<text x="50%%" y="50%%" text-anchor="middle" dominant-baseline="middle" fill="#94A3B8" font-size="12">No data</text>
	</svg>`, config.Width, config.Height)
}

// findMinMax finds the y axis range for all series; percent charts are capped at 100
func findMinMax(series [][]metrics.DataPoint, percent bool) (float64, float64) {
	min, max := 0.0, 0.0
	for _, dataPoints := range series {
		for _, point := range dataPoints {
			if point.Value < min {
				min = point.Value
			}
			if point.Value > max {
				max = point.Value
			}
		}
	}

	// Anchor values at 0 and scale to the data with some headroom,
	// so low utilization does not collapse into a flat line at the bottom
	max *= 1.25
	floor := minRateChartMax
	if percent {
		floor = minChartMax
	}
	if max < floor {
		max = floor
	}
	if percent && max > 100 {
		max = 100
	}

	return min, max
}

// splitSegments splits data points wherever consecutive points are more than two buckets apart
func splitSegments(dataPoints []metrics.DataPoint, bucket time.Duration) [][]metrics.DataPoint {
	var segments [][]metrics.DataPoint
	start := 0
	for i := 1; i < len(dataPoints); i++ {
		if bucket > 0 && dataPoints[i].Timestamp.Sub(dataPoints[i-1].Timestamp) > 2*bucket {
			segments = append(segments, dataPoints[start:i])
			start = i
		}
	}
	return append(segments, dataPoints[start:])
}

// chartPoint is a data point in SVG coordinates
type chartPoint struct {
	X, Y float64
}

// timeX returns the x coordinate of a bucket, placed at its center and clamped to the window
func timeX(ts time.Time, config ChartConfig, window ChartWindow) float64 {
	span := window.End.Sub(window.Start)
	if span <= 0 {
		return float64(config.Width - 1)
	}

	frac := float64(ts.Add(window.Bucket/2).Sub(window.Start)) / float64(span)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return frac * float64(config.Width-1)
}

// valueY returns the y coordinate of a value
func valueY(value float64, config ChartConfig, minVal, maxVal float64) float64 {
	valueRange := maxVal - minVal
	if valueRange == 0 {
		valueRange = 1 // Avoid division by zero
	}
	return float64(config.Height-1) - ((value-minVal)/valueRange)*float64(config.Height-1)
}

// segmentCoords converts a segment to SVG coordinates; a single point is drawn as a short flat line
func segmentCoords(segment []metrics.DataPoint, config ChartConfig, window ChartWindow, minVal, maxVal float64) []chartPoint {
	coords := make([]chartPoint, len(segment))
	for i, point := range segment {
		coords[i] = chartPoint{
			X: timeX(point.Timestamp, config, window),
			Y: valueY(point.Value, config, minVal, maxVal),
		}
	}

	if len(coords) == 1 {
		// Span one bucket (at least 2 units) so the point stays visible, keeping it inside the chart
		width := 2.0
		if span := window.End.Sub(window.Start); span > 0 {
			if w := float64(window.Bucket) / float64(span) * float64(config.Width-1); w > width {
				width = w
			}
		}
		x := coords[0].X
		if x+width > float64(config.Width-1) {
			x = float64(config.Width-1) - width
		}
		coords = []chartPoint{{X: x, Y: coords[0].Y}, {X: x + width, Y: coords[0].Y}}
	}

	return coords
}

// generatePathData creates the SVG path data for the line of one segment
func generatePathData(coords []chartPoint) string {
	pathParts := make([]string, len(coords))
	for i, c := range coords {
		command := "L"
		if i == 0 {
			command = "M"
		}
		pathParts[i] = fmt.Sprintf("%s %.2f %.2f", command, c.X, c.Y)
	}
	return strings.Join(pathParts, " ")
}

// generateAreaPath creates the SVG path data for the filled area under the line of one segment
func generateAreaPath(coords []chartPoint, config ChartConfig) string {
	first, last := coords[0], coords[len(coords)-1]

	pathParts := []string{fmt.Sprintf("M %.2f %d", first.X, config.Height)}
	for _, c := range coords {
		pathParts = append(pathParts, fmt.Sprintf("L %.2f %.2f", c.X, c.Y))
	}
	pathParts = append(pathParts, fmt.Sprintf("L %.2f %d", last.X, config.Height), "Z")

	return strings.Join(pathParts, " ")
}

// GetChartConfigs returns predefined chart configurations for different metrics
func GetChartConfigs() map[string]ChartConfig {
	return map[string]ChartConfig{
		"cpu": {
			Width:   300,
			Height:  64,
			Color:   "#6366F1", // indigo-500
			Percent: true,
		},
		"memory": {
			Width:   300,
			Height:  64,
			Color:   "#10B981", // emerald-500
			Percent: true,
		},
		"disk": {
			Width:   300,
			Height:  64,
			Color:   "#8B5CF6", // violet-500
			Percent: true,
		},
		"network": {
			Width:  300,
			Height: 64,
			Color:  netRxColor,
		},
	}
}
