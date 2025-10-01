package relay

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/minasoft-technology/gordion-relay/internal/relay/grpc"
	"github.com/minasoft-technology/gordion-relay/internal/security/timetoken"
	grpclib "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
)

const (
	MaxMessageSize = 16 * 1024 * 1024 // 16MB for large DICOMs
)

// GRPCServer manages gRPC tunnel connections from multiple edge servers
type GRPCServer struct {
	grpc.UnimplementedTunnelServiceServer

	config *Config
	logger *slog.Logger

	// Edge connections by hospital ID
	edges   map[string]*EdgeConnection // hospitalID -> connection
	edgesMu sync.RWMutex

	// HTTP server for viewer requests
	httpServer *http.Server
	grpcServer *grpclib.Server
}

// EdgeConnection represents one connected edge server
type EdgeConnection struct {
	HospitalID   string
	EdgeServerID string
	Stream       grpc.TunnelService_StreamServer
	Connected    time.Time
	LastSeen     time.Time
	mu           sync.RWMutex

	// Pending fetch requests
	pendingRequests map[string]*PendingRequest
	pendingMu       sync.RWMutex
}

// PendingRequest tracks in-flight fetch requests
type PendingRequest struct {
	RequestID    string
	StartTime    time.Time
	ResponseChan chan *grpc.DataResponse
	ErrorChan    chan error
}

// NewGRPCServer creates a new gRPC relay server
func NewGRPCServer(cfg *Config, logger *slog.Logger) *GRPCServer {
	return &GRPCServer{
		config: cfg,
		logger: logger,
		edges:  make(map[string]*EdgeConnection),
	}
}

// Start initializes and starts both gRPC and HTTP servers
func (s *GRPCServer) Start(ctx context.Context) error {
	// Start gRPC server for edge connections
	go func() {
		if err := s.startGRPCServer(); err != nil {
			s.logger.Error("gRPC server failed", "error", err)
		}
	}()

	// Start HTTP server for viewer requests
	return s.startHTTPServer(ctx)
}

// startGRPCServer starts the gRPC server for edge connections
func (s *GRPCServer) startGRPCServer() error {
	listenAddr := s.config.ListenAddr
	if listenAddr == "" {
		listenAddr = ":443"
	}

	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}

	// gRPC server options
	var opts []grpclib.ServerOption

	// TLS credentials (required for production)
	if s.config.TLS.Enabled {
		if s.config.TLS.CertFile == "" || s.config.TLS.KeyFile == "" {
			return fmt.Errorf("TLS enabled but cert/key files not specified")
		}
		creds, err := credentials.NewServerTLSFromFile(s.config.TLS.CertFile, s.config.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		opts = append(opts, grpclib.Creds(creds))
		s.logger.Info("gRPC server using TLS", "cert", s.config.TLS.CertFile)
	} else {
		s.logger.Warn("gRPC server running without TLS (not recommended for production)")
	}

	// Keep-alive enforcement (aggressive for firewall traversal)
	kaep := keepalive.EnforcementPolicy{
		MinTime:             5 * time.Second, // Allow pings every 5s
		PermitWithoutStream: true,            // Allow pings without streams
	}
	kasp := keepalive.ServerParameters{
		Time:    10 * time.Second, // Send pings every 10s if no activity
		Timeout: 5 * time.Second,  // Wait 5s for ping ack
	}
	opts = append(opts, grpclib.KeepaliveEnforcementPolicy(kaep), grpclib.KeepaliveParams(kasp))

	// Message size limits
	opts = append(opts,
		grpclib.MaxRecvMsgSize(MaxMessageSize),
		grpclib.MaxSendMsgSize(MaxMessageSize),
	)

	// Create and register server
	s.grpcServer = grpclib.NewServer(opts...)
	grpc.RegisterTunnelServiceServer(s.grpcServer, s)

	s.logger.Info("Starting gRPC relay server", "addr", listenAddr)
	return s.grpcServer.Serve(lis)
}

// Stream implements the bidirectional streaming RPC
func (s *GRPCServer) Stream(stream grpc.TunnelService_StreamServer) error {
	// First message must be registration
	msg, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive registration: %w", err)
	}

	reg := msg.GetRegister()
	if reg == nil {
		return fmt.Errorf("first message must be registration")
	}

	s.logger.Info("Registration request received",
		"hospital_id", reg.HospitalId,
		"edge_server_id", reg.EdgeServerId,
		"version", reg.Version)

	// Find hospital configuration
	hospital := s.findHospitalByID(reg.HospitalId)
	if hospital == nil {
		s.logger.Warn("Unknown hospital ID", "hospital_id", reg.HospitalId)
		stream.Send(&grpc.RelayMessage{
			Message: &grpc.RelayMessage_RegisterAck{
				RegisterAck: &grpc.RegisterResponse{
					Success: false,
					Message: fmt.Sprintf("unknown hospital: %s", reg.HospitalId),
				},
			},
		})
		return fmt.Errorf("unknown hospital: %s", reg.HospitalId)
	}

	// Validate token
	if reg.Token != hospital.Token {
		s.logger.Warn("Invalid token", "hospital_id", reg.HospitalId)
		stream.Send(&grpc.RelayMessage{
			Message: &grpc.RelayMessage_RegisterAck{
				RegisterAck: &grpc.RegisterResponse{
					Success: false,
					Message: "invalid authentication token",
				},
			},
		})
		return fmt.Errorf("invalid token for hospital: %s", reg.HospitalId)
	}

	// Register edge connection
	edgeConn := &EdgeConnection{
		HospitalID:      reg.HospitalId,
		EdgeServerID:    reg.EdgeServerId,
		Stream:          stream,
		Connected:       time.Now(),
		LastSeen:        time.Now(),
		pendingRequests: make(map[string]*PendingRequest),
	}

	s.edgesMu.Lock()
	s.edges[reg.HospitalId] = edgeConn
	s.edgesMu.Unlock()

	s.logger.Info("✅ Edge registered",
		"hospital_id", reg.HospitalId,
		"edge_server_id", reg.EdgeServerId,
		"version", reg.Version)

	// Send acknowledgment
	err = stream.Send(&grpc.RelayMessage{
		Message: &grpc.RelayMessage_RegisterAck{
			RegisterAck: &grpc.RegisterResponse{
				Success:    true,
				Message:    "registered successfully",
				ServerTime: time.Now().Unix(),
			},
		},
	})
	if err != nil {
		s.edgesMu.Lock()
		delete(s.edges, reg.HospitalId)
		s.edgesMu.Unlock()
		return err
	}

	// Handle incoming messages from edge
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				s.logger.Info("Edge disconnected", "hospital_id", reg.HospitalId)
			} else {
				s.logger.Error("Receive error", "hospital_id", reg.HospitalId, "error", err)
			}
			break
		}

		edgeConn.mu.Lock()
		edgeConn.LastSeen = time.Now()
		edgeConn.mu.Unlock()

		switch m := msg.Message.(type) {
		case *grpc.EdgeMessage_Data:
			go edgeConn.handleDataResponse(m.Data)
		case *grpc.EdgeMessage_Keepalive:
			s.logger.Debug("Received keep-alive", "hospital_id", reg.HospitalId, "seq", m.Keepalive.Sequence)
		case *grpc.EdgeMessage_Status:
			s.logger.Debug("Status update", "hospital_id", reg.HospitalId, "healthy", m.Status.Healthy)
		}
	}

	// Unregister on disconnect
	s.edgesMu.Lock()
	delete(s.edges, reg.HospitalId)
	s.edgesMu.Unlock()

	s.logger.Info("Edge connection closed", "hospital_id", reg.HospitalId)
	return nil
}

// handleDataResponse routes data responses to waiting requests
func (ec *EdgeConnection) handleDataResponse(data *grpc.DataResponse) {
	ec.pendingMu.RLock()
	req, exists := ec.pendingRequests[data.RequestId]
	ec.pendingMu.RUnlock()

	if !exists {
		return // Request expired or cancelled
	}

	// Check for error
	if err := data.GetError(); err != nil {
		req.ErrorChan <- fmt.Errorf("%s: %s", err.ErrorCode, err.ErrorMessage)
		return
	}

	// Check for completion
	if complete := data.GetComplete(); complete != nil {
		close(req.ResponseChan) // Signal completion
		ec.pendingMu.Lock()
		delete(ec.pendingRequests, data.RequestId)
		ec.pendingMu.Unlock()
		return
	}

	// Send data to response channel
	req.ResponseChan <- data
}

// findHospitalByID finds hospital config by hospital ID (case-insensitive)
func (s *GRPCServer) findHospitalByID(hospitalID string) *HospitalConfig {
	hospitalID = strings.ToUpper(hospitalID)
	for i := range s.config.Hospitals {
		if strings.ToUpper(s.config.Hospitals[i].HospitalID) == hospitalID {
			return &s.config.Hospitals[i]
		}
	}
	return nil
}

// findHospitalBySubdomain finds hospital config by subdomain
func (s *GRPCServer) findHospitalBySubdomain(subdomain string) *HospitalConfig {
	subdomain = strings.ToLower(subdomain)
	for i := range s.config.Hospitals {
		if strings.ToLower(s.config.Hospitals[i].Code) == subdomain {
			return &s.config.Hospitals[i]
		}
	}
	return nil
}

// startHTTPServer starts the HTTP server for viewer DICOM requests
func (s *GRPCServer) startHTTPServer(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/instances/", s.handleInstanceDownload)
	mux.HandleFunc("/health", s.handleHealth)

	httpAddr := ":8080" // HTTP on different port (Ingress handles TLS)
	if s.config.MetricsAddr != "" {
		httpAddr = s.config.MetricsAddr
	}

	s.httpServer = &http.Server{
		Addr:    httpAddr,
		Handler: mux,
	}

	s.logger.Info("Starting HTTP server for viewer requests", "addr", httpAddr)
	return s.httpServer.ListenAndServe()
}

// handleInstanceDownload handles DICOM instance download requests from viewers
func (s *GRPCServer) handleInstanceDownload(w http.ResponseWriter, r *http.Request) {
	// Extract subdomain from Host header
	host := r.Host
	subdomain := s.extractSubdomain(host)
	if subdomain == "" {
		http.Error(w, "Invalid subdomain", http.StatusBadRequest)
		return
	}

	// Find hospital by subdomain
	hospital := s.findHospitalBySubdomain(subdomain)
	if hospital == nil {
		s.logger.Warn("Unknown hospital subdomain", "subdomain", subdomain)
		http.Error(w, "Unknown hospital", http.StatusNotFound)
		return
	}

	// Validate download token using hospital's API key
	token := r.URL.Query().Get("token")
	if token == "" {
		s.logger.Warn("Missing token", "path", r.URL.Path, "subdomain", subdomain)
		http.Error(w, "Missing token parameter", http.StatusUnauthorized)
		return
	}

	if err := timetoken.ValidateToken(hospital.Token, token, r.URL.Path); err != nil {
		s.logger.Warn("Token validation failed",
			"error", err,
			"path", r.URL.Path,
			"subdomain", subdomain)
		http.Error(w, "Invalid or expired token", http.StatusUnauthorized)
		return
	}

	s.logger.Debug("Token validated successfully", "path", r.URL.Path, "subdomain", subdomain)

	// Extract instance UID from path
	instanceUID := s.extractInstanceUID(r.URL.Path)
	if instanceUID == "" {
		http.Error(w, "Invalid instance path", http.StatusBadRequest)
		return
	}

	// Fetch instance from edge via gRPC
	reader, err := s.fetchInstanceFromEdge(r.Context(), hospital.HospitalID, instanceUID)
	if err != nil {
		s.logger.Error("Failed to fetch instance",
			"hospital_id", hospital.HospitalID,
			"instance_uid", instanceUID,
			"error", err)
		http.Error(w, fmt.Sprintf("Failed to fetch instance: %v", err), http.StatusServiceUnavailable)
		return
	}

	// Stream to viewer
	w.Header().Set("Content-Type", "application/dicom")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.dcm", instanceUID))
	io.Copy(w, reader)
}

// fetchInstanceFromEdge requests a DICOM instance from edge via gRPC
func (s *GRPCServer) fetchInstanceFromEdge(ctx context.Context, hospitalID, instanceUID string) (io.Reader, error) {
	// Get edge connection
	s.edgesMu.RLock()
	edge, exists := s.edges[hospitalID]
	s.edgesMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("edge not connected: %s", hospitalID)
	}

	// Create request
	requestID := fmt.Sprintf("%d", time.Now().UnixNano())
	req := &PendingRequest{
		RequestID:    requestID,
		StartTime:    time.Now(),
		ResponseChan: make(chan *grpc.DataResponse, 10),
		ErrorChan:    make(chan error, 1),
	}

	edge.pendingMu.Lock()
	edge.pendingRequests[requestID] = req
	edge.pendingMu.Unlock()

	// Send fetch command
	err := edge.Stream.Send(&grpc.RelayMessage{
		Message: &grpc.RelayMessage_Command{
			Command: &grpc.FetchCommand{
				RequestId:   requestID,
				Type:        "instance",
				InstanceUid: instanceUID,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send fetch command: %w", err)
	}

	s.logger.Info("Sent fetch command to edge",
		"hospital_id", hospitalID,
		"instance_uid", instanceUID,
		"request_id", requestID)

	// Create pipe for streaming response
	pr, pw := io.Pipe()

	// Goroutine to assemble response and write to pipe
	go func() {
		defer pw.Close()

		chunks := make(map[int32][]byte) // For chunked files
		maxChunkIndex := int32(-1)

		for {
			select {
			case <-ctx.Done():
				pw.CloseWithError(ctx.Err())
				return
			case err := <-req.ErrorChan:
				s.logger.Error("Fetch error from edge", "error", err)
				pw.CloseWithError(err)
				return
			case data, ok := <-req.ResponseChan:
				if !ok {
					// Channel closed = transfer complete
					if maxChunkIndex >= 0 {
						// Write assembled chunks in order
						for i := int32(0); i <= maxChunkIndex; i++ {
							if chunkData, exists := chunks[i]; exists {
								pw.Write(chunkData)
							}
						}
					}
					return
				}

				// Handle start metadata
				if start := data.GetStart(); start != nil {
					s.logger.Debug("Received data start",
						"instance_uid", start.InstanceUid,
						"file_size", start.FileSize,
						"chunked", start.Chunked)
					if start.Chunked {
						maxChunkIndex = start.ChunkCount - 1
					}
					continue
				}

				// Handle data chunk
				if chunk := data.GetChunk(); chunk != nil {
					if chunk.ChunkIndex == 0 && chunk.IsLastChunk {
						// Whole file in one message
						pw.Write(chunk.Data)
					} else {
						// Store chunk for reassembly
						chunks[chunk.ChunkIndex] = chunk.Data
					}
				}
			}
		}
	}()

	return pr, nil
}

// extractSubdomain extracts subdomain from Host header
func (s *GRPCServer) extractSubdomain(host string) string {
	// Remove port if present
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Extract subdomain: demo-samsun.zenpacs.com.tr → demo-samsun
	domainSuffix := "." + s.config.Domain
	if strings.HasSuffix(host, domainSuffix) {
		subdomain := strings.TrimSuffix(host, domainSuffix)
		return subdomain
	}

	return ""
}

// extractInstanceUID extracts instance UID from path like /instances/{uid}/download
func (s *GRPCServer) extractInstanceUID(path string) string {
	// Path format: /instances/{uid}/download
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && parts[0] == "instances" {
		return parts[1]
	}
	return ""
}

// handleHealth handles health check requests
func (s *GRPCServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.edgesMu.RLock()
	edgeCount := len(s.edges)
	s.edgesMu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","connected_edges":%d}`, edgeCount)
}

// Stop gracefully shuts down the server
func (s *GRPCServer) Stop() {
	s.logger.Info("Stopping gRPC relay server")
	if s.grpcServer != nil {
		s.grpcServer.GracefulStop()
	}
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}
}
