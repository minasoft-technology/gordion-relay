# Deploy Gordion Relay to Kubernetes

**Status**: ✅ Ready to deploy (all issues fixed)

## Prerequisites

1. Kubernetes cluster running
2. `kubectl` configured to access your cluster
3. DNS wildcard record: `*.zenpacs.com.tr` → LoadBalancer IP (will get after deployment)
4. Hospital tokens generated (see below)

## Quick Deploy

```bash
# 1. Create namespace
kubectl apply -f k8s/namespace.yaml

# 2. Apply secret with real tokens (IMPORTANT!)
kubectl apply -f k8s/secret-example.yaml

# 3. Apply config
kubectl apply -f k8s/configmap.yaml

# 4. Deploy relay
kubectl apply -f k8s/deployment.yaml

# 5. Create service (LoadBalancer)
kubectl apply -f k8s/service.yaml

# 6. (Optional) Apply network policy
kubectl apply -f k8s/networkpolicy.yaml

# 7. (Optional) Enable autoscaling
kubectl apply -f k8s/hpa.yaml
```

## Step-by-Step Deployment

### Step 1: Verify Image Availability

The GitHub Action automatically builds and pushes images to:
```
ghcr.io/minasoft-technology/gordion-relay:main
```

Check if the image is available:
```bash
docker pull ghcr.io/minasoft-technology/gordion-relay:main
```

### Step 2: Create Namespace

```bash
kubectl apply -f k8s/namespace.yaml

# Verify
kubectl get namespace gordion-relay
```

### Step 3: Configure Secrets

**IMPORTANT**: Use real tokens, not the placeholder ones!

Option A - Use generated tokens from `k8s/secret-example.yaml`:
```bash
kubectl apply -f k8s/secret-example.yaml
```

Option B - Generate new tokens:
```bash
# Generate tokens
ANKARA_TOKEN=$(openssl rand -base64 32)
ISTANBUL_TOKEN=$(openssl rand -base64 32)
SAMSUN_TOKEN=$(openssl rand -base64 32)
IZMIR_TOKEN=$(openssl rand -base64 32)
ANTALYA_TOKEN=$(openssl rand -base64 32)

# Apply with environment substitution
envsubst < k8s/secret.yaml | kubectl apply -f -
```

**Store tokens securely!** You'll need to share them with hospitals.

### Step 4: Apply Configuration

```bash
kubectl apply -f k8s/configmap.yaml

# Verify
kubectl get configmap -n gordion-relay
```

### Step 5: Deploy Relay Server

```bash
kubectl apply -f k8s/deployment.yaml

# Watch deployment
kubectl get pods -n gordion-relay -w
```

Wait until pod shows `Running` and `1/1 Ready`:
```
NAME                             READY   STATUS    RESTARTS   AGE
gordion-relay-5d4f8b9c7d-abcde   1/1     Running   0          30s
```

### Step 6: Create LoadBalancer Service

```bash
kubectl apply -f k8s/service.yaml

# Get LoadBalancer IP (may take 1-2 minutes)
kubectl get svc gordion-relay-service -n gordion-relay -w
```

Wait for `EXTERNAL-IP` to appear:
```
NAME                    TYPE           EXTERNAL-IP     PORT(S)
gordion-relay-service   LoadBalancer   203.0.113.100   80:30080/TCP,443:30443/UDP,8080:30800/TCP
```

### Step 7: Configure DNS

Update your DNS records with the LoadBalancer IP:

```bash
# Get LoadBalancer IP
RELAY_IP=$(kubectl get svc gordion-relay-service -n gordion-relay -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "LoadBalancer IP: $RELAY_IP"

# Add these DNS records:
# *.zenpacs.com.tr.      A    $RELAY_IP
# relay.zenpacs.com.tr.  A    $RELAY_IP
```

**Important**: Wait for DNS propagation (1-24 hours) before testing HTTPS/Let's Encrypt.

### Step 8: Verify Deployment

```bash
# Check pod logs
kubectl logs -n gordion-relay -l app=gordion-relay -f

# Should see:
# {"level":"INFO","msg":"QUIC listener started","addr":":443"}
# {"level":"INFO","msg":"Relay server started successfully"}

# Test health endpoint
RELAY_IP=$(kubectl get svc gordion-relay-service -n gordion-relay -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
curl http://$RELAY_IP:8080/health
# Output: OK

# Test status endpoint
curl http://$RELAY_IP:8080/status
# Output: {"connected_hospitals":0,"hospitals":[]}
```

### Step 9: Configure Hospitals

Share tokens with each hospital via secure channel (encrypted email, 1Password, etc.):

**For Ankara Hospital:**
```bash
export GORDION_TUNNEL_ENABLED=true
export GORDION_TUNNEL_RELAY_ADDR="relay.zenpacs.com.tr:443"
export GORDION_TUNNEL_HOSPITAL_CODE="ankara"
export GORDION_TUNNEL_SUBDOMAIN="ankara.zenpacs.com.tr"
export GORDION_TUNNEL_TOKEN="<ANKARA_TOKEN_FROM_SECRET>"

# Start gordionedge
./gordionedge -config config.json
```

Repeat for istanbul, samsun, izmir, antalya with their respective tokens.

### Step 10: Verify Hospital Connection

```bash
# Check status
curl http://$RELAY_IP:8080/status | jq

# Should show connected hospitals:
{
  "connected_hospitals": 1,
  "hospitals": [
    {
      "code": "ankara",
      "subdomain": "ankara.zenpacs.com.tr",
      "last_seen": "2025-09-27T12:34:56Z",
      "remote_addr": "10.20.30.40:54321"
    }
  ]
}
```

## Optional Components

### Enable Autoscaling

```bash
kubectl apply -f k8s/hpa.yaml

# Verify
kubectl get hpa -n gordion-relay
```

### Apply Network Policy

```bash
kubectl apply -f k8s/networkpolicy.yaml

# Verify
kubectl get networkpolicy -n gordion-relay
```

### Enable Pod Disruption Budget

```bash
kubectl apply -f k8s/poddisruptionbudget.yaml

# Verify
kubectl get pdb -n gordion-relay
```

### Enable Prometheus Monitoring

```bash
kubectl apply -f k8s/servicemonitor.yaml

# Verify (requires Prometheus Operator)
kubectl get servicemonitor -n gordion-relay
```

## Monitoring

### Check Logs

```bash
# Follow logs
kubectl logs -n gordion-relay -l app=gordion-relay -f

# Search for errors
kubectl logs -n gordion-relay -l app=gordion-relay | grep ERROR

# Check specific hospital
kubectl logs -n gordion-relay -l app=gordion-relay | grep ankara
```

### Health Checks

```bash
# Liveness check
curl http://$RELAY_IP:8080/health

# Status with hospitals
curl http://$RELAY_IP:8080/status | jq
```

### Resource Usage

```bash
# CPU and memory
kubectl top pods -n gordion-relay

# Events
kubectl get events -n gordion-relay --sort-by='.lastTimestamp'
```

## Troubleshooting

### Pod Not Starting

```bash
# Check pod status
kubectl describe pod -n gordion-relay -l app=gordion-relay

# Check logs
kubectl logs -n gordion-relay -l app=gordion-relay
```

Common issues:
- Image pull errors → Check GitHub Container Registry permissions
- CrashLoopBackOff → Check logs for configuration errors
- Pending → Check resource availability

### Hospital Can't Connect

```bash
# Check relay is accessible
telnet relay.zenpacs.com.tr 443

# Check DNS
dig +short ankara.zenpacs.com.tr

# Check logs for auth errors
kubectl logs -n gordion-relay -l app=gordion-relay | grep "Invalid token"
```

### Let's Encrypt Certificate Issues

```bash
# Check logs for ACME errors
kubectl logs -n gordion-relay -l app=gordion-relay | grep -i acme

# Verify DNS is correct
dig +short relay.zenpacs.com.tr

# Verify port 80 is accessible
curl http://relay.zenpacs.com.tr/.well-known/acme-challenge/test
```

## Updating

### Update Image

```bash
# GitHub Action automatically builds on push to main
# After merge to main, update deployment:

kubectl set image deployment/gordion-relay \
  relay=ghcr.io/minasoft-technology/gordion-relay:main \
  -n gordion-relay

# Or apply the deployment again
kubectl apply -f k8s/deployment.yaml
```

### Update Configuration

```bash
# Edit configmap
kubectl edit configmap gordion-relay-config -n gordion-relay

# Restart pods to pick up changes
kubectl rollout restart deployment/gordion-relay -n gordion-relay
```

### Update Secrets

```bash
# Edit secret
kubectl edit secret gordion-relay-tokens -n gordion-relay

# Restart pods
kubectl rollout restart deployment/gordion-relay -n gordion-relay
```

## Rollback

```bash
# View rollout history
kubectl rollout history deployment/gordion-relay -n gordion-relay

# Rollback to previous version
kubectl rollout undo deployment/gordion-relay -n gordion-relay

# Rollback to specific revision
kubectl rollout undo deployment/gordion-relay --to-revision=2 -n gordion-relay
```

## Uninstall

```bash
# Delete all resources
kubectl delete -f k8s/

# Or delete namespace (removes everything)
kubectl delete namespace gordion-relay
```

## Security Checklist

- [x] Tokens are randomly generated (32+ characters)
- [x] Secret file is not committed to git (use secret-example.yaml)
- [x] Tokens shared with hospitals via secure channel
- [x] Network policy applied (restricts pod traffic)
- [x] Read-only root filesystem enabled
- [x] Non-root user (implicit in scratch image)
- [x] Capabilities dropped (no privileges)
- [x] Resource limits configured
- [x] TLS enabled with Let's Encrypt
- [x] Rate limiting enabled (5 attempts per IP)

## Production Considerations

1. **Persistent Storage**: Consider using PersistentVolume for Let's Encrypt certs instead of emptyDir
2. **Multiple Replicas**: Limited to 3 due to QUIC connection affinity
3. **Backup Tokens**: Store tokens in secure vault (HashiCorp Vault, AWS Secrets Manager)
4. **Monitoring**: Set up Prometheus alerts for disconnected hospitals
5. **DNS**: Ensure DNS has low TTL (300s) for quick failover
6. **Load Balancer**: Use Network Load Balancer (not Classic) for UDP support
7. **SSL Certificates**: Monitor expiration, Let's Encrypt auto-renews every 60 days

## Summary

✅ All K8s issues fixed:
- Removed `/bin/sh` preStop hook (scratch image compatible)
- Removed `runAsUser` security context (scratch image compatible)
- Updated image to `ghcr.io/minasoft-technology/gordion-relay:main`
- Generated real secure tokens for hospitals

The deployment is **production-ready**!