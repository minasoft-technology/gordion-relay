#!/bin/bash

# status.sh - Check Gordion Relay deployment status
# Usage: ./status.sh

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

NAMESPACE="gordion-relay"

echo -e "${BLUE}📊 Gordion Relay Status Dashboard${NC}"
echo "=================================="

# Check if namespace exists
if ! kubectl get namespace "$NAMESPACE" &> /dev/null; then
    echo -e "${RED}❌ Namespace '$NAMESPACE' not found. Run ./deploy.sh first.${NC}"
    exit 1
fi

echo -e "${GREEN}✓${NC} Namespace: $NAMESPACE"
echo

# Deployment status
echo -e "${BLUE}🚀 Deployment Status:${NC}"
kubectl get deployment gordion-relay -n "$NAMESPACE" -o wide

echo
echo -e "${BLUE}📦 Pod Status:${NC}"
kubectl get pods -n "$NAMESPACE" -o wide

echo
echo -e "${BLUE}🌐 Service Status:${NC}"
kubectl get services -n "$NAMESPACE" -o wide

# Check LoadBalancer IP
EXTERNAL_IP=$(kubectl get service gordion-relay-service -n "$NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
if [ -n "$EXTERNAL_IP" ]; then
    echo -e "${GREEN}🌍 External IP: $EXTERNAL_IP${NC}"
else
    echo -e "${YELLOW}⏳ LoadBalancer IP pending...${NC}"
fi

# Check endpoint health
echo
echo -e "${BLUE}🏥 Health Check:${NC}"
if kubectl get pods -n "$NAMESPACE" -l app=gordion-relay --field-selector=status.phase=Running --no-headers | grep -q gordion-relay; then
    # Try to port-forward and check health
    POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l app=gordion-relay --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.name}')

    echo -e "Testing health endpoint via pod $POD_NAME..."

    # Port forward in background and test
    kubectl port-forward pod/"$POD_NAME" 8080:8080 -n "$NAMESPACE" &> /dev/null &
    PF_PID=$!

    # Wait a moment for port-forward to establish
    sleep 2

    # Test health endpoint
    if curl -s http://localhost:8080/health &> /dev/null; then
        echo -e "${GREEN}✅ Health endpoint responding${NC}"

        # Get status if available
        if STATUS=$(curl -s http://localhost:8080/status 2>/dev/null); then
            HOSPITAL_COUNT=$(echo "$STATUS" | jq -r '.connected_hospitals // 0' 2>/dev/null || echo "0")
            echo -e "${GREEN}🏥 Connected hospitals: $HOSPITAL_COUNT${NC}"

            if [ "$HOSPITAL_COUNT" -gt 0 ]; then
                echo -e "${BLUE}Hospital details:${NC}"
                echo "$STATUS" | jq -r '.hospitals[]? | "  • " + .code + " (" + .subdomain + ") - " + .last_seen' 2>/dev/null || echo "  Could not parse hospital details"
            fi
        fi
    else
        echo -e "${RED}❌ Health endpoint not responding${NC}"
    fi

    # Clean up port-forward
    kill $PF_PID 2>/dev/null || true
else
    echo -e "${RED}❌ No running pods found${NC}"
fi

# Recent events
echo
echo -e "${BLUE}📋 Recent Events:${NC}"
kubectl get events -n "$NAMESPACE" --sort-by='.lastTimestamp' | tail -10

# Pod logs (last 10 lines)
echo
echo -e "${BLUE}📝 Recent Logs:${NC}"
if kubectl get pods -n "$NAMESPACE" -l app=gordion-relay --field-selector=status.phase=Running --no-headers | grep -q gordion-relay; then
    kubectl logs --tail=10 -l app=gordion-relay -n "$NAMESPACE"
else
    echo -e "${YELLOW}No running pods to show logs${NC}"
fi

echo
echo -e "${BLUE}🔧 Useful Commands:${NC}"
echo -e "  • View logs: ${GREEN}kubectl logs -f deployment/gordion-relay -n $NAMESPACE${NC}"
echo -e "  • Port forward metrics: ${GREEN}kubectl port-forward svc/gordion-relay-service 8080:8080 -n $NAMESPACE${NC}"
echo -e "  • Scale deployment: ${GREEN}kubectl scale deployment gordion-relay --replicas=2 -n $NAMESPACE${NC}"
echo -e "  • Update image: ${GREEN}kubectl set image deployment/gordion-relay relay=gordion-relay:new-tag -n $NAMESPACE${NC}"
echo -e "  • Delete deployment: ${GREEN}kubectl delete namespace $NAMESPACE${NC}"