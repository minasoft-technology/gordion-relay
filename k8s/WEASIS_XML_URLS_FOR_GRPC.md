# Weasis XML Download URLs for gRPC Mode

## Current Issue: URL Path Mismatch ⚠️

There's a **path mismatch** between what the edge generates and what the relay expects in gRPC mode:

### Edge XML Generation (gordionedge)
**File:** `internal/pipeline/xml_stage.go:304-305`

```go
if x.publicBaseURL != "" {
    return fmt.Sprintf("/api/instances/%s/download", inst.SOPInstanceUID)
}
```

**Generates:** `/api/instances/{instanceUID}/download`

### Relay gRPC Handler (gordion-relay)
**File:** `internal/relay/server_grpc.go:312`

```go
mux.HandleFunc("/instances/", s.handleInstanceDownload)
```

**Expects:** `/instances/{instanceUID}/*`

---

## Solution Options

### Option A: Fix Relay (Add /api/instances Handler) ✅ RECOMMENDED

**File:** `internal/relay/server_grpc.go:311-313`

**BEFORE:**
```go
func (s *GRPCServer) startHTTPServer(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/instances/", s.handleInstanceDownload)
	mux.HandleFunc("/health", s.handleHealth)
```

**AFTER:**
```go
func (s *GRPCServer) startHTTPServer(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/instances/", s.handleInstanceDownload)
	mux.HandleFunc("/api/instances/", s.handleInstanceDownload)  // ← ADD THIS
	mux.HandleFunc("/health", s.handleHealth)
```

**Why:** This matches WebSocket server behavior and maintains backward compatibility.

---

### Option B: Fix Edge XML Generation

**File:** `internal/pipeline/xml_stage.go:302-306`

**BEFORE:**
```go
// When publicBaseURL is set, use API endpoint that supports token authentication
// This allows the hub/API to add tokens before serving to clients
if x.publicBaseURL != "" {
    return fmt.Sprintf("/api/instances/%s/download", inst.SOPInstanceUID)
}
```

**AFTER:**
```go
// When publicBaseURL is set, use instances endpoint
// Relay handles token authentication
if x.publicBaseURL != "" {
    return fmt.Sprintf("/instances/%s/download", inst.SOPInstanceUID)  // ← REMOVE /api
}
```

**Why:** Simpler path, but breaks backward compatibility with existing deployments.

---

### Option C: Fix extractInstanceUID (More Flexible)

**File:** `internal/relay/server_grpc.go:512-520`

**BEFORE:**
```go
// extractInstanceUID extracts instance UID from path like /instances/{uid}/download
func (s *GRPCServer) extractInstanceUID(path string) string {
	// Path format: /instances/{uid}/download
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "instances" {
		return parts[1]
	}
	return ""
}
```

**AFTER:**
```go
// extractInstanceUID extracts instance UID from various path formats
func (s *GRPCServer) extractInstanceUID(path string) string {
	// Supported formats:
	// - /instances/{uid}/download
	// - /api/instances/{uid}/download
	parts := strings.Split(strings.Trim(path, "/"), "/")

	// /api/instances/{uid}/download
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "instances" {
		return parts[2]
	}

	// /instances/{uid}/download
	if len(parts) >= 2 && parts[0] == "instances" {
		return parts[1]
	}

	return ""
}
```

**Why:** Supports both URL formats, maximum backward compatibility.

---

## Recommended Configuration (Option A + C)

### 1. Update Relay Server (`gordion-relay`)

**File:** `internal/relay/server_grpc.go`

```go
// startHTTPServer starts the HTTP server for viewer DICOM requests
func (s *GRPCServer) startHTTPServer(ctx context.Context) error {
	mux := http.NewServeMux()

	// Support both /instances/ and /api/instances/ paths for compatibility
	mux.HandleFunc("/instances/", s.handleInstanceDownload)
	mux.HandleFunc("/api/instances/", s.handleInstanceDownload)  // ← ADD

	mux.HandleFunc("/health", s.handleHealth)

	httpAddr := ":8080"
	if s.config.MetricsAddr != "" {
		httpAddr = s.config.MetricsAddr
	}
	// ... rest of function
}

// extractInstanceUID extracts instance UID from various path formats
func (s *GRPCServer) extractInstanceUID(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")

	// /api/instances/{uid}/download or /api/instances/{uid}
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "instances" {
		return parts[2]
	}

	// /instances/{uid}/download or /instances/{uid}
	if len(parts) >= 2 && parts[0] == "instances" {
		return parts[1]
	}

	return ""
}
```

### 2. Edge Configuration Remains Unchanged

**File:** `C:\ProgramData\GordionEdge\configs\config.json`

```json
{
  "web": {
    "listen_port": "8083",
    "public_base_url": "https://hospital.zenpacs.com.tr"
  },
  "tunnel": {
    "enabled": true,
    "mode": "grpc",
    "grpc_relay_addr": "relay.zenpacs.com.tr:443",
    "use_ssl": true,
    "token": "your-secret-token"
  }
}
```

**Note:** `public_base_url` should be set to the **relay's subdomain**, NOT the edge's local address!

---

## URL Flow Examples

### Example 1: Direct DICOM Instance Download

**Edge generates XML with:**
```xml
<Instance InstanceUID="1.2.3.4.5">
  <URL>https://hospital.zenpacs.com.tr/api/instances/1.2.3.4.5/download?token=xyz</URL>
</Instance>
```

**Weasis viewer requests:**
```
GET https://hospital.zenpacs.com.tr/api/instances/1.2.3.4.5/download?token=xyz
Host: hospital.zenpacs.com.tr
```

**Relay processes:**
1. Extracts subdomain: `hospital`
2. Finds hospital config by subdomain
3. Validates token
4. Extracts instance UID: `1.2.3.4.5`
5. Sends gRPC FetchCommand to edge
6. Streams gRPC DataResponse back as HTTP

**Edge receives gRPC:**
```protobuf
FetchCommand {
  request_id: "123456789"
  type: "instance"
  instance_uid: "1.2.3.4.5"
}
```

**Edge responds with gRPC:**
```protobuf
DataResponse {
  request_id: "123456789"
  start: { instance_uid: "1.2.3.4.5", file_size: 2048576 }
}
DataResponse {
  request_id: "123456789"
  chunk: { data: [bytes...], chunk_index: 0, is_last_chunk: true }
}
DataResponse {
  request_id: "123456789"
  complete: { total_instances: 1, total_bytes: 2048576 }
}
```

---

### Example 2: Study Launch URL

**Weasis launch URL:**
```
weasis://$dicom:get -w "https://hospital.zenpacs.com.tr/studies/1.2.840.113619.2.55.3/manifest.xml?token=xyz"
```

**Manifest XML contains:**
```xml
<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns="http://www.weasis.org/xsd/2.5">
  <arcQuery arcId="1000" baseUrl="https://hospital.zenpacs.com.tr/studies/1.2.840.113619.2.55.3">
    <Patient PatientID="12345">
      <Study StudyInstanceUID="1.2.840.113619.2.55.3">
        <Series SeriesInstanceUID="1.2.840.113619.2.55.3.1">
          <Instance InstanceUID="1.2.840.113619.2.55.3.1.1">
            <URL>/api/instances/1.2.840.113619.2.55.3.1.1/download</URL>
          </Instance>
        </Series>
      </Study>
    </Patient>
  </arcQuery>
</manifest>
```

**Weasis combines `baseUrl` + `URL`:**
```
https://hospital.zenpacs.com.tr/studies/1.2.840.113619.2.55.3/api/instances/1.2.840.113619.2.55.3.1.1/download
```

**⚠️ PROBLEM:** This creates a malformed URL with `/studies/{uid}` in the middle!

---

## Correct Configuration

### Edge Config

**File:** `config.json`

```json
{
  "web": {
    "public_base_url": "https://hospital.zenpacs.com.tr"  ← Relay subdomain
  }
}
```

**Resulting XML:**
```xml
<arcQuery arcId="1000" baseUrl="https://hospital.zenpacs.com.tr/studies/1.2.840.113619.2.55.3">
  ...
  <Instance>
    <URL>/api/instances/1.2.3.4.5/download</URL>
  </Instance>
```

**Actually, let me check the baseUrl logic again...**

Looking at `xml_stage.go:159`:
```go
return fmt.Sprintf("%s/studies/%s", x.publicBaseURL, studyUID)
```

So if `publicBaseURL = "https://hospital.zenpacs.com.tr"`, it generates:
```
baseUrl = "https://hospital.zenpacs.com.tr/studies/1.2.840.113619.2.55.3"
```

And instance path is:
```
/api/instances/1.2.3.4.5/download
```

**The problem:** Weasis uses `baseUrl` as a prefix!

**Solution:** Instance path should be **absolute**, not relative:

```xml
<URL>https://hospital.zenpacs.com.tr/api/instances/1.2.3.4.5/download</URL>
```

---

## Final Fix Required

### Edge XML Generation Fix

**File:** `internal/pipeline/xml_stage.go:302-318`

**CURRENT CODE:**
```go
// When publicBaseURL is set, use API endpoint that supports token authentication
// This allows the hub/API to add tokens before serving to clients
if x.publicBaseURL != "" {
    return fmt.Sprintf("/api/instances/%s/download", inst.SOPInstanceUID)
}
```

**CORRECT CODE:**
```go
// When publicBaseURL is set, use absolute URL for relay
if x.publicBaseURL != "" {
    // Generate absolute URL for relay server
    // Format: https://hospital.zenpacs.com.tr/api/instances/{uid}/download
    return fmt.Sprintf("%s/api/instances/%s/download", x.publicBaseURL, inst.SOPInstanceUID)
}
```

**This ensures:**
- URLs are absolute: `https://hospital.zenpacs.com.tr/api/instances/1.2.3.4.5/download`
- No baseUrl concatenation issues
- Works with both gRPC and WebSocket relay

---

## Summary

**What needs to be changed:**

1. ✅ **Relay:** Add `/api/instances/` handler (or fix extractInstanceUID)
2. ✅ **Edge:** Generate absolute URLs when publicBaseURL is set
3. ✅ **Config:** Set `public_base_url` to relay subdomain

**Result:**
- Weasis gets absolute URLs
- Relay handles both `/instances/` and `/api/instances/` paths
- Works in both gRPC and WebSocket modes
