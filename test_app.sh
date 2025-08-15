#!/bin/bash

# Test script for node dashboard application

echo "Starting node dashboard test..."

# Start the application in background
echo "Starting server on port 8081..."
./build/node-dashboard --port 8081 --node-exporter http://localhost:9100 &
SERVER_PID=$!

# Wait for server to start
sleep 3

# Test if server is responding
echo "Testing server response..."
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8081/)

if [ "$HTTP_STATUS" -eq 200 ]; then
    echo "✓ Server is responding correctly (HTTP $HTTP_STATUS)"
    
    # Test API endpoint
    echo "Testing metrics API..."
    API_RESPONSE=$(curl -s http://localhost:8081/api/metrics)
    
    if [[ $API_RESPONSE == *"cpu"* ]] && [[ $API_RESPONSE == *"memory"* ]] && [[ $API_RESPONSE == *"disk"* ]]; then
        echo "✓ Metrics API is working correctly"
        echo "API Response: $API_RESPONSE"
    else
        echo "✗ Metrics API response is invalid"
        echo "API Response: $API_RESPONSE"
    fi
    
else
    echo "✗ Server is not responding correctly (HTTP $HTTP_STATUS)"
fi

# Clean up
echo "Stopping server..."
kill $SERVER_PID 2>/dev/null
wait $SERVER_PID 2>/dev/null

echo "Test completed."