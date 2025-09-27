# Quick Reference - Gordion Relay System

## 🎯 The Problem We Solved

**Before:**
```
User → Hospital PACS
         ❌ Blocked by hospital firewall
         ❌ Can't access DICOM files
```

**After:**
```
User → Relay Server → Tunnel → Hospital PACS
         ✅ No firewall changes needed
         ✅ Files accessible via public URL
```

## 🏗️ Simple Architecture

```
┌──────────┐           ┌──────────┐           ┌──────────┐
│  Doctor  │──https──> │  Relay   │<──tunnel──│ Hospital │
│ Browser  │           │  Server  │  (outbound)│   PACS   │
└──────────┘           └──────────┘           └──────────┘
                       Public VPS              Behind Firewall
                       45.123.45.67            10.1.1.50

URL: https://ankara.zenpacs.com.tr/api/instances/123/download
                    ↓
            DNS: *.zenpacs.com.tr → 45.123.45.67
                    ↓
            Relay: "ankara" → Forward to Ankara tunnel
                    ↓
            Hospital: Serve file from localhost:8083
```

## 📦 What Gets Deployed

### 1. Relay Server (1x Public VPS)
```bash
Location: Cloud provider (AWS, DigitalOcean, etc.)
IP: Static public IP (e.g., 45.123.45.67)
Ports:
  - 80/TCP  (HTTP redirect)
  - 443/UDP (QUIC - main tunnel)
  - 8080/TCP (metrics)
Software: gordion-relay binary
```

### 2. Hospital Edge Servers (Nx installations)
```bash
Location: Inside hospital network
Network: Behind firewall/NAT
Ports:
  - 11113 (DICOM C-STORE - local only)
  - 8083 (HTTP API - local only)
  - No inbound ports needed!
Software: gordionedge with tunnel enabled
```

## 🔧 Configuration Templates

### Relay Server (config.json)
```json
{
  "listen_addr": ":443",
  "domain": "zenpacs.com.tr",
  "tls": {
    "auto_cert": true
  },
  "hospitals": [
    {
      "code": "ankara",
      "subdomain": "ankara.zenpacs.com.tr",
      "token": "GENERATE_SECURE_TOKEN_HERE"
    },
    {
      "code": "istanbul",
      "subdomain": "istanbul.zenpacs.com.tr",
      "token": "GENERATE_SECURE_TOKEN_HERE"
    }
  ],
  "metrics_addr": ":8080"
}
```

### Hospital (config.json - tunnel section only)
```json
{
  "tunnel": {
    "enabled": true,
    "relay_addr": "relay.zenpacs.com.tr:443",
    "hospital_code": "ankara",
    "subdomain": "ankara.zenpacs.com.tr",
    "token": "SAME_TOKEN_AS_RELAY_CONFIG",
    "local_addr": "localhost:8083"
  }
}
```

### Environment Variables (Production - Recommended)
```bash
# Hospital Server
export GORDION_TUNNEL_ENABLED=true
export GORDION_TUNNEL_TOKEN="secure_random_token_here"
export GORDION_TUNNEL_RELAY_ADDR="relay.zenpacs.com.tr:443"
export GORDION_TUNNEL_HOSPITAL_CODE="ankara"
export GORDION_TUNNEL_SUBDOMAIN="ankara.zenpacs.com.tr"
```

## 🚀 Deployment Steps (15 Minutes)

### Step 1: Setup DNS (5 min)
```bash
# Add to your DNS provider
*.zenpacs.com.tr     A    45.123.45.67
relay.zenpacs.com.tr A    45.123.45.67
```

### Step 2: Deploy Relay (5 min)
```bash
# On public VPS
cd gordion-relay
docker-compose up -d

# Or manual
go build -o relay main.go
./relay -config config.json
```

### Step 3: Configure Hospitals (5 min)
```bash
# On each hospital server
# Edit config.json or use env vars
export GORDION_TUNNEL_TOKEN="hospital_specific_token"
systemctl restart gordionedge
```

### Step 4: Verify (1 min)
```bash
# Check relay status
curl http://relay-server:8080/status

# Should show connected hospitals:
{
  "connected_hospitals": 3,
  "hospitals": [
    {"code": "ankara", "subdomain": "ankara.zenpacs.com.tr", ...}
  ]
}

# Test access
curl https://ankara.zenpacs.com.tr/health
```

## 🔒 Security Checklist

- [ ] Generate unique token for each hospital
- [ ] Use environment variables for tokens (not config files)
- [ ] Enable TLS/SSL (Let's Encrypt auto-cert)
- [ ] Configure firewall on relay server (allow 80, 443, 8080)
- [ ] Monitor failed authentication attempts
- [ ] Set up log aggregation
- [ ] Regular security audits
- [ ] Keep tokens rotated (every 90 days)

## 📊 Monitoring Commands

```bash
# Check relay health
curl http://relay:8080/health

# List connected hospitals
curl http://relay:8080/status | jq

# Check hospital tunnel from hospital side
journalctl -u gordionedge -f | grep -i tunnel

# Monitor relay logs
docker logs -f gordion-relay

# Check QUIC connections
netstat -unp | grep 443
```

## 🐛 Troubleshooting

### Problem: Hospital can't connect
```bash
# Check 1: Can hospital reach relay?
telnet relay.zenpacs.com.tr 443

# Check 2: Correct token?
grep token /etc/gordionedge/config.json

# Check 3: Firewall allows outbound HTTPS?
curl -v https://relay.zenpacs.com.tr:8080/health

# Check 4: Check logs
journalctl -u gordionedge -n 100 | grep -i tunnel
```

### Problem: Rate limited
```bash
# Symptom: "Too many failed attempts"
# Solution: Wait 15 minutes OR fix token and restart relay

# Check relay logs
docker logs gordion-relay | grep -i "rate limit"

# Clear by restarting relay (dev only)
docker restart gordion-relay
```

### Problem: Certificate issues
```bash
# For Let's Encrypt:
# 1. Ensure port 80 is accessible
# 2. DNS points to relay
# 3. Check logs for ACME errors

docker logs gordion-relay | grep -i acme
```

### Problem: Tunnel connected but requests fail
```bash
# Check hospital's local server
curl http://localhost:8083/health

# Check relay can route to hospital
curl http://relay:8080/status

# Check tunnel is in agents map
# Should show hospital code in "connected_hospitals"
```

## 📝 Common Operations

### Add New Hospital
```bash
# 1. Generate token
TOKEN=$(openssl rand -base64 32)

# 2. Add to relay config
{
  "code": "new_hospital",
  "subdomain": "new-hospital.zenpacs.com.tr",
  "token": "$TOKEN"
}

# 3. Restart relay
docker restart gordion-relay

# 4. Configure hospital
export GORDION_TUNNEL_TOKEN="$TOKEN"
export GORDION_TUNNEL_HOSPITAL_CODE="new_hospital"
export GORDION_TUNNEL_SUBDOMAIN="new-hospital.zenpacs.com.tr"
systemctl restart gordionedge

# 5. Verify
curl http://relay:8080/status | jq '.hospitals[] | select(.code=="new_hospital")'
```

### Rotate Token
```bash
# 1. Generate new token
NEW_TOKEN=$(openssl rand -base64 32)

# 2. Update relay config (add both old and new temporarily)
# 3. Update hospital (use new token)
# 4. Wait 24 hours for all hospitals to update
# 5. Remove old token from relay config
```

### Monitor Performance
```bash
# Active tunnels
curl -s http://relay:8080/status | jq '.connected_hospitals'

# Response times (from hospital logs)
journalctl -u gordionedge | grep "tunnel request" | tail -100

# Relay resource usage
docker stats gordion-relay
```

## 💡 Key Concepts

1. **Reverse Tunnel**: Hospital initiates connection (outbound), relay uses same connection to send requests back

2. **QUIC Protocol**: Modern transport protocol over UDP, multiplexed streams, built-in encryption

3. **Token Authentication**: Pre-shared secret prevents unauthorized hospitals from connecting

4. **Rate Limiting**: Protects against brute force attacks (5 attempts / 15 min block)

5. **Wildcard DNS**: All hospital subdomains point to same relay IP

6. **No Inbound Ports**: Hospitals only need outbound HTTPS (443) - no firewall changes

## 📈 Scaling

- **Single Relay**: Handles ~1000 concurrent hospitals
- **Multiple Relays**: Use DNS load balancing or anycast
- **Hospital Failover**: Automatic reconnection on disconnect
- **Geographic Distribution**: Deploy relay servers per region

## 🎓 Protocol Flow (Simple)

```
1. Hospital → Relay: "REGISTER ankara ankara.zenpacs.com.tr TOKEN"
2. Relay → Hospital: "OK Registered"
3. [Tunnel stays open]
4. User → Relay: "GET /api/instances/123/download"
5. Relay → Hospital: [Forward GET request through tunnel]
6. Hospital → Relay: [Return DICOM file through tunnel]
7. Relay → User: [Return DICOM file]
```

## 📞 Support

- **Docs**: See ARCHITECTURE_DIAGRAMS.md for detailed flow
- **Implementation**: See IMPLEMENTATION_SUMMARY.md for technical details
- **Logs**: Check relay logs for connection issues
- **Metrics**: Use /status endpoint for monitoring

---

**Remember**: The beauty of this system is its simplicity - hospitals need ZERO network configuration changes. Just enable the tunnel and it works!