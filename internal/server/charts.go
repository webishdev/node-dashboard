package server

import (
	"fmt"
	"strings"

	"github.com/webishdev/node-dashboard/internal/metrics"
)

// ChartConfig holds configuration for SVG chart generation
type ChartConfig struct {
	Width  int
	Height int
	Color  string
}

// GenerateSVGLineChart creates an inline SVG line chart from data points
func GenerateSVGLineChart(dataPoints []metrics.DataPoint, config ChartConfig) string {
	if len(dataPoints) == 0 {
		return generateEmptyChart(config)
	}

	// Find min and max values for scaling
	minVal, maxVal := findMinMax(dataPoints)

	// Add some padding to the range
	valueRange := maxVal - minVal
	if valueRange == 0 {
		valueRange = 1 // Avoid division by zero
	}

	// Generate SVG path
	pathData := generatePathData(dataPoints, config, minVal, maxVal)

	// Create the complete SVG
	svg := fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">
		<defs>
			<linearGradient id="gradient-%s" x1="0%%" y1="0%%" x2="0%%" y2="100%%">
				<stop offset="0%%" style="stop-color:%s;stop-opacity:0.3" />
				<stop offset="100%%" style="stop-color:%s;stop-opacity:0.1" />
			</linearGradient>
		</defs>
		<rect width="100%%" height="100%%" fill="none"/>
		<path d="%s" stroke="%s" stroke-width="2" fill="none"/>
		<path d="%s" fill="url(#gradient-%s)" opacity="0.3"/>
	</svg>`,
		config.Width, config.Height, config.Width, config.Height,
		strings.ReplaceAll(config.Color, "#", ""), config.Color, config.Color,
		pathData, config.Color,
		generateAreaPath(dataPoints, config, minVal, maxVal), strings.ReplaceAll(config.Color, "#", ""))

	return svg
}

// generateEmptyChart creates an empty chart when no data is available
func generateEmptyChart(config ChartConfig) string {
	return fmt.Sprintf(`<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">
		<rect width="100%%" height="100%%" fill="none"/>
		<text x="50%%" y="50%%" text-anchor="middle" fill="#9CA3AF" font-size="12">No data</text>
	</svg>`, config.Width, config.Height, config.Width, config.Height)
}

// findMinMax finds the minimum and maximum values in the data points
func findMinMax(dataPoints []metrics.DataPoint) (float64, float64) {
	if len(dataPoints) == 0 {
		return 0, 100
	}

	min := dataPoints[0].Value
	max := dataPoints[0].Value

	for _, point := range dataPoints {
		if point.Value < min {
			min = point.Value
		}
		if point.Value > max {
			max = point.Value
		}
	}

	// Ensure we have a reasonable range for percentage values
	if min > 0 {
		min = 0
	}
	if max < 100 {
		max = 100
	}

	return min, max
}

// generatePathData creates the SVG path data for the line chart
func generatePathData(dataPoints []metrics.DataPoint, config ChartConfig, minVal, maxVal float64) string {
	if len(dataPoints) == 0 {
		return ""
	}

	var pathParts []string
	valueRange := maxVal - minVal
	if valueRange == 0 {
		valueRange = 1
	}

	for i, point := range dataPoints {
		x := float64(i) * float64(config.Width-1) / float64(len(dataPoints)-1)
		y := float64(config.Height-1) - ((point.Value-minVal)/valueRange)*float64(config.Height-1)

		if i == 0 {
			pathParts = append(pathParts, fmt.Sprintf("M %.2f %.2f", x, y))
		} else {
			pathParts = append(pathParts, fmt.Sprintf("L %.2f %.2f", x, y))
		}
	}

	return strings.Join(pathParts, " ")
}

// generateAreaPath creates the SVG path data for the filled area under the line
func generateAreaPath(dataPoints []metrics.DataPoint, config ChartConfig, minVal, maxVal float64) string {
	if len(dataPoints) == 0 {
		return ""
	}

	var pathParts []string
	valueRange := maxVal - minVal
	if valueRange == 0 {
		valueRange = 1
	}

	// Start from bottom left
	pathParts = append(pathParts, fmt.Sprintf("M 0 %d", config.Height))

	// Draw line to first point
	firstY := float64(config.Height-1) - ((dataPoints[0].Value-minVal)/valueRange)*float64(config.Height-1)
	pathParts = append(pathParts, fmt.Sprintf("L 0 %.2f", firstY))

	// Draw the line
	for i, point := range dataPoints {
		x := float64(i) * float64(config.Width-1) / float64(len(dataPoints)-1)
		y := float64(config.Height-1) - ((point.Value-minVal)/valueRange)*float64(config.Height-1)
		pathParts = append(pathParts, fmt.Sprintf("L %.2f %.2f", x, y))
	}

	// Close the path at bottom right
	lastX := float64(len(dataPoints)-1) * float64(config.Width-1) / float64(len(dataPoints)-1)
	pathParts = append(pathParts, fmt.Sprintf("L %.2f %d", lastX, config.Height))
	pathParts = append(pathParts, "Z")

	return strings.Join(pathParts, " ")
}

// GetChartConfigs returns predefined chart configurations for different metrics
func GetChartConfigs() map[string]ChartConfig {
	return map[string]ChartConfig{
		"cpu": {
			Width:  200,
			Height: 60,
			Color:  "#3B82F6", // blue-500
		},
		"memory": {
			Width:  200,
			Height: 60,
			Color:  "#10B981", // green-500
		},
		"disk": {
			Width:  200,
			Height: 60,
			Color:  "#8B5CF6", // purple-500
		},
	}
}
