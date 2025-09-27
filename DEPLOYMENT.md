# 🚀 Gordion Relay - Production Deployment Guide

Complete deployment guide for the Gordion Relay WebSocket tunnel system.

## 📋 Pre-Deployment Checklist

### Infrastructure Requirements
- [ ] **Kubernetes cluster** (v1.20+) with LoadBalancer support
- [ ] **DNS control** for wildcard domain (*.zenpacs.com.tr)
- [ ] **Public IP address** for LoadBalancer
- [ ] **Docker registry** access (optional - for custom builds)
- [ ] **StorageClass** for PersistentVolume (for Let's Encrypt cert caching)

### Network Requirements
- [ ] **Port 80/TCP** - HTTP redirects and ACME challenges
- [ ] **Port 443/TCP** - HTTPS/WebSocket tunnel
- [ ] **Port 8080/TCP** - Metrics and health checks
- [ ] **Outbound HTTPS** - For Let's Encrypt certificate requests

## 🔧 Step-by-Step Deployment

### Step 1: Clone and Prepare
```bash
# Clone the repository
git clone https://github.com/minasoft-technology/gordion-relay
cd gordion-relay

# Make scripts executable (if not already)
chmod +x scripts/*.sh
```

### Step 2: Generate Hospital Tokens
```bash
# Generate secure tokens for all hospitals
./scripts/generate-tokens.sh ankara istanbul samsun izmir antalya

# This creates:
# - generated-tokens/hospital-tokens.env
# - generated-tokens/k8s-secret-patch.yaml
# - generated-tokens/hospitals.json
```

### Step 3: Build Docker Image (Optional)
```bash
# Build and push image (if using custom registry)
export REGISTRY="your-registry.com"
export IMAGE_TAG="v1.0.0"

docker build -t $REGISTRY/gordion-relay:$IMAGE_TAG .
docker push $REGISTRY/gordion-relay:$IMAGE_TAG

# Update k8s/deployment.yaml with your image
sed -i "s|gordion-relay:latest|$REGISTRY/gordion-relay:$IMAGE_TAG|" k8s/deployment.yaml
```

### Step 4: Deploy to Kubernetes
```bash
# Deploy everything with build
./scripts/deploy.sh --build

# Or deploy without building (using existing image)
./scripts/deploy.sh
```

### Step 5: Configure DNS
```bash
# Get LoadBalancer IP
kubectl get svc gordion-relay-service -n gordion-relay

# Add these DNS records:
# *.zenpacs.com.tr     A    YOUR_LOADBALANCER_IP
# relay.zenpacs.com.tr A    YOUR_LOADBALANCER_IP
```

### Step 6: Verify Deployment
```bash
# Check deployment status
./scripts/status.sh

# Test health endpoint
curl http://YOUR_LOADBALANCER_IP:8080/health
```

### Step 7: Generate Hospital Configurations
```bash
# Generate config for each hospital
./scripts/hospital-setup.sh ankara
./scripts/hospital-setup.sh istanbul
./scripts/hospital-setup.sh samsun

# Distribute hospital-configs/ folders to each hospital
```

## 🏥 Hospital Setup

Each hospital receives a configuration package in `hospital-configs/{hospital}/`:

### Files Included
- `tunnel-config.json` - JSON configuration for gordionedge
- `tunnel.env` - Environment variables
- `docker-compose.tunnel.yml` - Docker Compose override
- `test-tunnel.sh` - Connectivity test script
- `README.md` - Setup instructions

### Hospital Configuration Steps
1. **Copy configuration files** to hospital server
2. **Apply tunnel settings** to gordionedge
3. **Restart gordionedge** with tunnel enabled
4. **Run test script** to verify connectivity
5. **Verify public access** at https://{hospital}.zenpacs.com.tr/health

## 📊 Monitoring Setup

### Kubernetes Monitoring
```bash
# Pod logs
kubectl logs -f deployment/gordion-relay -n gordion-relay

# Metrics endpoint
kubectl port-forward svc/gordion-relay-service 8080:8080 -n gordion-relay

# Service status
kubectl get all -n gordion-relay
```

### Prometheus Integration
```bash
# If prometheus-operator is installed
kubectl get servicemonitor gordion-relay-metrics -n gordion-relay

# Manual metrics collection
curl http://localhost:8080/status | jq
```

### Health Checks
```bash
# Relay health
curl https://relay.zenpacs.com.tr:8080/health

# Hospital connectivity
curl https://relay.zenpacs.com.tr:8080/status

# Individual hospitals
curl https://ankara.zenpacs.com.tr/health
curl https://istanbul.zenpacs.com.tr/health
```

## 🔒 Security Configuration

### Token Management
```bash
# Rotate tokens (every 90 days)
./scripts/generate-tokens.sh ankara istanbul samsun
kubectl apply -f generated-tokens/k8s-secret-patch.yaml

# Restart deployment to pick up new tokens
kubectl rollout restart deployment/gordion-relay -n gordion-relay
```

### Network Security
```bash
# Verify NetworkPolicy
kubectl get networkpolicy -n gordion-relay

# Check firewall rules (cloud-specific)
# Ensure only ports 80, 443, 8080 are exposed
```

### Certificate Management
```bash
# Let's Encrypt certificates are auto-managed
# Check certificate status
kubectl exec -it deployment/gordion-relay -n gordion-relay -- ls -la /app/certs/
```

## 🚨 Troubleshooting

### Common Issues

#### 1. Hospital Can't Connect
```bash
# Check tokens match
kubectl get secret gordion-relay-tokens -n gordion-relay -o yaml

# Check relay logs
kubectl logs deployment/gordion-relay -n gordion-relay | grep "ankara"

# Test connectivity from hospital
telnet relay.zenpacs.com.tr 443
```

#### 2. LoadBalancer IP Pending
```bash
# Check LoadBalancer status
kubectl describe svc gordion-relay-service -n gordion-relay

# Cloud-specific troubleshooting needed
# AWS: Check ELB/NLB configuration
# GCP: Check firewall rules
# Azure: Check Load Balancer configuration
```

#### 3. Certificate Issues
```bash
# Check ACME challenges
kubectl logs deployment/gordion-relay -n gordion-relay | grep -i acme

# Verify DNS resolution
nslookup ankara.zenpacs.com.tr
```

#### 4. Rate Limiting
```bash
# Check blocked IPs
kubectl logs deployment/gordion-relay -n gordion-relay | grep "rate limit"

# Clear blocks (restart relay)
kubectl rollout restart deployment/gordion-relay -n gordion-relay
```

### Debug Commands
```bash
# Interactive debugging
kubectl exec -it deployment/gordion-relay -n gordion-relay -- /bin/sh

# Port forward for testing
kubectl port-forward svc/gordion-relay-service 8080:8080 -n gordion-relay

# Check resource usage
kubectl top pods -n gordion-relay
```

## 🔄 Maintenance

### Regular Tasks

#### Weekly
- [ ] Check deployment status: `./scripts/status.sh`
- [ ] Review hospital connectivity
- [ ] Monitor resource usage
- [ ] Check certificate expiry

#### Monthly
- [ ] Review security logs
- [ ] Update relay server image
- [ ] Backup configuration
- [ ] Performance analysis

#### Quarterly
- [ ] **Rotate hospital tokens**
- [ ] Security audit
- [ ] Disaster recovery test
- [ ] Capacity planning review

### Updates
```bash
# Update relay server
kubectl set image deployment/gordion-relay relay=gordion-relay:new-version -n gordion-relay

# Rolling update with zero downtime
kubectl rollout status deployment/gordion-relay -n gordion-relay
```

### Backup
```bash
# Backup configuration
kubectl get secret gordion-relay-tokens -n gordion-relay -o yaml > backup-tokens.yaml
kubectl get configmap gordion-relay-config -n gordion-relay -o yaml > backup-config.yaml

# Backup generated tokens
cp -r generated-tokens/ backup-$(date +%Y%m%d)/
```

## 📈 Scaling

### Horizontal Scaling
```bash
# Scale relay instances
kubectl scale deployment gordion-relay --replicas=3 -n gordion-relay

# Note: QUIC connections have session affinity
# Use sessionAffinity: ClientIP in service
```

### Geographic Distribution
```bash
# Deploy in multiple regions
# Each region gets its own relay.{region}.zenpacs.com.tr
./scripts/deploy.sh --region eu-west
./scripts/deploy.sh --region us-east
```

### Resource Optimization
```bash
# Monitor resource usage
kubectl top pods -n gordion-relay

# Adjust resource limits
kubectl patch deployment gordion-relay -n gordion-relay -p '{"spec":{"template":{"spec":{"containers":[{"name":"relay","resources":{"requests":{"cpu":"500m","memory":"512Mi"}}}]}}}}'
```

## 🎯 Production Checklist

### Before Go-Live
- [ ] All hospitals connected and tested
- [ ] DNS propagated globally
- [ ] Monitoring alerts configured
- [ ] Backup procedures tested
- [ ] Documentation distributed
- [ ] Support contacts established

### Post Go-Live
- [ ] Monitor for 24 hours
- [ ] Verify all hospital access
- [ ] Check performance metrics
- [ ] Confirm certificate auto-renewal
- [ ] Document any issues

---

## 📞 Support

### Emergency Contacts
- **System Administrator**: Monitor relay server health
- **Network Team**: DNS and LoadBalancer issues
- **Security Team**: Token rotation and security incidents
- **Hospital IT Teams**: Tunnel configuration support

### Escalation Path
1. **Check automated monitoring** (Prometheus alerts)
2. **Run diagnostic scripts** (`./scripts/status.sh`)
3. **Review logs** (relay and hospital)
4. **Contact on-call engineer** if service is down
5. **Emergency rollback** if needed (`./scripts/cleanup.sh --force`)

### Documentation
- Architecture: See `ARCHITECTURE_DIAGRAMS.md`
- Troubleshooting: See `README-production.md`
- Quick Reference: See `QUICK_REFERENCE.md`

**🎉 Congratulations!** Your Gordion Relay system is now production-ready!