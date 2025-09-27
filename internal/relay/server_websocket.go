package relay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/acme/autocert"
)

// WebSocketServer is the HTTPS/WebSocket-based relay server
type WebSocketServer struct {
	config   *Config
	logger   *slog.Logger
	server   *http.Server

	// Hospital agent management
	agents      map[string]*WSAgentConnection // hospitalCode -> connection
	agentsMutex sync.RWMutex

	// TLS certificate management
	tlsConfig   *tls.Config
	acmeManager *autocert.Manager

	// Rate limiting for authentication
	failedAttempts map[string]*authAttempts
	attemptsMutex  sync.RWMutex

	// WebSocket upgrader
	upgrader websocket.Upgrader

	// Graceful shutdown
	running  bool
	runMutex sync.RWMutex
}

// authAttempts tracks failed authentication attempts for rate limiting
type authAttempts struct {
	Count        int
	LastAttempt  time.Time
	BlockedUntil time.Time
}

// WSAgentConnection represents a WebSocket connection from a hospital agent
type WSAgentConnection struct {
	HospitalCode string
	Subdomain    string
	Conn         *websocket.Conn
	LastSeen     time.Time
	Mutex        sync.RWMutex
}

// NewWebSocketServer creates a new WebSocket-based relay server
func NewWebSocketServer(config *Config, logger *slog.Logger) *WebSocketServer {
	return &WebSocketServer{
		config:         config,
		logger:         logger,
		agents:         make(map[string]*WSAgentConnection),
		failedAttempts: make(map[string]*authAttempts),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for tunnel connections
			},
			EnableCompression: false,
		},
	}
}

// Start starts the WebSocket relay server
func (s *WebSocketServer) Start(ctx context.Context) error {
	s.runMutex.Lock()
	s.running = true
	s.runMutex.Unlock()

	// Setup TLS configuration
	if err := s.setupTLS(); err != nil {
		return fmt.Errorf("failed to setup TLS: %w", err)
	}

	// Create HTTPS server with WebSocket handler
	mux := http.NewServeMux()
	mux.HandleFunc("/tunnel", s.handleTunnelConnection)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/", s.handleHTTPRequest)

	s.server = &http.Server{
		Addr:      s.config.ListenAddr,
		Handler:   mux,
		TLSConfig: s.tlsConfig,
	}

	// Start server (HTTPS or HTTP depending on TLS config)
	go func() {
		if s.tlsConfig != nil {
			s.logger.Info("HTTPS/WebSocket listener started", "addr", s.config.ListenAddr)
			if err := s.server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTPS server error", "error", err)
			}
		} else {
			s.logger.Info("HTTP/WebSocket listener started (TLS handled by Ingress)", "addr", s.config.ListenAddr)
			if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Error("HTTP server error", "error", err)
			}
		}
	}()

	// Start HTTP redirect server on port 80 for ACME challenges (only if TLS enabled)
	if s.config.TLS.Enabled {
		go s.startHTTPRedirectServer(ctx)
	}

	// Start metrics server if configured
	if s.config.MetricsAddr != "" {
		go s.startMetricsServer(ctx)
	}

	// Start cleanup routine for failed attempts
	go s.cleanupFailedAttempts(ctx)

	return nil
}

// Stop gracefully stops the relay server
func (s *WebSocketServer) Stop() {
	s.runMutex.Lock()
	s.running = false
	s.runMutex.Unlock()

	s.logger.Info("Stopping relay server")

	// Close all agent connections
	s.agentsMutex.Lock()
	for hospitalCode, agent := range s.agents {
		s.logger.Info("Closing agent connection", "hospital", hospitalCode)
		agent.Conn.Close()
	}
	s.agents = make(map[string]*WSAgentConnection)
	s.agentsMutex.Unlock()

	// Shutdown HTTPS server
	if s.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.server.Shutdown(ctx)
	}

	s.logger.Info("Relay server stopped")
}

// setupTLS configures TLS certificates
func (s *WebSocketServer) setupTLS() error {
	// If TLS is disabled (K8s Ingress handles TLS), skip setup
	if !s.config.TLS.Enabled {
		s.logger.Info("TLS disabled - assuming Kubernetes Ingress/LoadBalancer handles TLS termination")
		return nil
	}

	if s.config.TLS.AutoCert {
		// Use Let's Encrypt autocert
		if s.config.TLS.ACMEEmail == "" {
			return fmt.Errorf("acme_email is required when auto_cert is enabled")
		}

		m := &autocert.Manager{
			Cache:  autocert.DirCache("certs"),
			Prompt: autocert.AcceptTOS,
			Email:  s.config.TLS.ACMEEmail,
			HostPolicy: func(ctx context.Context, host string) error {
				// Allow apex domain and any subdomain
				if host == s.config.Domain || strings.HasSuffix(host, "."+s.config.Domain) {
					return nil
				}
				return fmt.Errorf("acme: unauthorized host %q", host)
			},
		}

		s.acmeManager = m
		s.tlsConfig = &tls.Config{
			GetCertificate: m.GetCertificate,
			MinVersion:     tls.VersionTLS12,
		}
	} else {
		// Use provided certificate files
		if s.config.TLS.CertFile == "" || s.config.TLS.KeyFile == "" {
			return fmt.Errorf("cert_file and key_file are required when TLS is enabled but auto_cert is false")
		}

		cert, err := tls.LoadX509KeyPair(s.config.TLS.CertFile, s.config.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}

		s.tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	return nil
}

// startHTTPRedirectServer starts HTTP server on port 80 for ACME and redirects
func (s *WebSocketServer) startHTTPRedirectServer(ctx context.Context) {
	redirectToHTTPS := func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + r.Host + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}

	var httpHandler http.Handler
	if s.acmeManager != nil {
		httpHandler = s.acmeManager.HTTPHandler(http.HandlerFunc(redirectToHTTPS))
	} else {
		httpHandler = http.HandlerFunc(redirectToHTTPS)
	}

	httpServer := &http.Server{
		Addr:    ":80",
		Handler: httpHandler,
	}

	go func() {
		s.logger.Info("Starting HTTP server (ACME/redirect)", "addr", ":80")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	<-ctx.Done()
	s.logger.Info("Shutting down HTTP server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// handleTunnelConnection handles WebSocket tunnel connections from hospitals
func (s *WebSocketServer) handleTunnelConnection(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("Failed to upgrade to WebSocket", "error", err)
		return
	}
	defer conn.Close()

	s.logger.Info("New tunnel connection attempt", "remote", r.RemoteAddr)

	// Read registration message
	_, message, err := conn.ReadMessage()
	if err != nil {
		s.logger.Error("Failed to read registration", "error", err)
		return
	}

	// Parse REGISTER command
	parts := strings.Fields(string(message))
	if len(parts) != 4 || parts[0] != "REGISTER" {
		s.logger.Error("Invalid registration message", "message", string(message))
		conn.WriteMessage(websocket.TextMessage, []byte("ERROR Invalid registration format"))
		return
	}

	hospitalCode := parts[1]
	subdomain := strings.ToLower(parts[2])
	providedToken := parts[3]

	// Check rate limiting
	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	if s.isRateLimited(remoteIP) {
		s.logger.Warn("Rate limited authentication attempt", "remote", remoteIP, "hospital", hospitalCode)
		conn.WriteMessage(websocket.TextMessage, []byte("ERROR Too many failed attempts"))
		return
	}

	// Validate subdomain
	expectedSubdomain := strings.ToLower(hospitalCode + "." + s.config.Domain)
	if subdomain != expectedSubdomain {
		s.logger.Error("Invalid subdomain", "expected", expectedSubdomain, "got", subdomain)
		s.recordFailedAttempt(remoteIP)
		conn.WriteMessage(websocket.TextMessage, []byte("ERROR Invalid subdomain"))
		return
	}

	// Validate token
	expectedToken, ok := s.getHospitalToken(hospitalCode, subdomain)
	if !ok || expectedToken == "" || providedToken != expectedToken {
		s.logger.Error("Invalid token for hospital", "hospital", hospitalCode)
		s.recordFailedAttempt(remoteIP)
		conn.WriteMessage(websocket.TextMessage, []byte("ERROR Invalid token"))
		return
	}

	// Clear failed attempts on successful auth
	s.clearFailedAttempts(remoteIP)

	// Register agent
	agent := &WSAgentConnection{
		HospitalCode: hospitalCode,
		Subdomain:    subdomain,
		Conn:         conn,
		LastSeen:     time.Now(),
	}

	s.agentsMutex.Lock()
	s.agents[hospitalCode] = agent
	s.agentsMutex.Unlock()

	s.logger.Info("Agent registered", "hospital", hospitalCode, "subdomain", subdomain)

	// Send success response
	conn.WriteMessage(websocket.TextMessage, []byte("OK Registered"))

	// Handle messages from agent (heartbeats, etc.)
	s.handleAgentMessages(agent)

	// Clean up on disconnect
	s.agentsMutex.Lock()
	delete(s.agents, hospitalCode)
	s.agentsMutex.Unlock()

	s.logger.Info("Agent disconnected", "hospital", hospitalCode)
}

// handleAgentMessages handles control messages from an agent
func (s *WebSocketServer) handleAgentMessages(agent *WSAgentConnection) {
	for {
		_, message, err := agent.Conn.ReadMessage()
		if err != nil {
			s.logger.Debug("Agent connection closed", "hospital", agent.HospitalCode, "error", err)
			return
		}

		msg := strings.TrimSpace(string(message))
		if msg == "HEARTBEAT" {
			agent.Mutex.Lock()
			agent.LastSeen = time.Now()
			agent.Mutex.Unlock()
			s.logger.Debug("Heartbeat received", "hospital", agent.HospitalCode)
		}
	}
}

// handleHTTPRequest handles incoming HTTP/HTTPS requests and forwards through tunnel
func (s *WebSocketServer) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("Received HTTP request", "method", r.Method, "path", r.URL.Path, "host", r.Host)

	// Extract hospital code from subdomain
	hospitalCode := s.extractHospitalCode(r.Host)
	if hospitalCode == "" {
		s.logger.Warn("No hospital code found in request", "host", r.Host)
		http.Error(w, "Invalid subdomain", http.StatusBadRequest)
		return
	}

	// Find agent connection
	s.agentsMutex.RLock()
	agent, exists := s.agents[hospitalCode]
	s.agentsMutex.RUnlock()

	if !exists {
		s.logger.Warn("No agent found for hospital", "hospital", hospitalCode, "host", r.Host)
		http.Error(w, "Hospital not connected", http.StatusServiceUnavailable)
		return
	}

	// Forward request through tunnel
	s.logger.Debug("Forwarding request to agent", "hospital", hospitalCode, "method", r.Method, "path", r.URL.Path)
	if err := s.forwardRequest(w, r, agent); err != nil {
		s.logger.Error("Failed to forward request", "error", err, "hospital", hospitalCode)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	s.logger.Debug("Successfully forwarded request", "hospital", hospitalCode)
}

// extractHospitalCode extracts hospital code from subdomain
func (s *WebSocketServer) extractHospitalCode(host string) string {
	// Normalize to lowercase for case-insensitive host matching
	host = strings.ToLower(host)

	// Remove port if present
	if colonIndex := strings.Index(host, ":"); colonIndex != -1 {
		host = host[:colonIndex]
	}

	// Check if it's a subdomain of our domain
	domainSuffix := "." + strings.ToLower(s.config.Domain)
	if !strings.HasSuffix(host, domainSuffix) {
		return ""
	}

	// The subdomain is the hospital code
	return strings.TrimSuffix(host, domainSuffix)
}

// forwardRequest forwards an HTTP request through the WebSocket tunnel
func (s *WebSocketServer) forwardRequest(w http.ResponseWriter, r *http.Request, agent *WSAgentConnection) error {
	s.logger.Debug("Starting request forwarding")

	agent.Mutex.RLock()
	conn := agent.Conn
	agent.Mutex.RUnlock()

	deadline := time.Now().Add(time.Duration(s.config.RequestTimeout))
	conn.SetReadDeadline(deadline)
	conn.SetWriteDeadline(deadline)

	s.logger.Debug("Set WebSocket deadlines", "timeout", s.config.RequestTimeout)

	// Serialize HTTP request
	var reqBuf strings.Builder
	reqBuf.WriteString(fmt.Sprintf("%s %s %s\r\n", r.Method, r.RequestURI, r.Proto))
	for key, values := range r.Header {
		for _, value := range values {
			reqBuf.WriteString(fmt.Sprintf("%s: %s\r\n", key, value))
		}
	}
	reqBuf.WriteString("\r\n")

	// Write request to WebSocket
	s.logger.Debug("Sending HTTP request to agent", "request_size", len(reqBuf.String()))
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(reqBuf.String())); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}
	s.logger.Debug("Successfully sent HTTP request to agent")

	// Copy body if present
	if r.Body != nil {
		bodyData, err := io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("failed to read body: %w", err)
		}
		if len(bodyData) > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, bodyData); err != nil {
				return fmt.Errorf("failed to write body: %w", err)
			}
		}
	}

	// Read response headers (first message)
	s.logger.Debug("Waiting for response headers from agent")
	_, respData, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("failed to read response headers: %w", err)
	}
	s.logger.Debug("Received response headers from agent", "response_size", len(respData))

	// Parse HTTP response headers
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(respData)), r)
	if err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Copy response headers to client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream body chunks to client
	for {
		_, chunk, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("failed to read chunk: %w", err)
		}

		// Empty message signals end
		if len(chunk) == 0 {
			break
		}

		// Write chunk to client
		if _, err := w.Write(chunk); err != nil {
			return fmt.Errorf("failed to write chunk to client: %w", err)
		}

		// Flush to ensure progressive download
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	return nil
}

func (s *WebSocketServer) getHospitalToken(code, subdomain string) (string, bool) {
	subdomain = strings.ToLower(subdomain)
	for _, h := range s.config.Hospitals {
		if h.Code == code && strings.ToLower(h.Subdomain) == subdomain {
			return h.Token, true
		}
	}
	return "", false
}

// handleStatus returns current relay status (shared by main and metrics server)
func (s *WebSocketServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.agentsMutex.RLock()
	defer s.agentsMutex.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{
		"connected_hospitals": %d,
		"hospitals": [`, len(s.agents))

	first := true
	for hospitalCode, agent := range s.agents {
		if !first {
			fmt.Fprintf(w, ",")
		}
		fmt.Fprintf(w, `{
			"code": "%s",
			"subdomain": "%s",
			"last_seen": "%s"
		}`, hospitalCode, agent.Subdomain, agent.LastSeen.Format(time.RFC3339))
		first = false
	}

	fmt.Fprintf(w, `]
	}`)
}

// startMetricsServer starts a metrics/status server
func (s *WebSocketServer) startMetricsServer(ctx context.Context) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	mux.HandleFunc("/status", s.handleStatus)

	server := &http.Server{
		Addr:    s.config.MetricsAddr,
		Handler: mux,
	}

	s.logger.Info("Starting metrics server", "addr", s.config.MetricsAddr)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Metrics server error", "error", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(shutdownCtx)
}

// Rate limiting functions (same as before)

func (s *WebSocketServer) isRateLimited(remoteAddr string) bool {
	// Read-only check; mutations happen in recordFailedAttempt
	s.attemptsMutex.RLock()
	attempts, exists := s.failedAttempts[remoteAddr]
	s.attemptsMutex.RUnlock()
	if !exists {
		return false
	}
	return time.Now().Before(attempts.BlockedUntil)
}

func (s *WebSocketServer) recordFailedAttempt(remoteAddr string) {
	s.attemptsMutex.Lock()
	defer s.attemptsMutex.Unlock()

	attempts, exists := s.failedAttempts[remoteAddr]
	if !exists {
		attempts = &authAttempts{}
		s.failedAttempts[remoteAddr] = attempts
	}

	attempts.Count++
	attempts.LastAttempt = time.Now()

	const maxAttempts = 5
	const blockDuration = 15 * time.Minute

	if attempts.Count >= maxAttempts {
		attempts.BlockedUntil = time.Now().Add(blockDuration)
		s.logger.Warn("IP blocked due to too many failed attempts",
			"remote", remoteAddr,
			"attempts", attempts.Count,
			"blocked_until", attempts.BlockedUntil)
	}
}

func (s *WebSocketServer) clearFailedAttempts(remoteAddr string) {
	s.attemptsMutex.Lock()
	defer s.attemptsMutex.Unlock()

	delete(s.failedAttempts, remoteAddr)
}

func (s *WebSocketServer) cleanupFailedAttempts(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.attemptsMutex.Lock()
			now := time.Now()
			for addr, attempts := range s.failedAttempts {
				if now.Sub(attempts.LastAttempt) > 24*time.Hour {
					delete(s.failedAttempts, addr)
				}
			}
			s.attemptsMutex.Unlock()
		}
	}
}