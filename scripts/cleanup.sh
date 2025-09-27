#!/bin/bash

# cleanup.sh - Clean up Gordion Relay deployment
# Usage: ./cleanup.sh [--force]

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

NAMESPACE="gordion-relay"
FORCE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --force)
            FORCE=true
            shift
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

echo -e "${BLUE}🧹 Cleaning up Gordion Relay deployment${NC}"
echo "======================================="

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
    echo -e "${YELLOW}⚠️  Namespace '$NAMESPACE' not found. Nothing to clean up.${NC}"
    exit 0
fi

# Confirmation if not forced
if [ "$FORCE" != true ]; then
    echo -e "${YELLOW}⚠️  This will delete ALL resources in namespace '$NAMESPACE'${NC}"
    echo -e "${RED}This action cannot be undone!${NC}"
    echo
    read -p "Are you sure you want to continue? (y/N): " -n 1 -r
    echo
    if [[ ! $RESPONSE =~ ^[Yy]$ ]]; then
        echo -e "${BLUE}Cleanup cancelled.${NC}"
        exit 0
    fi
fi

echo -e "${BLUE}🗑️  Deleting Kubernetes resources...${NC}"

# Delete namespace (this will delete all resources within it)
kubectl delete namespace "$NAMESPACE" --timeout=60s

echo -e "${GREEN}✅ Namespace '$NAMESPACE' deleted${NC}"

# Optional: Clean up generated tokens
if [ -d "./generated-tokens" ]; then
    echo -e "${BLUE}🔐 Found generated tokens directory${NC}"
    read -p "Delete generated tokens? (y/N): " -n 1 -r
    echo
    if [[ $RESPONSE =~ ^[Yy]$ ]]; then
        rm -rf "./generated-tokens"
        echo -e "${GREEN}✅ Generated tokens deleted${NC}"
    else
        echo -e "${YELLOW}⚠️  Generated tokens preserved${NC}"
    fi
fi

echo
echo -e "${GREEN}🎉 Cleanup complete!${NC}"
echo
echo -e "${BLUE}Cleanup summary:${NC}"
echo -e "  • Namespace '$NAMESPACE' deleted"
echo -e "  • All deployments, services, and secrets removed"
echo -e "  • LoadBalancer resources cleaned up"
echo
echo -e "${YELLOW}Note: LoadBalancer IP may take a few minutes to be released.${NC}"