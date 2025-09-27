#!/bin/bash

# Test setup script for Gordion Relay System
# This script helps test the tunnel system locally

set -e

echo "🚀 Setting up Gordion Relay Test Environment"

# Create test directories
mkdir -p test-env/relay
mkdir -p test-env/hospital

# Create relay config
cat > test-env/relay/config.json << EOF
{
  "listen_addr": ":8443",
  "domain": "localhost",
  "tls": {
    "auto_cert": false,
    "cert_file": "test-cert.pem",
    "key_file": "test-key.pem"
  },
  "hospitals": [
    {
      "code": "test-hospital",
      "subdomain": "test-hospital.localhost"
    }
  ],
  "idle_timeout": "30s",
  "max_concurrent_conn": 100,
  "request_timeout": "30s",
  "metrics_addr": ":8080"
}
EOF

# Create test certificate (self-signed for testing)
cd test-env/relay
if [ ! -f test-cert.pem ]; then
    echo "🔐 Creating self-signed certificate for testing..."
    openssl req -x509 -newkey rsa:4096 -keyout test-key.pem -out test-cert.pem -days 365 -nodes \
        -subj "/C=TR/ST=Ankara/L=Ankara/O=Test/CN=*.localhost"
fi
cd ../..

# Create hospital config
cat > test-env/hospital/config.json << EOF
{
  "tunnel": {
    "enabled": true,
    "relay_addr": "localhost:8443",
    "hospital_code": "test-hospital",
    "subdomain": "test-hospital.localhost",
    "local_addr": "localhost:8083",
    "heartbeat_interval": "30s",
    "max_retries": 10,
    "retry_delay": "5s"
  }
}
EOF

# Create a simple HTTP server to simulate gordionedge
cat > test-env/hospital/mock-server.py << 'EOF'
#!/usr/bin/env python3
import http.server
import socketserver
import json
from urllib.parse import urlparse

class MockGordionHandler(http.server.SimpleHTTPRequestHandler):
    def do_GET(self):
        print(f"Mock Hospital Server: {self.command} {self.path}")

        if self.path.startswith('/api/instances/'):
            # Mock DICOM file download
            self.send_response(200)
            self.send_header('Content-Type', 'application/dicom')
            self.send_header('Content-Length', '1024')
            self.end_headers()
            self.wfile.write(b'MOCK_DICOM_DATA' + b'0' * 1008)  # 1024 bytes total
        elif self.path == '/health':
            # Health check
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            response = json.dumps({"status": "ok", "service": "mock-gordionedge"})
            self.wfile.write(response.encode())
        else:
            # Default response
            self.send_response(200)
            self.send_header('Content-Type', 'text/html')
            self.end_headers()
            self.wfile.write(b'<html><body><h1>Mock Gordionedge Hospital Server</h1></body></html>')

if __name__ == "__main__":
    PORT = 8083
    with socketserver.TCPServer(("", PORT), MockGordionHandler) as httpd:
        print(f"Mock Hospital Server running on port {PORT}")
        httpd.serve_forever()
EOF

chmod +x test-env/hospital/mock-server.py

# Create test runner script
cat > test-env/run-test.sh << 'EOF'
#!/bin/bash

echo "🏥 Starting Test Environment"

# Function to cleanup on exit
cleanup() {
    echo "🧹 Cleaning up..."
    kill $(jobs -p) 2>/dev/null || true
    wait 2>/dev/null || true
}

trap cleanup EXIT

# Start mock hospital server
echo "🚀 Starting mock hospital server on :8083..."
cd hospital
python3 mock-server.py &
HOSPITAL_PID=$!
cd ..

# Wait for hospital server to start
sleep 2

# Test hospital server
echo "🧪 Testing hospital server..."
curl -s http://localhost:8083/health || {
    echo "❌ Hospital server not responding"
    exit 1
}
echo "✅ Hospital server running"

# Start relay server
echo "🚀 Starting relay server on :8443..."
cd relay
../../../relay -config config.json &
RELAY_PID=$!
cd ..

# Wait for relay server to start
sleep 3

# Test relay server
echo "🧪 Testing relay server..."
curl -s http://localhost:8080/health || {
    echo "❌ Relay server not responding"
    exit 1
}
echo "✅ Relay server running"

echo "🎉 Test environment ready!"
echo ""
echo "📋 Test URLs:"
echo "  - Relay Status: http://localhost:8080/status"
echo "  - Hospital Direct: http://localhost:8083/health"
echo "  - Hospital via Tunnel: https://test-hospital.localhost:8443/health (after tunnel connects)"
echo ""
echo "📝 To test tunnel, configure gordionedge with hospital/config.json"
echo "   or run: ./test-tunnel.sh"
echo ""
echo "Press Ctrl+C to stop all services"

# Keep running
wait
EOF

chmod +x test-env/run-test.sh

# Create tunnel test script
cat > test-env/test-tunnel.sh << 'EOF'
#!/bin/bash

echo "🔌 Testing Tunnel Connection"

# Test if relay is running
if ! curl -s http://localhost:8080/health > /dev/null; then
    echo "❌ Relay server not running. Run ./run-test.sh first"
    exit 1
fi

# Test if hospital is running
if ! curl -s http://localhost:8083/health > /dev/null; then
    echo "❌ Hospital server not running. Run ./run-test.sh first"
    exit 1
fi

echo "✅ Both servers are running"

# Check relay status
echo "📊 Relay Status:"
curl -s http://localhost:8080/status | python3 -m json.tool

echo ""
echo "🔗 To complete the test:"
echo "1. Configure gordionedge with tunnel config from hospital/config.json"
echo "2. Check connection: curl -s http://localhost:8080/status"
echo "3. Test tunnel: curl -k https://test-hospital.localhost:8443/health"
EOF

chmod +x test-env/test-tunnel.sh

echo "✅ Test environment created in test-env/"
echo ""
echo "📋 Next steps:"
echo "1. Build relay server: go build -o test-env/relay main.go"
echo "2. Run test: cd test-env && ./run-test.sh"
echo "3. Test tunnel: cd test-env && ./test-tunnel.sh"
echo ""
echo "🔧 For full integration test with gordionedge:"
echo "1. Update gordionedge config with tunnel settings from test-env/hospital/config.json"
echo "2. Start gordionedge with tunnel enabled"
echo "3. Access hospital via: https://test-hospital.localhost:8443/api/instances/123/download"