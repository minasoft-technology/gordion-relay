#!/bin/bash

# hospital-setup.sh - Generate hospital-specific configuration
# Usage: ./hospital-setup.sh <hospital-code> [relay-domain]

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Check arguments
if [ $# -lt 1 ]; then
    echo -e "${RED}Usage: $0 <hospital-code> [relay-domain]${NC}"
    echo "Example: $0 ankara zenpacs.com.tr"
    exit 1
fi

HOSPITAL_CODE="$1"
RELAY_DOMAIN="${2:-zenpacs.com.tr}"
RELAY_ADDR="relay.${RELAY_DOMAIN}:443"
SUBDOMAIN="${HOSPITAL_CODE}.${RELAY_DOMAIN}"

echo -e "${BLUE}🏥 Generating configuration for hospital: $HOSPITAL_CODE${NC}"
echo "=================================================="

# Create output directory
OUTPUT_DIR="./hospital-configs/$HOSPITAL_CODE"
mkdir -p "$OUTPUT_DIR"

# Get token for this hospital
TOKEN_FILE="./generated-tokens/hospital-tokens.env"
if [ -f "$TOKEN_FILE" ]; then
    # Source the token file to get the token
    source "$TOKEN_FILE"
    TOKEN_VAR="${HOSPITAL_CODE^^}_TOKEN"
    TOKEN="${!TOKEN_VAR:-}"

    if [ -z "$TOKEN" ]; then
        echo -e "${RED}❌ Token for $HOSPITAL_CODE not found in $TOKEN_FILE${NC}"
        echo -e "${YELLOW}Run ./scripts/generate-tokens.sh first${NC}"
        exit 1
    fi
else
    echo -e "${RED}❌ Token file not found: $TOKEN_FILE${NC}"
    echo -e "${YELLOW}Run ./scripts/generate-tokens.sh first${NC}"
    exit 1
fi

# Generate gordionedge tunnel configuration
cat > "$OUTPUT_DIR/tunnel-config.json" << EOF
{
  "tunnel": {
    "enabled": true,
    "relay_addr": "$RELAY_ADDR",
    "hospital_code": "$HOSPITAL_CODE",
    "subdomain": "$SUBDOMAIN",
    "local_addr": "localhost:8083",
    "token": "$TOKEN",
    "heartbeat_interval": "30s",
    "max_retries": 10,
    "retry_delay": "5s"
  }
}
EOF

# Generate environment variables file
cat > "$OUTPUT_DIR/tunnel.env" << EOF
# Tunnel configuration for $HOSPITAL_CODE
# Add these to your gordionedge environment

GORDION_TUNNEL_ENABLED=true
GORDION_TUNNEL_TOKEN="$TOKEN"
GORDION_TUNNEL_RELAY_ADDR="$RELAY_ADDR"
GORDION_TUNNEL_HOSPITAL_CODE="$HOSPITAL_CODE"
GORDION_TUNNEL_SUBDOMAIN="$SUBDOMAIN"
EOF

# Generate Docker Compose override for tunnel
cat > "$OUTPUT_DIR/docker-compose.tunnel.yml" << EOF
# Docker Compose override for $HOSPITAL_CODE tunnel
# Usage: docker-compose -f docker-compose.yml -f docker-compose.tunnel.yml up

version: '3.8'

services:
  gordionedge:
    environment:
      # Tunnel configuration
      - GORDION_TUNNEL_ENABLED=true
      - GORDION_TUNNEL_TOKEN=$TOKEN
      - GORDION_TUNNEL_RELAY_ADDR=$RELAY_ADDR
      - GORDION_TUNNEL_HOSPITAL_CODE=$HOSPITAL_CODE
      - GORDION_TUNNEL_SUBDOMAIN=$SUBDOMAIN
    # Ensure outbound connectivity for tunnel
    networks:
      - default
    # No port mapping needed for tunneled access
    # ports: [] # Remove or comment out port mappings
EOF

# Generate Windows service configuration
cat > "$OUTPUT_DIR/tunnel-service-config.txt" << EOF
# Windows Service Configuration for $HOSPITAL_CODE
# Add these to your gordionedge Windows service configuration

Service Environment Variables:
GORDION_TUNNEL_ENABLED=true
GORDION_TUNNEL_TOKEN=$TOKEN
GORDION_TUNNEL_RELAY_ADDR=$RELAY_ADDR
GORDION_TUNNEL_HOSPITAL_CODE=$HOSPITAL_CODE
GORDION_TUNNEL_SUBDOMAIN=$SUBDOMAIN

Registry Path: HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Services\\GordionEdge\\Environment
(Add each variable as a REG_SZ value)
EOF

# Generate testing script
cat > "$OUTPUT_DIR/test-tunnel.sh" << EOF
#!/bin/bash

# Test script for $HOSPITAL_CODE tunnel

echo "🧪 Testing tunnel configuration for $HOSPITAL_CODE"
echo "================================================="

# Test 1: Check if gordionedge is running locally
echo "1. Checking local gordionedge health..."
if curl -s http://localhost:8083/health > /dev/null; then
    echo "✅ Local gordionedge is responding"
else
    echo "❌ Local gordionedge not responding on localhost:8083"
    echo "   Make sure gordionedge is running"
    exit 1
fi

# Test 2: Check tunnel connectivity
echo "2. Testing tunnel connectivity to relay..."
if timeout 10 nc -z relay.$RELAY_DOMAIN 443; then
    echo "✅ Can reach relay server"
else
    echo "❌ Cannot reach relay server at relay.$RELAY_DOMAIN:443"
    echo "   Check network connectivity and firewall"
    exit 1
fi

# Test 3: Test public access (if tunnel is working)
echo "3. Testing public access via tunnel..."
if curl -s https://$SUBDOMAIN/health > /dev/null; then
    echo "✅ Public access working via tunnel"
    echo "🎉 Tunnel is fully operational!"
else
    echo "⚠️  Public access not working yet"
    echo "   This may be normal if tunnel is still connecting"
fi

echo
echo "📋 Tunnel Status URLs:"
echo "  • Local health: http://localhost:8083/health"
echo "  • Public health: https://$SUBDOMAIN/health"
echo "  • Example file: https://$SUBDOMAIN/api/instances/test/download"
EOF

chmod +x "$OUTPUT_DIR/test-tunnel.sh"

# Generate README
cat > "$OUTPUT_DIR/README.md" << EOF
# Tunnel Configuration for $HOSPITAL_CODE

This directory contains all configuration files needed to connect $HOSPITAL_CODE to the Gordion Relay.

## Files

- \`tunnel-config.json\` - JSON configuration for gordionedge
- \`tunnel.env\` - Environment variables for Linux/Docker
- \`docker-compose.tunnel.yml\` - Docker Compose override
- \`tunnel-service-config.txt\` - Windows service configuration
- \`test-tunnel.sh\` - Test script to verify tunnel

## Setup Instructions

### Option 1: Environment Variables (Recommended)
\`\`\`bash
source tunnel.env
./gordionedge
\`\`\`

### Option 2: Configuration File
Merge \`tunnel-config.json\` into your existing gordionedge config.

### Option 3: Docker Compose
\`\`\`bash
docker-compose -f docker-compose.yml -f docker-compose.tunnel.yml up
\`\`\`

## Security Notice

🔒 **IMPORTANT**: This directory contains sensitive authentication tokens.

- Do not commit these files to version control
- Restrict file permissions: \`chmod 600 *.env *.json\`
- Store securely and distribute only to authorized personnel
- Rotate tokens every 90 days

## Testing

Run the test script to verify tunnel connectivity:
\`\`\`bash
./test-tunnel.sh
\`\`\`

## Access URLs

Once configured, your hospital will be accessible at:
- **Health check**: https://$SUBDOMAIN/health
- **DICOM downloads**: https://$SUBDOMAIN/api/instances/{id}/download
- **Weasis manifests**: https://$SUBDOMAIN/api/studies/{id}/weasis.xml

## Support

For issues:
1. Check local gordionedge logs
2. Verify network connectivity to relay.$RELAY_DOMAIN:443
3. Ensure firewall allows outbound HTTPS (port 443)
4. Contact system administrator with relay logs if needed
EOF

# Set secure permissions
chmod 600 "$OUTPUT_DIR"/*.env "$OUTPUT_DIR"/*.json "$OUTPUT_DIR"/*.txt

echo
echo -e "${GREEN}✅ Hospital configuration generated successfully!${NC}"
echo
echo -e "${BLUE}📁 Output directory: $OUTPUT_DIR${NC}"
echo -e "${BLUE}📄 Files created:${NC}"
ls -la "$OUTPUT_DIR"
echo
echo -e "${YELLOW}🔒 Security Notice:${NC}"
echo -e "  • Files contain sensitive authentication tokens"
echo -e "  • Permissions set to 600 (owner read/write only)"
echo -e "  • Do not commit to version control"
echo -e "  • Distribute securely to hospital IT team"
echo
echo -e "${BLUE}📋 Next steps for $HOSPITAL_CODE:${NC}"
echo -e "  1. Copy configuration files to hospital server"
echo -e "  2. Configure gordionedge with tunnel settings"
echo -e "  3. Run test script to verify connectivity"
echo -e "  4. Verify public access at https://$SUBDOMAIN/health"
echo