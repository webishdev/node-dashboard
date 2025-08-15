#!/bin/bash

echo "Testing Multi-Node Dashboard"
echo "============================"

# Test 1: Single node (backward compatibility)
echo "Test 1: Single node configuration"
echo "./node-dashboard --node-exporter http://localhost:9100"
echo ""

# Test 2: Multiple nodes with default names
echo "Test 2: Multiple nodes with default names"
echo "./node-dashboard --node-exporter http://localhost:9100 --node-exporter http://localhost:9101"
echo ""

# Test 3: Multiple nodes (will get default names Node-1, Node-2, etc.)
echo "Test 3: Multiple nodes with default names"
echo "./node-dashboard --node-exporter http://localhost:9100 --node-exporter http://localhost:9101 --node-exporter http://localhost:9102"
echo ""

# Note: Custom node names can be implemented in future versions
echo "Note: Custom node names are automatically generated as Node-1, Node-2, etc."
echo ""

echo "To test the application:"
echo "1. Start one or more node_exporter instances on different ports"
echo "2. Run one of the commands above"
echo "3. Open http://localhost:8080 in your browser"
echo "4. Verify that metrics are displayed for each configured node"
echo ""

echo "Example: Starting the application with multiple nodes:"
echo "./node-dashboard --node-exporter 'Local-Node,http://localhost:9100' --node-exporter 'Remote-Node,http://localhost:9101'"