# Gordion Relay System - Architecture Diagrams

## 1. Overall System Architecture

```
┌────────────────────────────────────────────────────────────────────────┐
│                          EXTERNAL USERS                                 │
│                                                                         │
│   Doctors, Radiologists, Medical Staff accessing DICOM files          │
│                                                                         │
│   Browser: https://ankara.zenpacs.com.tr/api/instances/123/download   │
│   Browser: https://istanbul.zenpacs.com.tr/api/studies/456            │
│   Browser: https://samsun.zenpacs.com.tr/weasis.xml                   │
└────────────────┬───────────────────┬──────────────────┬────────────────┘
                 │                   │                  │
                 │ DNS Lookup        │ DNS Lookup       │ DNS Lookup
                 │ *.zenpacs.com.tr  │                  │
                 ▼                   ▼                  ▼
         ┌───────────────────────────────────────────────────────┐
         │       Wildcard DNS: *.zenpacs.com.tr                  │
         │       Points to: 45.123.45.67 (Public VPS)            │
         └───────────────────────────────────────────────────────┘
                                 │
                                 │ All subdomains resolve to
                                 │ same relay server IP
                                 ▼
╔════════════════════════════════════════════════════════════════════════╗
║                    RELAY SERVER (Public VPS)                           ║
║                  IP: 45.123.45.67 (Public)                             ║
╚════════════════════════════════════════════════════════════════════════╝

    ┌────────────────────────────────────────────────────────────┐
    │              Port 80 (TCP)                                  │
    │   ┌──────────────────────────────────────────────┐         │
    │   │  HTTP Redirect Server                         │         │
    │   │  • ACME HTTP-01 Challenges (Let's Encrypt)   │         │
    │   │  • Redirect all HTTP → HTTPS                 │         │
    │   └──────────────────────────────────────────────┘         │
    └────────────────────────────────────────────────────────────┘

    ┌────────────────────────────────────────────────────────────┐
    │              Port 443 (TCP/HTTPS + WebSocket)               │
    │   ┌──────────────────────────────────────────────┐         │
    │   │  HTTPS Server + WebSocket Upgrade            │         │
    │   │                                               │         │
    │   │  1. Accept HTTPS connection                  │         │
    │   │  2. Upgrade /tunnel to WebSocket             │         │
    │   │  3. Handle protocol:                         │         │
    │   │     • "REGISTER ..." → WebSocket tunnel      │         │
    │   │     • Other HTTP → forward via tunnel        │         │
    │   └──────┬─────────────────────┬─────────────────┘         │
    │          │                     │                            │
    │    ┌─────▼──────┐       ┌──────▼─────────┐               │
    │    │ Tunnel     │       │ HTTP Request   │               │
    │    │ Handler    │       │ Handler        │               │
    │    └─────┬──────┘       └──────┬─────────┘               │
    │          │                     │                            │
    │          │                     │                            │
    └──────────┼─────────────────────┼────────────────────────────┘
               │                     │
               │                     │
    ┌────────────────────────────────────────────────────────────┐
    │              Port 8080 (TCP)                                │
    │   ┌──────────────────────────────────────────────┐         │
    │   │  Metrics & Status Server                      │         │
    │   │  • GET /health → OK                           │         │
    │   │  • GET /status → Connected hospitals JSON    │         │
    │   └──────────────────────────────────────────────┘         │
    └────────────────────────────────────────────────────────────┘

               │                     │
               │ Tunnel              │ Forward request
               │ Registration        │ through tunnel
               │                     │
               ▼                     │
    ┌──────────────────────┐        │
    │  Hospital Registry   │        │
    │  ┌────────────────┐  │        │
    │  │ ankara → Conn1 │  │        │
    │  │ istanbul→Conn2 │  │        │
    │  │ samsun → Conn3 │  │        │
    │  └────────────────┘  │        │
    └──────────────────────┘        │
               │                     │
               └─────────────────────┘

         ┌─────────────┴──────────────┬─────────────────────┐
         │                            │                     │
         ▼                            ▼                     ▼
╔════════════════════╗    ╔════════════════════╗   ╔════════════════════╗
║  HOSPITAL ANKARA   ║    ║ HOSPITAL ISTANBUL  ║   ║  HOSPITAL SAMSUN   ║
║  (Private Network) ║    ║  (Private Network) ║   ║  (Private Network) ║
╚════════════════════╝    ╚════════════════════╝   ╚════════════════════╝

┌──────────────────────────────────────────────────────────────────────┐
│                     🏥 HOSPITAL NETWORK                              │
│                  (Behind Firewall / NAT)                             │
│                  Private IP: 10.1.1.0/24                             │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │                    🔥 FIREWALL                              │   │
│  │  ✅ Outbound HTTPS (443) → Allowed                          │   │
│  │  ❌ Inbound connections → Blocked                           │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │            GORDIONEDGE SERVER (10.1.1.50)                   │   │
│  │                                                              │   │
│  │  ┌────────────────────────────────────────────────────┐    │   │
│  │  │  Tunnel Agent                                       │    │   │
│  │  │  • Connects TO relay (outbound)                    │    │   │
│  │  │  • Keeps connection alive (heartbeat)              │    │   │
│  │  │  • Receives requests FROM relay                    │    │   │
│  │  │  • Forwards to localhost:8083                      │    │   │
│  │  └───────────────┬────────────────────────────────────┘    │   │
│  │                  │                                          │   │
│  │                  ▼                                          │   │
│  │  ┌────────────────────────────────────────────────────┐    │   │
│  │  │  Gordionedge HTTP Server (localhost:8083)          │    │   │
│  │  │  • DICOM C-STORE receiver (port 11113)            │    │   │
│  │  │  • HTTP API for DICOM files                        │    │   │
│  │  │  • Local cache (100GB)                             │    │   │
│  │  │  • MinIO uploader                                  │    │   │
│  │  │  • BadgerDB pipeline                               │    │   │
│  │  └────────────────────────────────────────────────────┘    │   │
│  │                                                              │   │
│  └─────────────────────────────────────────────────────────────┘   │
│                                                                      │
│  ┌─────────────────────────────────────────────────────────────┐   │
│  │  PACS / Modality Devices                                    │   │
│  │  • CT Scanner → DICOM C-STORE → Gordionedge:11113          │   │
│  │  • MRI Machine → DICOM C-STORE → Gordionedge:11113         │   │
│  │  • X-Ray → DICOM C-STORE → Gordionedge:11113               │   │
│  └─────────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────────────┘
```

## 2. Tunnel Registration Flow (Hospital Startup)

```
┌─────────────────────────────────────────────────────────────────────┐
│                   TUNNEL REGISTRATION SEQUENCE                       │
└─────────────────────────────────────────────────────────────────────┘

Hospital                          Relay Server
Gordionedge                       (45.123.45.67:443)
   │                                    │
   │ 1. Gordionedge starts              │
   │    Config loaded:                  │
   │    - hospital_code: "ankara"       │
   │    - token: "SECRET123"            │
   │    - relay: relay.zenpacs.com.tr   │
   │                                    │
   │ 2. DNS Lookup                      │
   │    relay.zenpacs.com.tr            │
   │    → 45.123.45.67                  │
   │                                    │
   │ 3. Open WebSocket Connection        │
   │    (Outbound HTTPS - Firewall allows)│
   ├──────── wss://relay/tunnel ───────>│
   │    TLS Handshake                   │
   │<──────── TLS Certificate ──────────┤
   │                                    │
   │ 4. Connection Established          │
   │    Local:  10.1.1.50:52341        │
   │    Remote: 45.123.45.67:443       │
   │                                    │
   │ 5. Send Registration               │
   │                                    │
   │ 6. Send Registration               │
   ├─ "REGISTER ankara ankara.zenpacs.com.tr SECRET123" ───>│
   │                                    │
   │                                    │ 7. Relay Validates
   │                                    │    ✓ Parse fields
   │                                    │    ✓ Check rate limit
   │                                    │    ✓ Validate subdomain
   │                                    │    ✓ Check token
   │                                    │
   │                                    │ 8. Token Match?
   │                                    │    Config: "SECRET123"
   │                                    │    Received: "SECRET123"
   │                                    │    ✅ Match!
   │                                    │
   │                                    │ 9. Register Agent
   │                                    │    agents["ankara"] = {
   │                                    │      Connection: conn,
   │                                    │      Subdomain: "ankara.zenpacs.com.tr",
   │                                    │      LastSeen: now()
   │                                    │    }
   │                                    │
   │ 10. Receive Success                │
   │<──── "OK Registered\n" ────────────┤
   │                                    │
   │ 11. Log Success                    │
   │     "✓ Tunnel agent started"       │
   │                                    │
   │ 12. Start Heartbeat Loop           │
   │     Every 30 seconds:              │
   ├───── "HEARTBEAT\n" ───────────────>│
   │                                    │───> Update LastSeen
   │                                    │
   │ 13. Listen for Requests            │
   │     WebSocket messages (non-block) │
   │     Waiting for HTTP requests...   │
   │                                    │
   │                    [TUNNEL ACTIVE] │
   │                                    │

┌─────────────────────────────────────────────────────────────────────┐
│  At this point, the hospital is registered and ready to serve files  │
│  Users can now access: https://ankara.zenpacs.com.tr/...             │
└─────────────────────────────────────────────────────────────────────┘
```

## 3. HTTP Request Flow (User Accessing DICOM File)

```
┌─────────────────────────────────────────────────────────────────────┐
│                     HTTP REQUEST FLOW                                │
└─────────────────────────────────────────────────────────────────────┘

User Browser              Relay Server           Hospital (Ankara)
    │                          │                        │
    │ 1. User clicks link      │                        │
    │    https://ankara.zenpacs.com.tr                 │
    │         /api/instances/123/download               │
    │                          │                        │
    │ 2. DNS Lookup            │                        │
    │    ankara.zenpacs.com.tr │                        │
    │    → 45.123.45.67       │                        │
    │                          │                        │
    │ 3. HTTPS Connection      │
    ├──── Dial 45.123.45.67:443 ──>│                   │
    │    TLS Handshake         │                        │
    │<──── Certificate ────────┤                        │
    │    (*.zenpacs.com.tr)    │                        │
    │                          │                        │
    │ 4. Send HTTP Request     │                        │
    ├─ "GET /api/instances/123/download HTTP/1.1\n"───>│
    ├─ "Host: ankara.zenpacs.com.tr\n"─────────────────>│
    ├─ "User-Agent: Mozilla/5.0\n"─────────────────────>│
    ├─ "\n" (end headers)──────────────────────────────>│
    │                          │                        │
    │                          │ 5. Protocol Detection  │
    │                          │    Read first line:    │
    │                          │    "GET /api/..."      │
    │                          │    → It's HTTP request │
    │                          │                        │
    │                          │ 6. Extract Hospital    │
    │                          │    Host: ankara.zenpacs.com.tr
    │                          │    Extract: "ankara"   │
    │                          │                        │
    │                          │ 7. Find Tunnel         │
    │                          │    agents["ankara"]    │
    │                          │    → Found! ✓          │
    │                          │                        │
    │                          │ 8. Forward via WebSocket │
    │                          ├── Send over WSS ───────>│
    │                          │    (on existing tunnel) │
    │                          │                        │
    │                          │ 9. Forward Request     │
    │                          ├─ "GET /api/instances/123/download HTTP/1.1" ─>│
    │                          ├─ "Host: ankara.zenpacs.com.tr" ───────────────>│
    │                          ├─ ... (all headers) ────────────────────────────>│
    │                          │                        │
    │                          │                        │ 10. Process Request
    │                          │                        │     Forward to local:
    │                          │                        │     http://localhost:8083
    │                          │                        │         /api/instances/123/download
    │                          │                        │
    │                          │                        │ 11. Gordionedge Server
    │                          │                        │     • Query BadgerDB
    │                          │                        │     • Find file in cache
    │                          │                        │     • Read DICOM file
    │                          │                        │
    │                          │                        │ 12. Generate Response
    │                          │                        │     HTTP/1.1 200 OK
    │                          │                        │     Content-Type: application/dicom
    │                          │                        │     Content-Length: 5242880
    │                          │                        │
    │                          │ 13. Send Response      │
    │                          │<─ HTTP/1.1 200 OK ─────┤
    │                          │<─ Headers ─────────────┤
    │                          │<─ DICOM file data ─────┤
    │                          │   (streaming...)       │
    │                          │                        │
    │ 14. Forward to User      │                        │
    │<─ HTTP/1.1 200 OK ───────┤                        │
    │<─ Headers ───────────────┤                        │
    │<─ DICOM file data ───────┤                        │
    │   (streaming 5MB...)     │                        │
    │                          │                        │
    │ 15. Download Complete    │                        │
    │    User views DICOM      │                        │
    │    in browser/viewer     │                        │
    │                          │                        │

┌─────────────────────────────────────────────────────────────────────┐
│  Total time: ~500ms (depends on file size and network)              │
│  File served from hospital without any inbound firewall rules       │
└─────────────────────────────────────────────────────────────────────┘
```

## 4. Authentication & Security Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│              AUTHENTICATION & RATE LIMITING FLOW                     │
└─────────────────────────────────────────────────────────────────────┘

Hospital Attempt              Relay Server              Security State
     │                             │                          │
     │ Attempt 1 (Wrong Token)     │                          │
     ├─ REGISTER ankara ankara.zenpacs.com.tr WRONG_TOKEN ──>│
     │                             │                          │
     │                             │ Validate Token          │
     │                             │ Config: "SECRET123"     │
     │                             │ Got: "WRONG_TOKEN"      │
     │                             │ ❌ Mismatch             │
     │                             │                          │
     │                             │ Record Failed Attempt    │
     │                             │ failedAttempts[10.1.1.50] = {
     │                             │   Count: 1,              │
     │                             │   LastAttempt: now()     │
     │                             │ }                        │
     │                             │                          │
     │<─── "ERROR Invalid token\n" ┤                          │
     │                             │                          │
     │ Attempt 2-4 (Wrong Token)   │                          │
     ├─ REGISTER ... WRONG_TOKEN ─>│                          │
     │<─── "ERROR Invalid token\n" ┤───> Count: 2, 3, 4      │
     │                             │                          │
     │ Attempt 5 (Wrong Token)     │                          │
     ├─ REGISTER ... WRONG_TOKEN ─>│                          │
     │                             │                          │
     │                             │ Record Failed Attempt    │
     │                             │ Count: 5 >= Max (5)     │
     │                             │ 🚨 RATE LIMIT TRIGGERED │
     │                             │ BlockedUntil: now()+15min│
     │                             │                          │
     │<─── "ERROR Invalid token\n" ┤                          │
     │                             │                          │
     │ Attempt 6 (Correct Token!)  │                          │
     ├─ REGISTER ... SECRET123 ───>│                          │
     │                             │                          │
     │                             │ Check Rate Limit        │
     │                             │ isRateLimited(10.1.1.50)│
     │                             │ now() < BlockedUntil    │
     │                             │ ✋ BLOCKED              │
     │                             │                          │
     │<─ "ERROR Too many failed attempts, try again later" ──┤
     │                             │                          │
     │ ... Wait 15 minutes ...     │                          │
     │                             │                          │
     │ Attempt 7 (After cooldown)  │                          │
     ├─ REGISTER ... SECRET123 ───>│                          │
     │                             │                          │
     │                             │ Check Rate Limit        │
     │                             │ now() > BlockedUntil    │
     │                             │ ✅ Not blocked          │
     │                             │                          │
     │                             │ Validate Token          │
     │                             │ "SECRET123" == "SECRET123"
     │                             │ ✅ Match!               │
     │                             │                          │
     │                             │ Clear Failed Attempts    │
     │                             │ delete(failedAttempts[10.1.1.50])
     │                             │                          │
     │<─── "OK Registered\n" ──────┤                          │
     │                             │                          │
     │ ✅ Connected Successfully   │                          │
     │                             │                          │

┌─────────────────────────────────────────────────────────────────────┐
│  Security Features:                                                  │
│  • Max 5 failed attempts before blocking                            │
│  • 15 minute block duration                                         │
│  • Automatic cleanup after 24 hours                                 │
│  • Per-IP tracking (not per-hospital)                               │
│  • Detailed security logging                                        │
└─────────────────────────────────────────────────────────────────────┘
```

## 5. Network & Firewall Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                    NETWORK TOPOLOGY                                  │
└─────────────────────────────────────────────────────────────────────┘

                        Internet
                           │
                           │
           ┌───────────────┼───────────────┐
           │               │               │
    ┌──────▼─────┐  ┌──────▼─────┐  ┌─────▼──────┐
    │ User 1     │  │ User 2     │  │ User 3     │
    │ Laptop     │  │ Tablet     │  │ Phone      │
    └────────────┘  └────────────┘  └────────────┘
           │               │               │
           └───────────────┼───────────────┘
                           │
                    DNS Resolution
                *.zenpacs.com.tr → 45.123.45.67
                           │
                           ▼
        ╔══════════════════════════════════════╗
        ║    RELAY SERVER (Public VPS)         ║
        ║    IP: 45.123.45.67                  ║
        ║                                      ║
        ║    Ports:                            ║
        ║    • 80/TCP  - HTTP Redirect         ║
        ║    • 443/UDP - QUIC (Main)           ║
        ║    • 8080/TCP - Metrics              ║
        ╚══════════════════════════════════════╝
                           │
                           │ Internet
                           │
           ┌───────────────┼───────────────┐
           │               │               │
           ▼               ▼               ▼

┌────────────────────┐ ┌────────────────────┐ ┌────────────────────┐
│  HOSPITAL ANKARA   │ │ HOSPITAL ISTANBUL  │ │  HOSPITAL SAMSUN   │
│  Public IP:        │ │  Public IP:        │ │  Public IP:        │
│  203.0.113.45      │ │  198.51.100.23     │ │  192.0.2.87        │
│  (NAT)             │ │  (NAT)             │ │  (NAT)             │
└────────┬───────────┘ └────────┬───────────┘ └────────┬───────────┘
         │                      │                      │
    ┌────▼─────────────────┐ ┌──▼──────────────────┐ ┌─▼────────────────┐
    │ 🔥 FIREWALL         │ │ 🔥 FIREWALL        │ │ 🔥 FIREWALL     │
    │                     │ │                    │ │                  │
    │ INBOUND:            │ │ INBOUND:           │ │ INBOUND:         │
    │ ❌ Port 443: DENY   │ │ ❌ Port 443: DENY  │ │ ❌ Port 443: DENY│
    │ ❌ Port 8083: DENY  │ │ ❌ Port 8083: DENY │ │ ❌ Port 8083: DENY
    │ ❌ Port 11113: DENY │ │ ❌ Port 11113: DENY│ │ ❌ Port 11113: DENY
    │                     │ │                    │ │                  │
    │ OUTBOUND:           │ │ OUTBOUND:          │ │ OUTBOUND:        │
    │ ✅ Port 443: ALLOW  │ │ ✅ Port 443: ALLOW │ │ ✅ Port 443: ALLOW
    │ ✅ HTTPS: ALLOW     │ │ ✅ HTTPS: ALLOW    │ │ ✅ HTTPS: ALLOW  │
    │                     │ │                    │ │                  │
    │ ESTABLISHED:        │ │ ESTABLISHED:       │ │ ESTABLISHED:     │
    │ ✅ Return traffic   │ │ ✅ Return traffic  │ │ ✅ Return traffic│
    │    on established   │ │    on established  │ │    on established│
    │    connections      │ │    connections     │ │    connections   │
    └─────────┬───────────┘ └─────────┬──────────┘ └─────────┬────────┘
              │                       │                       │
              │ Private Network       │ Private Network       │ Private Network
              │ 10.1.1.0/24          │ 10.2.1.0/24          │ 10.3.1.0/24
              │                       │                       │
    ┌─────────▼───────────┐ ┌─────────▼──────────┐ ┌─────────▼─────────┐
    │ GORDIONEDGE        │ │ GORDIONEDGE       │ │ GORDIONEDGE      │
    │ 10.1.1.50          │ │ 10.2.1.50         │ │ 10.3.1.50        │
    │                    │ │                   │ │                  │
    │ • DICOM: 11113     │ │ • DICOM: 11113    │ │ • DICOM: 11113   │
    │ • HTTP: 8083       │ │ • HTTP: 8083      │ │ • HTTP: 8083     │
    │ • Tunnel: Active   │ │ • Tunnel: Active  │ │ • Tunnel: Active │
    │                    │ │                   │ │                  │
    │ Outbound to:       │ │ Outbound to:      │ │ Outbound to:     │
    │ relay:443 ✅       │ │ relay:443 ✅      │ │ relay:443 ✅     │
    └────────────────────┘ └───────────────────┘ └──────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│  KEY INSIGHT:                                                        │
│  • Hospitals initiate OUTBOUND connections (firewall allows)        │
│  • Return traffic on established connections is allowed             │
│  • NO inbound ports need to be opened                               │
│  • Relay server uses existing connection to send requests           │
└─────────────────────────────────────────────────────────────────────┘
```

## 6. Data Flow - DICOM File Upload & Download

```
┌─────────────────────────────────────────────────────────────────────┐
│              COMPLETE DATA FLOW (CT Scan to Doctor)                  │
└─────────────────────────────────────────────────────────────────────┘

HOSPITAL                    GORDIONEDGE                RELAY         DOCTOR
(CT Scanner)                (10.1.1.50)              (Cloud)       (Browser)

    │                            │                        │              │
    │ 1. CT Scan Complete        │                        │              │
    │    Patient: John Doe       │                        │              │
    │    Study: CT Brain         │                        │              │
    │    Images: 120 slices      │                        │              │
    │                            │                        │              │
    │ 2. DICOM C-STORE           │                        │              │
    ├─ Send 120 DICOM files ────>│                        │              │
    │    (port 11113)            │                        │              │
    │                            │ 3. Process Images      │              │
    │                            │    • Save to BadgerDB  │              │
    │                            │    • Store in cache    │              │
    │                            │    • Compress (GDCM)   │              │
    │                            │    • Generate metadata │              │
    │                            │                        │              │
    │                            │ 4. Upload to MinIO     │              │
    │                            ├─ Upload compressed ──>MinIO           │
    │                            │   (s3.zenpacs.com.tr) │              │
    │                            │                        │              │
    │                            │ 5. Generate Weasis XML │              │
    │                            │    Study metadata      │              │
    │                            │    Instance URLs       │              │
    │                            │                        │              │
    │                            │ 6. Publish to NATS     │              │
    │                            ├─ Study Completed ────>NATS            │
    │                            │   Topic: dicom.study   │              │
    │                            │                        │              │
    │                            │ 7. Ready to Serve      │              │
    │                            │    Tunnel: Active ✅   │              │
    │                            │<────── Connected ──────┤              │
    │                            │                        │              │
    │                            │                        │              │
    │                            │                        │ 8. Doctor     │
    │                            │                        │    Receives   │
    │                            │                        │    Notification
    │                            │                        │              │
    │                            │                        │ 9. Click Link │
    │                            │                        │    ankara.zenpacs.com.tr
    │                            │                        │    /weasis.xml│
    │                            │                        │<─────────────┤
    │                            │                        │              │
    │                            │                        │ 10. Route to  │
    │                            │                        │     Hospital  │
    │                            │ 11. Request Weasis XML │              │
    │                            │<───────────────────────┤              │
    │                            │    (via tunnel)        │              │
    │                            │                        │              │
    │                            │ 12. Serve Weasis XML   │              │
    │                            ├────────────────────────>│              │
    │                            │    (contains URLs)     ├──────────────>│
    │                            │                        │              │
    │                            │                        │ 13. Parse XML │
    │                            │                        │     Get image │
    │                            │                        │     URLs      │
    │                            │                        │              │
    │                            │                        │ 14. Request   │
    │                            │                        │     Images    │
    │                            │                        │<─────────────┤
    │                            │                        │   GET /api/   │
    │                            │                        │   instances/  │
    │                            │                        │   001/download│
    │                            │                        │              │
    │                            │ 15. Serve Images       │              │
    │                            │    (120 requests)      │              │
    │                            │<───────────────────────┤              │
    │                            ├────────────────────────>│              │
    │                            │    DICOM files         ├──────────────>│
    │                            │    (from cache or MinIO)              │
    │                            │                        │              │
    │                            │                        │ 16. View      │
    │                            │                        │     Study in  │
    │                            │                        │     Browser   │
    │                            │                        │              │
    │                            │                        │     ┌───────┐│
    │                            │                        │     │ CT    ││
    │                            │                        │     │ Brain ││
    │                            │                        │     │ 120   ││
    │                            │                        │     │ images││
    │                            │                        │     └───────┘│

┌─────────────────────────────────────────────────────────────────────┐
│  Total time from scan to view: 2-5 minutes                          │
│  • C-STORE: 1-2 min (depends on image count)                        │
│  • Processing: 30-60 sec (compression, metadata)                    │
│  • Upload: 30-60 sec (to MinIO, if configured)                      │
│  • View: < 10 sec (tunnel routing)                                  │
└─────────────────────────────────────────────────────────────────────┘
```

## 7. Component Interaction Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                    COMPONENT INTERACTION                             │
└─────────────────────────────────────────────────────────────────────┘

╔════════════════════════════════════════════════════════════════════╗
║                        RELAY SERVER                                 ║
╚════════════════════════════════════════════════════════════════════╝

    ┌──────────────────────────────────────────────────────────┐
    │              main.go (Entry Point)                        │
    │  • Load configuration                                     │
    │  • Setup logging                                          │
    │  • Create Server instance                                 │
    │  • Start server                                           │
    │  • Handle signals (Ctrl+C)                               │
    └───────────────────────┬──────────────────────────────────┘
                            │
                            ▼
    ┌──────────────────────────────────────────────────────────┐
    │              Server (relay/server.go)                     │
    │                                                           │
    │  Components:                                              │
    │  • QUIC Listener (port 443/UDP)                          │
    │  • HTTP Redirect Server (port 80/TCP)                    │
    │  • Metrics Server (port 8080/TCP)                        │
    │  • Agent Registry (map[hospitalCode]Connection)          │
    │  • Rate Limiter (map[IP]FailedAttempts)                  │
    │  • TLS Manager (Let's Encrypt or manual)                 │
    └─┬───────────────┬──────────────┬──────────────┬──────────┘
      │               │              │              │
      │               │              │              │
   ┌──▼────┐   ┌─────▼────┐   ┌─────▼────┐   ┌────▼────┐
   │ Accept │   │ HTTP     │   │ Metrics  │   │ Rate    │
   │ QUIC   │   │ Redirect │   │ /status  │   │ Limiter │
   │ Conn   │   │ Server   │   │ /health  │   │ Cleanup │
   └───┬────┘   └──────────┘   └──────────┘   └─────────┘
       │
       │ For each connection
       ▼
   ┌────────────────────────────────────────┐
   │  handleConnection()                    │
   │  • Accept first stream                 │
   │  • Read first line                     │
   │  • Determine protocol                  │
   └───┬───────────────────┬────────────────┘
       │                   │
       │                   │
    "REGISTER ..."    "GET /api/..."
       │                   │
       ▼                   ▼
   ┌────────────┐    ┌──────────────┐
   │  Tunnel    │    │  HTTP        │
   │  Register  │    │  Request     │
   │            │    │              │
   │  • Parse   │    │  • Extract   │
   │  • Validate│    │    subdomain │
   │  • Auth    │    │  • Find      │
   │  • Store   │    │    tunnel    │
   └────────────┘    │  • Forward   │
                     └──────────────┘


╔════════════════════════════════════════════════════════════════════╗
║                      GORDIONEDGE (Hospital)                         ║
╚════════════════════════════════════════════════════════════════════╝

    ┌──────────────────────────────────────────────────────────┐
    │              main.go (Entry Point)                        │
    │  • Load configuration                                     │
    │  • Setup logging                                          │
    │  • Create EdgeService                                     │
    │  • Start service                                          │
    │  • Handle signals                                         │
    └───────────────────────┬──────────────────────────────────┘
                            │
                            ▼
    ┌──────────────────────────────────────────────────────────┐
    │         EdgeService (service/service_impl.go)             │
    │                                                           │
    │  Components:                                              │
    │  • BadgerDB Pipeline                                      │
    │  • DICOM Server (port 11113)                             │
    │  • HTTP Server (port 8083)                               │
    │  • MinIO Client                                           │
    │  • NATS Publisher                                         │
    │  • Tunnel Agent ← NEW                                    │
    └─┬───────┬────────┬───────┬────────┬──────────┬──────────┘
      │       │        │       │        │          │
      │       │        │       │        │          │
   ┌──▼──┐ ┌─▼───┐ ┌──▼──┐ ┌──▼──┐  ┌──▼───┐  ┌──▼───────┐
   │DICOM│ │HTTP │ │Minio│ │NATS │  │Cache │  │ Tunnel   │
   │C-   │ │API  │ │Upload│ │Pub  │  │Mgmt  │  │ Agent    │
   │STORE│ │Serve│ │     │ │     │  │      │  │          │
   └──┬──┘ └──┬──┘ └─────┘ └─────┘  └──────┘  └─────┬────┘
      │       │                                       │
      │       │                                       │
      │       │ Serves files locally                  │
      │       │ on localhost:8083                     │
      │       │                                       │
      │       │                          ┌────────────▼────────┐
      │       │                          │  tunnel/agent.go    │
      │       │                          │                     │
      │       │                          │  • Connect to relay │
      │       │                          │  • Register tunnel  │
      │       │                          │  • Send heartbeat   │
      │       │                          │  • Accept streams   │
      │       │                          │  • Forward requests │
      │       └──────────────────────────┤    to localhost     │
      │                                  └─────────────────────┘
      │
      │ DICOM files from CT/MRI scanners
      ▼
   ┌─────────────────────────────────┐
   │  DICOM Pipeline                 │
   │  1. Receive Stage               │
   │  2. Compress Stage (optional)   │
   │  3. Storage Stage (cache)       │
   │  4. Upload Stage (MinIO)        │
   │  5. XML Generation Stage        │
   │  6. NATS Publish Stage          │
   └─────────────────────────────────┘
```

These diagrams show the complete system architecture, data flow, security mechanisms, and component interactions. The system successfully allows hospitals behind firewalls to serve DICOM files publicly without any inbound port configuration!
