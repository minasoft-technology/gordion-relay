#!/bin/bash

# generate-tokens.sh - Generate secure tokens for hospitals
# Usage: ./generate-tokens.sh [hospital1] [hospital2] ...

set -euo pipefail

echo "🔐 Generating secure tokens for hospital relay authentication"
echo "========================================================"

# Default hospitals if none provided
HOSPITALS=("ankara" "istanbul" "samsun" "izmir" "antalya")

# Use provided hospitals or defaults
if [ $# -gt 0 ]; then
    HOSPITALS=("$@")
fi

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}Generating tokens for hospitals: ${HOSPITALS[*]}${NC}"
echo

# Generate tokens and create files
TOKENS_DIR="./generated-tokens"
mkdir -p "$TOKENS_DIR"

# Create token file
TOKEN_FILE="$TOKENS_DIR/hospital-tokens.env"
SECRET_FILE="$TOKENS_DIR/k8s-secret-patch.yaml"

echo "# Hospital Tokens - Generated $(date)" > "$TOKEN_FILE"
echo "# Source this file to set environment variables" >> "$TOKEN_FILE"
echo >> "$TOKEN_FILE"

echo "# Kubernetes Secret Patch - Generated $(date)" > "$SECRET_FILE"
echo "apiVersion: v1" >> "$SECRET_FILE"
echo "kind: Secret" >> "$SECRET_FILE"
echo "metadata:" >> "$SECRET_FILE"
echo "  name: gordion-relay-tokens" >> "$SECRET_FILE"
echo "  namespace: gordion-relay" >> "$SECRET_FILE"
echo "type: Opaque" >> "$SECRET_FILE"
echo "stringData:" >> "$SECRET_FILE"

# Hospital JSON for config
HOSPITALS_JSON="$TOKENS_DIR/hospitals.json"
echo "[" > "$HOSPITALS_JSON"

for i in "${!HOSPITALS[@]}"; do
    HOSPITAL="${HOSPITALS[$i]}"

    # Generate a secure 32-byte token
    TOKEN=$(openssl rand -base64 32)

    echo -e "${GREEN}✓${NC} Generated token for ${YELLOW}$HOSPITAL${NC}"

    # Add to environment file
    echo "export ${HOSPITAL^^}_TOKEN=\"$TOKEN\"" >> "$TOKEN_FILE"

    # Add to Kubernetes secret
    echo "  ${HOSPITAL}-token: \"$TOKEN\"" >> "$SECRET_FILE"

    # Add to hospitals JSON
    if [ $i -gt 0 ]; then
        echo "," >> "$HOSPITALS_JSON"
    fi
    cat >> "$HOSPITALS_JSON" << EOF
  {
    "code": "$HOSPITAL",
    "subdomain": "$HOSPITAL.zenpacs.com.tr",
    "token": "$TOKEN"
  }
EOF
done

echo "]" >> "$HOSPITALS_JSON"

# Add hospitals.json to secret
echo "  hospitals.json: |" >> "$SECRET_FILE"
sed 's/^/    /' "$HOSPITALS_JSON" >> "$SECRET_FILE"

echo
echo -e "${GREEN}✅ Token generation complete!${NC}"
echo
echo -e "${BLUE}Generated files:${NC}"
echo -e "  📄 ${TOKEN_FILE} - Environment variables"
echo -e "  📄 ${SECRET_FILE} - Kubernetes secret patch"
echo -e "  📄 ${HOSPITALS_JSON} - Hospital configuration JSON"
echo
echo -e "${YELLOW}⚠️  Security Notice:${NC}"
echo -e "  • Store these tokens securely"
echo -e "  • Do not commit to version control"
echo -e "  • Distribute tokens to hospitals securely"
echo -e "  • Rotate tokens every 90 days"
echo
echo -e "${BLUE}Next steps:${NC}"
echo -e "  1. Apply tokens: ${GREEN}kubectl apply -f ${SECRET_FILE}${NC}"
echo -e "  2. Deploy relay: ${GREEN}./deploy.sh${NC}"
echo -e "  3. Distribute tokens to hospitals"
echo

# Make files readable only by owner
chmod 600 "$TOKENS_DIR"/*
echo -e "${GREEN}🔒 File permissions set to 600 (owner read/write only)${NC}"