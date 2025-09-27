package relay

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Duration wraps time.Duration for JSON unmarshaling
type Duration time.Duration

// UnmarshalJSON implements json.Unmarshaler
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(duration)
	return nil
}

// ToDuration converts to time.Duration
func (d Duration) ToDuration() time.Duration {
	return time.Duration(d)
}

// Config holds the relay server configuration
type Config struct {
	// Server configuration
	ListenAddr string `json:"listen_addr"` // e.g., ":443"
	Domain     string `json:"domain"`      // e.g., "zenpacs.com.tr"

	// TLS configuration
	TLS TLSConfig `json:"tls"`

	// Hospital mappings (optional - for static configuration)
	Hospitals []HospitalConfig `json:"hospitals,omitempty"`

	// NATS configuration (optional - for dynamic service discovery)
	NATS *NATSConfig `json:"nats,omitempty"`

	// Timeouts and limits
	IdleTimeout       Duration `json:"idle_timeout"`        // Default: 30s
	MaxConcurrentConn int      `json:"max_concurrent_conn"` // Default: 1000
	RequestTimeout    Duration `json:"request_timeout"`     // Default: 5m (for large file transfers)

	// Monitoring
	MetricsAddr string `json:"metrics_addr,omitempty"` // e.g., ":8080" for metrics endpoint
}

// TLSConfig holds TLS certificate configuration
type TLSConfig struct {
	Enabled   bool   `json:"enabled"`    // Enable TLS (false = assume Ingress/LoadBalancer handles TLS)
	CertFile  string `json:"cert_file"`  // Path to certificate file
	KeyFile   string `json:"key_file"`   // Path to private key file
	AutoCert  bool   `json:"auto_cert"`  // Use Let's Encrypt auto-cert
	ACMEEmail string `json:"acme_email"` // Email for Let's Encrypt notifications (required for auto_cert)
}

// HospitalConfig defines a static hospital mapping
type HospitalConfig struct {
	Code      string `json:"code"`      // e.g., "ankara"
	Subdomain string `json:"subdomain"` // e.g., "ankara.zenpacs.com.tr"
	Token     string `json:"token"`     // Pre-shared token for authentication
}

// NATSConfig holds NATS configuration for dynamic service discovery
type NATSConfig struct {
	URL             string `json:"url"`
	CredentialsFile string `json:"credentials_file,omitempty"`
	Credentials     string `json:"credentials,omitempty"`
	Subject         string `json:"subject"` // e.g., "hospitals.registration"
}

// LoadConfig loads configuration from a JSON file and environment variables
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Set defaults
	if config.IdleTimeout == 0 {
		config.IdleTimeout = Duration(30 * time.Second)
	}
	if config.MaxConcurrentConn == 0 {
		config.MaxConcurrentConn = 1000
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = Duration(5 * time.Minute)
	}

	// TLS is disabled by default (HTTPProxy/Ingress handles TLS)
	// Users must explicitly enable it for standalone deployments

	// Load hospitals from environment variables or separate file
	if err := loadHospitalsFromEnv(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// loadHospitalsFromEnv loads hospital configuration from environment variables
func loadHospitalsFromEnv(config *Config) error {
	// Try to load from hospitals.json file first (for K8s Secret mount)
	if hospitalsData, err := os.ReadFile("hospitals.json"); err == nil {
		var hospitals []HospitalConfig
		if err := json.Unmarshal(hospitalsData, &hospitals); err == nil {
			// Replace tokens with environment variables
			for i, hospital := range hospitals {
				envKey := strings.ToUpper(hospital.Code) + "_TOKEN"
				if token := os.Getenv(envKey); token != "" {
					hospitals[i].Token = token
				}
			}
			config.Hospitals = hospitals
			return nil
		}
	}

	// Fallback: build hospitals from individual environment variables
	hospitalCodes := []string{"ankara", "istanbul", "samsun", "izmir", "antalya"}

	for _, code := range hospitalCodes {
		tokenEnv := strings.ToUpper(code) + "_TOKEN"
		token := os.Getenv(tokenEnv)

		if token != "" {
			hospital := HospitalConfig{
				Code:      code,
				Subdomain: code + "." + config.Domain,
				Token:     token,
			}
			config.Hospitals = append(config.Hospitals, hospital)
		}
	}

	return nil
}