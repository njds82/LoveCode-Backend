package harness

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// DevicePairRequest matches the body of POST /v1/devices/pair.
type DevicePairRequest struct {
	PairingCode           string   `json:"pairing_code"`
	DeviceID              string   `json:"device_id"`
	Hostname              string   `json:"hostname"`
	Platform              string   `json:"platform"`
	Arch                  string   `json:"arch"`
	AnnouncedCapabilities []string `json:"announced_capabilities"`
}

// DevicePairResponse matches the response of POST /v1/devices/pair.
type DevicePairResponse struct {
	DeviceID            string   `json:"device_id"`
	Token               string   `json:"token"`
	Status              string   `json:"status"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
}

// MockDeviceNode simulates a physical or edge Device Node.
type MockDeviceNode struct {
	DeviceID     string
	Hostname     string
	Platform     string
	Arch         string
	Capabilities []string
	Token        string
	TLSServer    net.Listener
	TLSAddr      string
	activeConns  map[net.Conn]struct{}
	mu           sync.Mutex
	closed       bool
	Executions   []DeviceExecutionRecord
}

// DeviceExecutionRecord logs executions dispatched to the device.
type DeviceExecutionRecord struct {
	Capability string
	Action     string
	Arguments  map[string]any
}

// NewMockDeviceNode creates an un-paired mock device node.
func NewMockDeviceNode(deviceID, hostname, platform, arch string, capabilities []string) *MockDeviceNode {
	return &MockDeviceNode{
		DeviceID:     deviceID,
		Hostname:     hostname,
		Platform:     platform,
		Arch:         arch,
		Capabilities: capabilities,
		activeConns:  make(map[net.Conn]struct{}),
	}
}

// Pair contacts the LoveCode server to redeem pairing code and obtain device token.
func (d *MockDeviceNode) Pair(ctx context.Context, client *Client, pairingCode string) (*DevicePairResponse, *Response, error) {
	req := DevicePairRequest{
		PairingCode:           pairingCode,
		DeviceID:              d.DeviceID,
		Hostname:              d.Hostname,
		Platform:              d.Platform,
		Arch:                  d.Arch,
		AnnouncedCapabilities: d.Capabilities,
	}

	var pairResp DevicePairResponse
	resp, err := client.PostJSON(ctx, "/v1/devices/pair", req, &pairResp)
	if err != nil {
		return nil, resp, err
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		d.mu.Lock()
		d.Token = pairResp.Token
		d.mu.Unlock()
	}

	return &pairResp, resp, nil
}

// SendHeartbeat sends a telemetry heartbeat to /v1/devices/{id}/heartbeat.
func (d *MockDeviceNode) SendHeartbeat(ctx context.Context, client *Client, metrics map[string]any) (*Response, error) {
	d.mu.Lock()
	tok := d.Token
	d.mu.Unlock()

	deviceClient := client.WithToken(tok)
	body := map[string]any{
		"status":  "online",
		"metrics": metrics,
	}
	return deviceClient.Post(ctx, fmt.Sprintf("/v1/devices/%s/heartbeat", d.DeviceID), body)
}

// StartTLSDaemon spins up an in-memory TLS JSON-RPC daemon representing the device's remote listener.
func (d *MockDeviceNode) StartTLSDaemon(t testing.TB) string {
	t.Helper()

	cert, err := generateSelfSignedCert()
	if err != nil {
		t.Fatalf("generate cert failed: %v", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}

	tlsLn := tls.NewListener(ln, tlsConfig)
	d.TLSServer = tlsLn
	d.TLSAddr = ln.Addr().String()

	go func() {
		for {
			conn, err := tlsLn.Accept()
			if err != nil {
				return
			}
			d.mu.Lock()
			if d.closed {
				d.mu.Unlock()
				_ = conn.Close()
				return
			}
			d.activeConns[conn] = struct{}{}
			d.mu.Unlock()

			go d.handleTLSConn(conn)
		}
	}()

	t.Cleanup(func() {
		d.Close()
	})

	return d.TLSAddr
}

func (d *MockDeviceNode) handleTLSConn(conn net.Conn) {
	defer func() {
		d.mu.Lock()
		delete(d.activeConns, conn)
		d.mu.Unlock()
		_ = conn.Close()
	}()
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req JSONRPCRequest
		if err := decoder.Decode(&req); err != nil {
			return
		}

		if req.Method == "execute" {
			var params struct {
				Capability string         `json:"capability"`
				Action     string         `json:"action"`
				Arguments  map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)

			d.mu.Lock()
			d.Executions = append(d.Executions, DeviceExecutionRecord{
				Capability: params.Capability,
				Action:     params.Action,
				Arguments:  params.Arguments,
			})
			d.mu.Unlock()

			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"status":  "success",
					"output":  fmt.Sprintf("executed %s:%s on %s", params.Capability, params.Action, d.DeviceID),
					"details": params.Arguments,
				},
			}
			_ = encoder.Encode(resp)
		} else {
			resp := JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  map[string]any{"status": "ok"},
			}
			_ = encoder.Encode(resp)
		}
	}
}

// Close stops the TLS daemon and closes all active client connections.
func (d *MockDeviceNode) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	if d.TLSServer != nil {
		_ = d.TLSServer.Close()
	}
	for conn := range d.activeConns {
		_ = conn.Close()
	}
	d.activeConns = make(map[net.Conn]struct{})
	d.mu.Unlock()
}

func generateSelfSignedCert() (tls.Certificate, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(24 * time.Hour)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"LoveCode Test Device"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	return tls.X509KeyPair(certPEM, keyPEM)
}

// GetExecutions returns a copy of received executions.
func (d *MockDeviceNode) GetExecutions() []DeviceExecutionRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]DeviceExecutionRecord, len(d.Executions))
	copy(out, d.Executions)
	return out
}
