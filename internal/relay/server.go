package relay

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"golang.org/x/crypto/acme/autocert"
)

// Server is the main relay server
type Server struct {
	config   *Config
	logger   *slog.Logger
	listener *quic.Listener

	// Hospital agent management
	agents map[string]*AgentConnection // hospitalCode -> connection
	agentsMutex sync.RWMutex

	// TLS certificate management
	tlsConfig   *tls.Config
	acmeManager *autocert.Manager

	// Rate limiting for authentication
	failedAttempts map[string]*authAttempts
	attemptsMutex  sync.RWMutex

	// Graceful shutdown
	running bool
	runMutex sync.RWMutex
}

// authAttempts tracks failed authentication attempts
type authAttempts struct {
	Count      int
	LastAttempt time.Time
	BlockedUntil time.Time
}

// AgentConnection represents a connection from a hospital agent
type AgentConnection struct {
	HospitalCode string
	Subdomain    string
	Connection   quic.Connection
	LastSeen     time.Time
	Mutex        sync.RWMutex
}

// NewServer creates a new relay server
func NewServer(config *Config, logger *slog.Logger) *Server {
	return &Server{
		config:         config,
		logger:         logger,
		agents:         make(map[string]*AgentConnection),
		failedAttempts: make(map[string]*authAttempts),
	}
}

// Start starts the relay server
func (s *Server) Start(ctx context.Context) error {
	s.runMutex.Lock()
	s.running = true
	s.runMutex.Unlock()

	// Setup TLS configuration
	if err := s.setupTLS(); err != nil {
		return fmt.Errorf("failed to setup TLS: %w", err)
	}

	// Start QUIC listener
	listener, err := quic.ListenAddr(s.config.ListenAddr, s.tlsConfig, &quic.Config{
		MaxIdleTimeout:  s.config.IdleTimeout.ToDuration(),
		KeepAlivePeriod: s.config.IdleTimeout.ToDuration() / 2,
	})
	if err != nil {
		return fmt.Errorf("failed to start QUIC listener: %w", err)
	}

	s.listener = listener
	s.logger.Info("QUIC listener started", "addr", s.config.ListenAddr)

	// Start HTTP redirect server on port 80 for ACME challenges
	go s.startHTTPRedirectServer(ctx)

	// Accept all QUIC connections (both tunnels and HTTP/3 requests)
	go s.acceptConnections(ctx)

	// Start metrics server if configured
	if s.config.MetricsAddr != "" {
		go s.startMetricsServer(ctx)
	}

	// Start cleanup routine for failed attempts
	go s.cleanupFailedAttempts(ctx)

	return nil
}

// Stop gracefully stops the relay server
func (s *Server) Stop() {
	s.runMutex.Lock()
	s.running = false
	s.runMutex.Unlock()

	s.logger.Info("Stopping relay server")

	// Close all agent connections
	s.agentsMutex.Lock()
	for hospitalCode, agent := range s.agents {
		s.logger.Info("Closing agent connection", "hospital", hospitalCode)
		agent.Connection.CloseWithError(0, "server shutting down")
	}
	s.agents = make(map[string]*AgentConnection)
	s.agentsMutex.Unlock()

	// Close QUIC listener
	if s.listener != nil {
		s.listener.Close()
	}

	s.logger.Info("Relay server stopped")
}

// setupTLS configures TLS certificates
func (s *Server) setupTLS() error {
	if s.config.TLS.AutoCert {
		// Use Let's Encrypt autocert with a host policy that allows the apex domain and all subdomains
		m := &autocert.Manager{
			Cache:  autocert.DirCache("certs"),
			Prompt: autocert.AcceptTOS,
			HostPolicy: func(ctx context.Context, host string) error {
				// Allow apex domain and any subdomain under configured domain
				if host == s.config.Domain || strings.HasSuffix(host, "."+s.config.Domain) {
					return nil
				}
				return fmt.Errorf("acme: unauthorized host %q", host)
			},
		}

		s.acmeManager = m
		s.tlsConfig = &tls.Config{
			GetCertificate: m.GetCertificate,
			NextProtos:     []string{"tunnel-v1", "h2", "http/1.1"},
		}
	} else {
		// Use provided certificate files
		cert, err := tls.LoadX509KeyPair(s.config.TLS.CertFile, s.config.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS certificate: %w", err)
		}

		s.tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"tunnel-v1", "h2", "http/1.1"},
		}
	}

	return nil
}

// startHTTPRedirectServer starts HTTP server on port 80 for ACME and redirects
func (s *Server) startHTTPRedirectServer(ctx context.Context) {
	// Helper to redirect to HTTPS
	redirectToHTTPS := func(w http.ResponseWriter, r *http.Request) {
		target := "https://" + r.Host + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	}

	// HTTP server on :80 — serve ACME challenges when autocert is enabled, otherwise just redirect
	var httpHandler http.Handler
	if s.acmeManager != nil {
		// Serve HTTP-01 challenges via autocert; non-challenge paths will hit the fallback redirect handler
		httpHandler = s.acmeManager.HTTPHandler(http.HandlerFunc(redirectToHTTPS))
	} else {
		httpHandler = http.HandlerFunc(redirectToHTTPS)
	}

	httpServer := &http.Server{
		Addr:    ":80",
		Handler: httpHandler,
	}

	// Start HTTP server
	go func() {
		s.logger.Info("Starting HTTP server (ACME/redirect)", "addr", ":80")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("HTTP server error", "error", err)
		}
	}()

	// Graceful shutdown
	<-ctx.Done()
	s.logger.Info("Shutting down HTTP server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// handleHTTPRequest handles incoming HTTP/HTTPS requests
func (s *Server) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
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
	if err := s.forwardRequest(w, r, agent); err != nil {
		s.logger.Error("Failed to forward request", "error", err, "hospital", hospitalCode)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// extractHospitalCode extracts hospital code from subdomain
func (s *Server) extractHospitalCode(host string) string {
	// Remove port if present
	if colonIndex := strings.Index(host, ":"); colonIndex != -1 {
		host = host[:colonIndex]
	}

	// Check if it's a subdomain of our domain
	domainSuffix := "." + s.config.Domain
	if !strings.HasSuffix(host, domainSuffix) {
		return ""
	}

	// Extract subdomain
	subdomain := strings.TrimSuffix(host, domainSuffix)

	// The subdomain is the hospital code
	return subdomain
}

// forwardRequest forwards an HTTP request through the tunnel
func (s *Server) forwardRequest(w http.ResponseWriter, r *http.Request, agent *AgentConnection) error {
	agent.Mutex.RLock()
	conn := agent.Connection
	agent.Mutex.RUnlock()

	// Open a new stream for this request
	stream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		return fmt.Errorf("failed to open stream: %w", err)
	}
	defer stream.Close()

	// Write the HTTP request to the stream
	if err := r.Write(stream); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	// Read the response
	resp, err := http.ReadResponse(bufio.NewReader(stream), r)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body
	_, err = io.Copy(w, resp.Body)
	return err
}

// acceptConnections accepts all QUIC connections (tunnels and HTTP requests)
func (s *Server) acceptConnections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.runMutex.RLock()
		running := s.running
		s.runMutex.RUnlock()

		if !running {
			return
		}

		conn, err := s.listener.Accept(ctx)
		if err != nil {
			s.logger.Error("Failed to accept connection", "error", err)
			continue
		}

		// Handle connection - could be tunnel registration or direct HTTP request
		go s.handleConnection(ctx, conn)
	}
}

// handleConnection handles a new QUIC connection (tunnel or HTTP)
func (s *Server) handleConnection(ctx context.Context, conn quic.Connection) {
	// Accept the first stream to determine connection type
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		s.logger.Error("Failed to accept first stream", "error", err)
		conn.CloseWithError(1, "failed to accept stream")
		return
	}

	// Peek at the first few bytes to determine if it's HTTP or tunnel registration
	reader := bufio.NewReader(stream)
	firstLine, err := reader.ReadString('\n')
	if err != nil {
		s.logger.Error("Failed to read first line", "error", err)
		stream.Close()
		conn.CloseWithError(1, "failed to read first line")
		return
	}

	// Check if it's a REGISTER command (tunnel) or HTTP request
	if strings.HasPrefix(firstLine, "REGISTER ") {
		// It's a tunnel registration
		s.handleTunnelRegistration(ctx, conn, stream, firstLine)
	} else if strings.HasPrefix(firstLine, "GET ") || strings.HasPrefix(firstLine, "POST ") ||
		strings.HasPrefix(firstLine, "PUT ") || strings.HasPrefix(firstLine, "DELETE ") ||
		strings.HasPrefix(firstLine, "HEAD ") || strings.HasPrefix(firstLine, "OPTIONS ") {
		// It's an HTTP request
		s.handleDirectHTTPRequest(ctx, conn, stream, firstLine, reader)
	} else {
		s.logger.Warn("Unknown connection type", "first_line", firstLine)
		stream.Close()
		conn.CloseWithError(1, "unknown protocol")
	}
}

// handleTunnelRegistration handles tunnel registration from hospital
func (s *Server) handleTunnelRegistration(ctx context.Context, conn quic.Connection, stream quic.Stream, regLine string) {
	defer conn.CloseWithError(0, "tunnel connection handler finished")

	s.logger.Info("New tunnel registration attempt", "remote", conn.RemoteAddr())

	// Parse registration message (already read from stream)
	parts := strings.Fields(strings.TrimSpace(regLine))
if len(parts) != 4 || parts[0] != "REGISTER" {
	s.logger.Error("Invalid registration message", "message", strings.TrimSpace(regLine))
	stream.Write([]byte("ERROR Invalid registration format\n"))
	stream.Close()
	return
}

	hospitalCode := parts[1]
	subdomain := strings.ToLower(parts[2]) // Case-insensitive subdomain
	providedToken := parts[3]

	// Check rate limiting
	remoteIP := conn.RemoteAddr().String()
	if s.isRateLimited(remoteIP) {
		s.logger.Warn("Rate limited authentication attempt", "remote", remoteIP, "hospital", hospitalCode)
		stream.Write([]byte("ERROR Too many failed attempts, please try again later\n"))
		stream.Close()
		return
	}

	// Validate subdomain
	expectedSubdomain := strings.ToLower(hospitalCode + "." + s.config.Domain)
	if subdomain != expectedSubdomain {
		s.logger.Error("Invalid subdomain", "expected", expectedSubdomain, "got", subdomain)
		s.recordFailedAttempt(remoteIP)
		stream.Write([]byte("ERROR Invalid subdomain\n"))
		stream.Close()
		return
	}

	// Validate token from config
	expectedToken, ok := s.getHospitalToken(hospitalCode, subdomain)
	if !ok || expectedToken == "" {
		s.logger.Error("Hospital not configured or token missing", "hospital", hospitalCode, "subdomain", subdomain)
		s.recordFailedAttempt(remoteIP)
		stream.Write([]byte("ERROR Hospital not configured or token missing\n"))
		stream.Close()
		return
	}
	if providedToken != expectedToken {
		s.logger.Error("Invalid token for hospital", "hospital", hospitalCode, "subdomain", subdomain)
		s.recordFailedAttempt(remoteIP)
		stream.Write([]byte("ERROR Invalid token\n"))
		stream.Close()
		return
	}

	// Clear failed attempts on successful auth
	s.clearFailedAttempts(remoteIP)

// Register agent
agent := &AgentConnection{
	HospitalCode: hospitalCode,
	Subdomain:    subdomain,
	Connection:   conn,
	LastSeen:     time.Now(),
}

s.agentsMutex.Lock()
s.agents[hospitalCode] = agent
s.agentsMutex.Unlock()

s.logger.Info("Agent registered", "hospital", hospitalCode, "subdomain", subdomain)

// Send success response
stream.Write([]byte("OK Registered\n"))
stream.Close()

// Handle heartbeats and other control messages
s.handleAgentMessages(ctx, agent)

// Clean up on disconnect
s.agentsMutex.Lock()
delete(s.agents, hospitalCode)
s.agentsMutex.Unlock()

s.logger.Info("Agent disconnected", "hospital", hospitalCode)
}

func (s *Server) getHospitalToken(code, subdomain string) (string, bool) {
	subdomain = strings.ToLower(subdomain) // Case-insensitive
	for _, h := range s.config.Hospitals {
		if h.Code == code && strings.ToLower(h.Subdomain) == subdomain {
			return h.Token, true
		}
	}
	return "", false
}

// handleAgentMessages handles control messages from an agent
func (s *Server) handleAgentMessages(ctx context.Context, agent *AgentConnection) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		stream, err := agent.Connection.AcceptStream(ctx)
		if err != nil {
			s.logger.Debug("Agent connection closed", "hospital", agent.HospitalCode, "error", err)
			return
		}

		go func(stream quic.Stream) {
			defer stream.Close()

			reader := bufio.NewReader(stream)
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			message := strings.TrimSpace(line)
			if message == "HEARTBEAT" {
				agent.Mutex.Lock()
				agent.LastSeen = time.Now()
				agent.Mutex.Unlock()
				s.logger.Debug("Heartbeat received", "hospital", agent.HospitalCode)
			}
		}(stream)
	}
}

// startMetricsServer starts a metrics/status server
func (s *Server) startMetricsServer(ctx context.Context) {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
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
				"last_seen": "%s",
				"remote_addr": "%s"
			}`, hospitalCode, agent.Subdomain, agent.LastSeen.Format(time.RFC3339), agent.Connection.RemoteAddr())
			first = false
		}

		fmt.Fprintf(w, `]
		}`)
	})

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

// handleDirectHTTPRequest handles direct HTTP requests over QUIC
func (s *Server) handleDirectHTTPRequest(ctx context.Context, conn quic.Connection, stream quic.Stream, firstLine string, reader *bufio.Reader) {
	defer stream.Close()
	defer conn.CloseWithError(0, "HTTP request completed")

	// Reconstruct the HTTP request
	// We already have the first line (e.g., "GET /path HTTP/1.1")
	// Now read the rest of the headers
	req, err := http.ReadRequest(reader)
	if err != nil {
		s.logger.Error("Failed to read HTTP request", "error", err)
		stream.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	// Extract hospital code from Host header
	hospitalCode := s.extractHospitalCode(req.Host)
	if hospitalCode == "" {
		s.logger.Warn("No hospital code found in request", "host", req.Host)
		stream.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Type: text/plain\r\n\r\nInvalid subdomain\n"))
		return
	}

	// Find agent connection
	s.agentsMutex.RLock()
	agent, exists := s.agents[hospitalCode]
	s.agentsMutex.RUnlock()

	if !exists {
		s.logger.Warn("No agent found for hospital", "hospital", hospitalCode, "host", req.Host)
		stream.Write([]byte("HTTP/1.1 503 Service Unavailable\r\nContent-Type: text/plain\r\n\r\nHospital not connected\n"))
		return
	}

	// Forward request through tunnel
	if err := s.forwardRequestToTunnel(stream, req, agent); err != nil {
		s.logger.Error("Failed to forward request", "error", err, "hospital", hospitalCode)
		stream.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Type: text/plain\r\n\r\nInternal server error\n"))
		return
	}
}

// forwardRequestToTunnel forwards request to tunnel agent
func (s *Server) forwardRequestToTunnel(responseStream quic.Stream, req *http.Request, agent *AgentConnection) error {
	agent.Mutex.RLock()
	conn := agent.Connection
	agent.Mutex.RUnlock()

	// Open a new stream for this request
	tunnelStream, err := conn.OpenStreamSync(context.Background())
	if err != nil {
		return fmt.Errorf("failed to open tunnel stream: %w", err)
	}
	defer tunnelStream.Close()

	// Write the HTTP request to the tunnel
	if err := req.Write(tunnelStream); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	// Read the response
	resp, err := http.ReadResponse(bufio.NewReader(tunnelStream), req)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	defer resp.Body.Close()

	// Write response back to original stream
	// Write status line
	statusLine := fmt.Sprintf("HTTP/1.1 %d %s\r\n", resp.StatusCode, resp.Status[4:])
	if _, err := responseStream.Write([]byte(statusLine)); err != nil {
		return err
	}

	// Write headers
	for key, values := range resp.Header {
		for _, value := range values {
			headerLine := fmt.Sprintf("%s: %s\r\n", key, value)
			if _, err := responseStream.Write([]byte(headerLine)); err != nil {
				return err
			}
		}
	}

	// End headers
	if _, err := responseStream.Write([]byte("\r\n")); err != nil {
		return err
	}

	// Write body
	_, err = io.Copy(responseStream, resp.Body)
	return err
}

// Rate limiting functions

// isRateLimited checks if an IP is currently rate limited
func (s *Server) isRateLimited(remoteAddr string) bool {
	s.attemptsMutex.RLock()
	defer s.attemptsMutex.RUnlock()

	attempts, exists := s.failedAttempts[remoteAddr]
	if !exists {
		return false
	}

	// Check if currently blocked
	if time.Now().Before(attempts.BlockedUntil) {
		return true
	}

	// Block if too many attempts
	const maxAttempts = 5
	const blockDuration = 15 * time.Minute

	if attempts.Count >= maxAttempts {
		// Extend block time
		attempts.BlockedUntil = time.Now().Add(blockDuration)
		return true
	}

	return false
}

// recordFailedAttempt records a failed authentication attempt
func (s *Server) recordFailedAttempt(remoteAddr string) {
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

// clearFailedAttempts clears failed attempts for an IP
func (s *Server) clearFailedAttempts(remoteAddr string) {
	s.attemptsMutex.Lock()
	defer s.attemptsMutex.Unlock()

	delete(s.failedAttempts, remoteAddr)
}

// cleanupFailedAttempts periodically cleans up old failed attempt records
func (s *Server) cleanupFailedAttempts(ctx context.Context) {
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
				// Remove entries older than 24 hours
				if now.Sub(attempts.LastAttempt) > 24*time.Hour {
					delete(s.failedAttempts, addr)
				}
			}
			s.attemptsMutex.Unlock()
		}
	}
}