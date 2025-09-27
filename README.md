# Gordion Relay Server

A reverse tunnel relay server that allows hospitals to serve DICOM files through public subdomains without requiring inbound firewall rules.

## Architecture

```
External User → hospital.domain.com → Relay Server → HTTPS/WebSocket Tunnel → Hospital → Gordionedge
```

### How it works:

1. **DNS Setup**: Wildcard DNS `*.zenpacs.com.tr` points to the relay server
2. **Hospital Agents**: Each hospital runs gordionedge with tunnel agent enabled
3. **Outbound Tunnels**: Hospital agents connect outbound via HTTPS/WebSocket to relay (no firewall changes needed)
4. **Request Routing**: Relay routes requests based on subdomain to the correct hospital tunnel
5. **Firewall Friendly**: Uses standard HTTPS port 443 TCP - works through any firewall

## Configuration

### Relay Server Configuration

```json
{
  "listen_addr": ":443",
  "domain": "zenpacs.com.tr",
  "tls": {
    "auto_cert": true
  },
  "idle_timeout": "30s",
  "max_concurrent_conn": 1000,
  "request_timeout": "30s",
  "metrics_addr": ":8080"
}
```

### Hospital Configuration (Gordionedge)

Add to your `config.json`:

```json
{
  "tunnel": {
    "enabled": true,
    "relay_addr": "relay.zenpacs.com.tr:443",
    "hospital_code": "ankara",
    "subdomain": "ankara.zenpacs.com.tr",
    "local_addr": "localhost:8083",
    "heartbeat_interval": "30s",
    "max_retries": 10,
    "retry_delay": "5s"
  }
}
```

## DNS Setup

### Required DNS Records

```
# Wildcard A record - routes ALL subdomains to relay
*.zenpacs.com.tr.     IN  A   YOUR_RELAY_SERVER_IP

# Relay server itself
relay.zenpacs.com.tr. IN  A   YOUR_RELAY_SERVER_IP

# Optional: Main domain
zenpacs.com.tr.       IN  A   YOUR_RELAY_SERVER_IP
```

### Example Hospital URLs

With this setup, hospitals become accessible as:
- `https://ankara.zenpacs.com.tr/api/instances/123/download`
- `https://istanbul.zenpacs.com.tr/api/instances/456/download`
- `https://samsun.zenpacs.com.tr/api/instances/789/download`

## Deployment

### Docker Compose

```bash
# Copy example config
cp config.example.json config.json

# Edit config.json with your domain

# Build and run
docker-compose up -d
```

### Kubernetes

```bash
# Apply all manifests (see DEPLOY_TO_K8S.md for detailed instructions)
kubectl apply -f k8s/namespace.yaml
kubectl apply -f k8s/pvc-certs.yaml
kubectl apply -f k8s/secret-example.yaml
kubectl apply -f k8s/configmap.yaml
kubectl apply -f k8s/deployment.yaml
kubectl apply -f k8s/service.yaml

# Check status
kubectl get pods -n gordion-relay
kubectl get svc -n gordion-relay
```

### Manual Deployment

```bash
# Build
go build -o relay main.go

# Run with config
./relay -config config.json

# Or with debug logging
./relay -config config.json -debug
```

## TLS Certificates

### Automatic (Let's Encrypt)

Set `"auto_cert": true` in config. The relay will automatically obtain and renew certificates for your domain.

### Manual Certificates

```json
{
  "tls": {
    "auto_cert": false,
    "cert_file": "/path/to/cert.pem",
    "key_file": "/path/to/key.pem"
  }
}
```

## Monitoring

### Health Check

```bash
curl http://relay-server:8080/health
```

### Status and Connected Hospitals

```bash
curl http://relay-server:8080/status
```

Response:
```json
{
  "connected_hospitals": 3,
  "hospitals": [
    {
      "code": "ankara",
      "subdomain": "ankara.zenpacs.com.tr",
      "last_seen": "2024-01-15T10:30:00Z",
      "remote_addr": "10.1.1.100:52341"
    }
  ]
}
```

## Security

- **TLS Encryption**: All tunnel traffic is encrypted with HTTPS/TLS
- **Hospital Authentication**: Token-based authentication per hospital
- **No Inbound Ports**: Hospitals only make outbound HTTPS connections
- **Request Validation**: Relay validates subdomain ownership
- **Rate Limiting**: Protection against brute force attacks (5 attempts/15min)

## Troubleshooting

### Hospital Can't Connect

1. Check relay server is running: `curl http://relay:8080/health`
2. Check hospital config has correct `relay_addr`
3. Check hospital can reach relay: `telnet relay.domain.com 443`
4. Check gordionedge logs for tunnel errors

### Web Requests Fail

1. Check DNS resolution: `nslookup hospital.domain.com`
2. Check hospital is connected: `curl http://relay:8080/status`
3. Check relay logs for routing errors
4. Test hospital locally: `curl http://localhost:8083/api/instances/123/download`

### Certificate Issues

1. For auto-cert, ensure ports 80/443 are accessible
2. Check certificate cache directory permissions
3. Verify domain ownership for Let's Encrypt

## Scaling

- **Single Relay**: One relay server can handle thousands of hospitals
- **Load Balancing**: Use multiple relay instances behind a load balancer
- **Hospital Failover**: Hospitals automatically reconnect if relay restarts

## Performance

- **HTTPS/WebSocket**: Persistent bidirectional tunnels
- **Connection Pooling**: Long-lived connections avoid handshake overhead
- **Streaming**: Large DICOM files stream efficiently through tunnels
- **Firewall Compatible**: TCP port 443 works everywhere (no UDP issues)