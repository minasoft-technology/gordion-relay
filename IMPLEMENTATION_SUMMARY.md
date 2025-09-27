# Implementation Summary: Unified QUIC Relay

## ✅ Changes Implemented

### 1. **Unified QUIC Architecture**
Instead of running separate QUIC and HTTPS servers on port 443, the relay now uses a single QUIC listener that handles:
- Tunnel registrations from hospitals (custom protocol)
- Direct HTTP requests (HTTP/3 over QUIC)

**Benefits:**
- No port conflicts
- Modern HTTP/3 support
- Reduced resource usage
- Simpler deployment

### 2. **Token-Based Authentication**
- Pre-shared tokens for each hospital
- Token validation on registration
- Prevents unauthorized hospitals from connecting

**Configuration:**
```json
{
  "hospitals": [{
    "code": "ankara",
    "subdomain": "ankara.zenpacs.com.tr",
    "token": "SECURE_RANDOM_TOKEN_HERE"
  }]
}
```

### 3. **Rate Limiting for Failed Authentication**
- Tracks failed authentication attempts by IP
- Blocks IPs after 5 failed attempts for 15 minutes
- Automatic cleanup of old records after 24 hours
- Protects against brute force attacks

**Features:**
- Per-IP tracking
- Exponential backoff
- Memory-efficient cleanup
- Detailed logging

### 4. **Case-Insensitive Subdomain Handling**
- Subdomain matching is now case-insensitive
- `Ankara.zenpacs.com.tr` = `ankara.zenpacs.com.tr`
- Prevents connection failures due to DNS case variations

### 5. **Environment Variable Support**
Sensitive configuration can be provided via environment variables:

**Gordionedge:**
```bash
export GORDION_TUNNEL_ENABLED=true
export GORDION_TUNNEL_TOKEN="secret_token"
export GORDION_TUNNEL_RELAY_ADDR="relay.zenpacs.com.tr:443"
export GORDION_TUNNEL_HOSPITAL_CODE="ankara"
export GORDION_TUNNEL_SUBDOMAIN="ankara.zenpacs.com.tr"
```

## 🏗️ Architecture

### Connection Flow

```
┌─────────────────────────────────────────────────────────────┐
│                     External User                            │
│                 https://ankara.zenpacs.com.tr               │
└───────────────────────┬─────────────────────────────────────┘
                        │
                        ▼
        ┌───────────────────────────────┐
        │     Relay Server (VPS)        │
        │      Port 443 (QUIC)          │
        │                               │
        │  ┌─────────────────────────┐  │
        │  │  Connection Handler     │  │
        │  │  - Reads first line     │  │
        │  │  - Detects type         │  │
        │  └────────┬────────┬───────┘  │
        │           │        │           │
        │     ┌─────┘        └─────┐    │
        │     │                    │    │
        │  ┌──▼───────┐   ┌────────▼──┐ │
        │  │ REGISTER │   │ HTTP GET  │ │
        │  │  ankara  │   │  /api/... │ │
        │  │  token   │   │           │ │
        │  └──────────┘   └───────────┘ │
        └───┬──────────────────┬─────────┘
            │                  │
            │ Tunnel           │ Forward over
            │ Registration     │ existing tunnel
            │                  │
            ▼                  ▼
    ┌──────────────────────────────────┐
    │   Hospital Network (Ankara)      │
    │                                  │
    │   ┌──────────────────────────┐  │
    │   │  Gordionedge             │  │
    │   │  - Tunnel Agent          │  │
    │   │  - Local HTTP Server     │  │
    │   │    (localhost:8083)      │  │
    │   └──────────────────────────┘  │
    │                                  │
    │   [Firewall: Only Outbound]     │
    └──────────────────────────────────┘
```

### Protocol Detection

```go
// Relay reads first line of QUIC stream
firstLine := reader.ReadString('\n')

if strings.HasPrefix(firstLine, "REGISTER ") {
    // It's a tunnel registration
    // Parse: REGISTER <code> <subdomain> <token>
    handleTunnelRegistration()
} else if strings.HasPrefix(firstLine, "GET ") {
    // It's an HTTP request
    // Route to appropriate hospital tunnel
    handleDirectHTTPRequest()
}
```

## 🔒 Security Improvements

### 1. Token Authentication
```
Hospital → Relay: REGISTER ankara ankara.zenpacs.com.tr SECRET_TOKEN
Relay validates:
  ✓ Hospital "ankara" exists in config
  ✓ Subdomain matches
  ✓ Token matches configured token
  ✓ IP not rate-limited
```

### 2. Rate Limiting
```
After 5 failed attempts from an IP:
  → Block IP for 15 minutes
  → Log security event
  → Return "Too many failed attempts"
```

### 3. Case-Insensitive Matching
```
DNS returns: Ankara.ZenPACS.com.tr
Relay normalizes: ankara.zenpacs.com.tr
Match: ✓
```

## 📋 Configuration Updates

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
      "token": "CHANGE_ME_ANKARA_TOKEN"
    }
  ],
  "idle_timeout": "30s",
  "max_concurrent_conn": 1000,
  "request_timeout": "30s",
  "metrics_addr": ":8080"
}
```

### Hospital (gordionedge config.json)
```json
{
  "tunnel": {
    "enabled": true,
    "relay_addr": "relay.zenpacs.com.tr:443",
    "hospital_code": "ankara",
    "subdomain": "ankara.zenpacs.com.tr",
    "local_addr": "localhost:8083",
    "token": "CHANGE_ME_ANKARA_TOKEN",
    "heartbeat_interval": "30s",
    "max_retries": 10,
    "retry_delay": "5s"
  }
}
```

## 🚀 Deployment

### Single Port Configuration

**Before (Conflicting):**
- Port 443/TCP: HTTPS server
- Port 443/UDP: QUIC listener
- ❌ Conflict!

**After (Unified):**
- Port 443/UDP: QUIC listener (handles both tunnel and HTTP)
- Port 80/TCP: HTTP redirect + ACME challenges
- ✅ No conflicts!

### Kubernetes Configuration
```yaml
spec:
  ports:
  - name: http
    port: 80
    protocol: TCP
  - name: quic
    port: 443
    protocol: UDP  # ← Single port for everything
  - name: metrics
    port: 8080
    protocol: TCP
```

## 🧪 Testing

### Test Token Authentication
```bash
# On hospital server
export GORDION_TUNNEL_TOKEN="test_token_123"
./gordionedge -config config.json

# Check relay logs
# Should see: "Agent registered" (if token correct)
# Should see: "Invalid token" (if token wrong)
```

### Test Rate Limiting
```bash
# Try connecting with wrong token 6 times
for i in {1..6}; do
  # Attempt registration with bad token
  echo "Attempt $i"
done

# 6th attempt should get:
# "ERROR Too many failed attempts, please try again later"
```

### Test Case-Insensitive Subdomain
```bash
# All of these should work:
curl https://ankara.zenpacs.com.tr/health
curl https://Ankara.zenpacs.com.tr/health
curl https://ANKARA.ZENPACS.COM.TR/health
```

## 📊 Monitoring

### Metrics Endpoint
```bash
# Check connected hospitals
curl http://relay:8080/status

# Response:
{
  "connected_hospitals": 2,
  "hospitals": [
    {
      "code": "ankara",
      "subdomain": "ankara.zenpacs.com.tr",
      "last_seen": "2025-09-26T10:30:00Z",
      "remote_addr": "203.0.113.45:52341"
    }
  ]
}
```

### Health Check
```bash
curl http://relay:8080/health
# Response: OK
```

## 🎯 Next Steps

1. **Generate Secure Tokens**
   ```bash
   # For each hospital, generate a random token
   openssl rand -base64 32
   ```

2. **Update Configuration**
   - Add tokens to relay config
   - Distribute tokens to hospitals (securely!)
   - Use environment variables in production

3. **Deploy Relay**
   ```bash
   cd gordion-relay
   docker-compose up -d
   ```

4. **Configure Hospitals**
   ```bash
   # On each hospital server
   export GORDION_TUNNEL_TOKEN="<hospital_specific_token>"
   systemctl restart gordionedge
   ```

5. **Verify Connections**
   ```bash
   curl http://relay:8080/status
   # Should show all hospitals connected
   ```

## ⚠️ Important Notes

1. **Port 443 UDP**: Ensure your firewall/load balancer allows UDP on port 443 for QUIC
2. **Tokens**: Keep tokens secret! Use environment variables, not config files in production
3. **Let's Encrypt**: For wildcard certificates, you may need DNS-01 challenge (not HTTP-01)
4. **Rate Limiting**: Default is 5 attempts per 15 minutes - adjust if needed
5. **Monitoring**: Check `/status` endpoint regularly to ensure hospitals stay connected

## 🐛 Troubleshooting

### Hospital Can't Connect
```bash
# Check if relay is accessible
telnet relay.zenpacs.com.tr 443

# Check token is correct
grep -i token /etc/gordionedge/config.json

# Check logs
journalctl -u gordionedge -f
```

### Rate Limited
```bash
# Wait 15 minutes or restart relay to clear blocks
# Check relay logs for:
# "IP blocked due to too many failed attempts"
```

### Certificate Issues
```bash
# For Let's Encrypt, ensure:
# 1. DNS points to relay
# 2. Port 80 is open for HTTP-01 challenges
# 3. Or use DNS-01 for wildcard certs
```