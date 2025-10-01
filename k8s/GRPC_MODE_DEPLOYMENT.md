# Deploying Gordion Relay in gRPC Mode

## Overview

This guide covers deploying Gordion Relay with **gRPC bidirectional streaming** instead of WebSocket mode.

**Benefits of gRPC Mode:**
- ✅ Better firewall compatibility (standard HTTPS/HTTP2)
- ✅ Efficient binary protocol (Protobuf)
- ✅ Built-in flow control and backpressure
- ✅ Lower latency (100ms vs 150ms)
- ✅ Native HTTP/2 multiplexing

---

## Quick Deploy (gRPC Mode)

```bash
# 1. Create namespace
kubectl apply -f k8s/namespace.yaml

# 2. Create PVC for certs
kubectl apply -f k8s/pvc-certs.yaml

# 3. Create secrets with hospital tokens
kubectl apply -f k8s/secret-example.yaml

# 4. Apply gRPC-specific configs
kubectl apply -f k8s/configmap-grpc.yaml
kubectl apply -f k8s/service-grpc.yaml
kubectl apply -f k8s/httpproxy-grpc.yaml

# 5. Deploy relay (same deployment for both modes)
kubectl apply -f k8s/deployment.yaml

# Done! Verify:
kubectl get pods -n gordion-relay
kubectl logs -n gordion-relay -l app=gordion-relay --tail=50
```

---

## Key Configuration Changes

### 1. ConfigMap - Enable gRPC Mode

**File:** `k8s/configmap-grpc.yaml`

```json
{
  "mode": "grpc",  ← CRITICAL: Must be set to "grpc"
  "listen_addr": ":8080",
  "domain": "zenpacs.com.tr",
  "request_timeout": "5m",
  ...
}
```

### 2. HTTPProxy - HTTP/2 Support

**File:** `k8s/httpproxy-grpc.yaml`

```yaml
spec:
  routes:
  - services:
    - name: gordion-relay-service
      port: 8080
      protocol: h2c  ← HTTP/2 cleartext (gRPC requires HTTP/2)
    timeoutPolicy:
      response: 5m   ← Increased for large files
      idle: 15m      ← Increased for long streams
```

### 3. Service - gRPC Protocol

**File:** `k8s/service-grpc.yaml`

```yaml
metadata:
  annotations:
    service.beta.kubernetes.io/backend-protocol: "h2c"
ports:
- name: grpc      ← Named "grpc" instead of "http"
  appProtocol: grpc  ← Kubernetes 1.20+
```

---

## Verification Steps

### 1. Check Relay Logs

```bash
kubectl logs -n gordion-relay -l app=gordion-relay --tail=50
```

**Expected output:**
```
Starting Gordion Relay Server
Starting gRPC relay server mode
gRPC server listening on :8080
HTTP server listening on :8080
Relay server started successfully mode=grpc
```

### 2. Check Edge Connection

From edge server, check logs:
```
Creating tunnel mode=grpc
Connecting to relay addr=relay.zenpacs.com.tr:443
✅ gRPC tunnel connected hospital_id=DEMO_HOSPITAL
✓ Tunnel started successfully mode=grpc connected=true
```

### 3. Test from Edge Web UI

```bash
# Open edge dashboard
http://localhost:8083

# Check System Status → Tunnel
# Should show: Connected (gRPC)
```

### 4. Monitor Metrics

```bash
# Port-forward metrics
kubectl port-forward -n gordion-relay svc/gordion-relay-metrics 9090:9090

# Check status
curl http://localhost:9090/status
```

---

## Production Deployment (Full Stack)

For production with cert-manager, external-dns, and monitoring:

```bash
# 1. Update relay-stack.yaml with gRPC changes
# Edit k8s/production/relay-stack.yaml:
#   - Update ConfigMap (line ~189): Add "mode": "grpc"
#   - Update HTTPProxy (line ~446): Add protocol: h2c, increase timeouts
#   - Update Service (line ~407): Add grpc annotations

# 2. Apply complete stack
kubectl apply -f k8s/production/relay-stack.yaml

# 3. Wait for cert generation
kubectl get certificate -n gordion-relay
# Should show: gordion-relay-wildcard-cert   True   Ready

# 4. Verify DNS
nslookup demo.zenpacs.com.tr
# Should resolve to LoadBalancer IP

# 5. Test from edge
# Update edge config.json:
{
  "tunnel": {
    "enabled": true,
    "mode": "grpc",
    "grpc_relay_addr": "relay.zenpacs.com.tr:443",
    "use_ssl": true,
    "token": "your-hospital-token"
  }
}
```

---

## Troubleshooting

### Issue: Connection Fails with "HTTP2 not supported"

**Cause:** Ingress controller doesn't support HTTP/2 to backend

**Fix:** Ensure you're using Contour (not Nginx Ingress):
```bash
kubectl get pods -n projectcontour
# Should show contour pods running
```

If using Nginx Ingress, add to Deployment:
```yaml
env:
- name: NGINX_GRPC_PROXY_MODE
  value: "grpc"
```

### Issue: "connection timeout" or "stream closed"

**Cause:** Timeouts too short for large transfers

**Fix:** Increase timeouts in HTTPProxy:
```yaml
timeoutPolicy:
  response: 10m    # Increase from 5m
  idle: 30m        # Increase from 15m
```

### Issue: Edge shows "registration failed: invalid token"

**Cause:** Token mismatch between edge and relay

**Fix:** Verify tokens match:
```bash
# Check relay tokens
kubectl get secret -n gordion-relay gordion-relay-tokens -o jsonpath='{.data.ankara-token}' | base64 -d

# Check edge config
cat /c/ProgramData/GordionEdge/configs/config.json | grep token
```

### Issue: "TLS handshake failed"

**Cause:** Certificate not trusted or expired

**Fix:** Check certificate:
```bash
kubectl get certificate -n gordion-relay
kubectl describe certificate -n gordion-relay gordion-relay-wildcard-cert
```

---

## Monitoring gRPC Connections

### Check Active Connections

```bash
# Get relay pod
POD=$(kubectl get pod -n gordion-relay -l app=gordion-relay -o name | head -1)

# Check logs for connections
kubectl logs -n gordion-relay $POD | grep "gRPC tunnel connected"
```

**Output:**
```
gRPC tunnel connected hospital_id=ANKARA edge_server_id=EDGE-01
gRPC tunnel connected hospital_id=ISTANBUL edge_server_id=EDGE-02
```

### Monitor Stream Activity

```bash
# Watch for fetch commands
kubectl logs -n gordion-relay $POD -f | grep "FetchCommand"

# Watch for data transfers
kubectl logs -n gordion-relay $POD -f | grep "DataResponse"
```

---

## Performance Tuning

### For High-Volume Hospitals (>100 studies/day)

**Increase resources:**
```yaml
# k8s/deployment.yaml
resources:
  requests:
    cpu: 500m      # Increase from 200m
    memory: 512Mi  # Increase from 256Mi
  limits:
    cpu: 4000m     # Increase from 2000m
    memory: 2Gi    # Increase from 1Gi
```

**Scale replicas:**
```bash
kubectl scale deployment -n gordion-relay gordion-relay --replicas=5
```

### For Large Studies (>1000 instances)

**Increase timeouts:**
```yaml
# HTTPProxy
timeoutPolicy:
  response: 30m   # Very large studies
  idle: 60m
```

**Update ConfigMap:**
```json
{
  "request_timeout": "30m",
  "idle_timeout": "60s"
}
```

---

## Rollback to WebSocket Mode

If gRPC mode has issues, rollback:

```bash
# 1. Revert ConfigMap
kubectl apply -f k8s/configmap.yaml  # Original WebSocket config

# 2. Revert HTTPProxy
kubectl apply -f k8s/production/relay-stack.yaml  # Use original

# 3. Restart pods
kubectl rollout restart deployment -n gordion-relay gordion-relay

# 4. Update edge servers to WebSocket mode
# Edit edge config.json:
{
  "tunnel": {
    "enabled": true,
    "mode": "websocket",
    "relay_addr": "relay.zenpacs.com.tr:443"
  }
}
```

---

## Migration Checklist

- [ ] Update relay ConfigMap with `"mode": "grpc"`
- [ ] Update HTTPProxy with `protocol: h2c` and increased timeouts
- [ ] Update Service with gRPC annotations
- [ ] Apply changes: `kubectl apply -f k8s/`
- [ ] Verify relay logs show "Starting gRPC relay server mode"
- [ ] Update edge server configs to use gRPC mode
- [ ] Test connection from one edge server
- [ ] Verify DICOM fetch works from viewer
- [ ] Monitor for 24 hours
- [ ] Roll out to remaining edge servers

---

## Support

**Logs location:**
```bash
# Relay logs
kubectl logs -n gordion-relay -l app=gordion-relay --tail=100 -f

# Edge logs
C:\ProgramData\GordionEdge\logs\service.log
```

**Status endpoints:**
```bash
# Relay status
curl https://relay.zenpacs.com.tr/status

# Edge status
http://localhost:8083/api/system/status
```

**Documentation:**
- gRPC Implementation: `internal/tunnel/grpc/README.md`
- Edge Client Code: `internal/tunnel/grpc/edge_client.go`
- Relay Server Code: `internal/relay/server_grpc.go`
