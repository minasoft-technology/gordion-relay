#!/bin/bash

# deploy.sh - Deploy Gordion Relay to Kubernetes
# Usage: ./deploy.sh [--build] [--no-wait]

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
NAMESPACE="gordion-relay"
IMAGE_NAME="gordion-relay"
IMAGE_TAG="${IMAGE_TAG:-latest}"
REGISTRY="${REGISTRY:-}"

# Parse arguments
BUILD_IMAGE=false
WAIT_FOR_READY=true

while [[ $# -gt 0 ]]; do
    case $1 in
        --build)
            BUILD_IMAGE=true
            shift
            ;;
        --no-wait)
            WAIT_FOR_READY=false
            shift
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

echo -e "${BLUE}🚀 Deploying Gordion Relay to Kubernetes${NC}"
echo "=========================================="

# Check prerequisites
echo -e "${BLUE}📋 Checking prerequisites...${NC}"

# Check kubectl
if ! command -v kubectl &> /dev/null; then
    echo -e "${RED}❌ kubectl not found. Please install kubectl.${NC}"
    exit 1
fi

# Check cluster connection
if ! kubectl cluster-info &> /dev/null; then
    echo -e "${RED}❌ Cannot connect to Kubernetes cluster.${NC}"
    exit 1
fi

echo -e "${GREEN}✓${NC} kubectl available"
echo -e "${GREEN}✓${NC} Connected to cluster: $(kubectl config current-context)"

# Build image if requested
if [ "$BUILD_IMAGE" = true ]; then
    echo -e "${BLUE}🔨 Building Docker image...${NC}"

    if [ -n "$REGISTRY" ]; then
        FULL_IMAGE="$REGISTRY/$IMAGE_NAME:$IMAGE_TAG"
    else
        FULL_IMAGE="$IMAGE_NAME:$IMAGE_TAG"
    fi

    docker build -t "$FULL_IMAGE" .

    if [ -n "$REGISTRY" ]; then
        echo -e "${BLUE}📤 Pushing image to registry...${NC}"
        docker push "$FULL_IMAGE"
    fi

    echo -e "${GREEN}✅ Image built: $FULL_IMAGE${NC}"
fi

# Check if tokens are generated
if [ ! -f "./generated-tokens/k8s-secret-patch.yaml" ]; then
    echo -e "${YELLOW}⚠️  Hospital tokens not found.${NC}"
    echo -e "${BLUE}Generating tokens automatically...${NC}"
    ./scripts/generate-tokens.sh
fi

# Apply Kubernetes manifests
echo -e "${BLUE}📦 Applying Kubernetes manifests...${NC}"

# Apply in order
kubectl apply -f k8s/namespace.yaml
echo -e "${GREEN}✓${NC} Namespace created"

kubectl apply -f k8s/configmap.yaml
echo -e "${GREEN}✓${NC} ConfigMap applied"

# Apply generated secret
kubectl apply -f ./generated-tokens/k8s-secret-patch.yaml
echo -e "${GREEN}✓${NC} Secret applied"

kubectl apply -f k8s/deployment.yaml
echo -e "${GREEN}✓${NC} Deployment applied"

kubectl apply -f k8s/service.yaml
echo -e "${GREEN}✓${NC} Service applied"

kubectl apply -f k8s/networkpolicy.yaml
echo -e "${GREEN}✓${NC} NetworkPolicy applied"

kubectl apply -f k8s/hpa.yaml
echo -e "${GREEN}✓${NC} HorizontalPodAutoscaler applied"

kubectl apply -f k8s/poddisruptionbudget.yaml
echo -e "${GREEN}✓${NC} PodDisruptionBudget applied"

# Apply ServiceMonitor if prometheus-operator is available
if kubectl get crd servicemonitors.monitoring.coreos.com &> /dev/null; then
    kubectl apply -f k8s/servicemonitor.yaml
    echo -e "${GREEN}✓${NC} ServiceMonitor applied"
else
    echo -e "${YELLOW}⚠️  Prometheus operator not found, skipping ServiceMonitor${NC}"
fi

# Wait for deployment if requested
if [ "$WAIT_FOR_READY" = true ]; then
    echo -e "${BLUE}⏳ Waiting for deployment to be ready...${NC}"
    kubectl rollout status deployment/gordion-relay -n "$NAMESPACE" --timeout=300s
    echo -e "${GREEN}✅ Deployment ready!${NC}"

    # Get service information
    echo -e "${BLUE}📋 Service Information:${NC}"
    kubectl get service gordion-relay-service -n "$NAMESPACE" -o wide

    # Get external IP if available
    EXTERNAL_IP=$(kubectl get service gordion-relay-service -n "$NAMESPACE" -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")
    if [ -n "$EXTERNAL_IP" ]; then
        echo -e "${GREEN}🌍 External IP: $EXTERNAL_IP${NC}"
        echo -e "${BLUE}📝 Update your DNS:${NC}"
        echo "  *.zenpacs.com.tr     A    $EXTERNAL_IP"
        echo "  relay.zenpacs.com.tr A    $EXTERNAL_IP"
    else
        echo -e "${YELLOW}⏳ LoadBalancer IP pending...${NC}"
        echo "Run 'kubectl get svc -n $NAMESPACE' to check status"
    fi
fi

echo
echo -e "${GREEN}🎉 Gordion Relay deployment complete!${NC}"
echo
echo -e "${BLUE}Next steps:${NC}"
echo -e "  1. Update DNS records to point to LoadBalancer IP"
echo -e "  2. Distribute hospital tokens securely"
echo -e "  3. Configure hospital edge servers with tunnel settings"
echo
echo -e "${BLUE}Monitoring:${NC}"
echo -e "  • Status: ${GREEN}kubectl get pods -n $NAMESPACE${NC}"
echo -e "  • Logs: ${GREEN}kubectl logs -f deployment/gordion-relay -n $NAMESPACE${NC}"
echo -e "  • Metrics: ${GREEN}kubectl port-forward svc/gordion-relay-service 8080:8080 -n $NAMESPACE${NC}"