package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/lightningnetwork/lnd/lnrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

// ZionLightningBridge represents the main bridge service
type ZionLightningBridge struct {
	lndClient lnrpc.LightningClient
	zionRPC   *ZionRPCClient
	config    *Config
}

// Config holds the bridge configuration
type Config struct {
	ZionRPCURL     string
	LNDHost        string
	LNDTLSCert     string
	LNDMacaroon    string
	BridgePort     string
	LogLevel       string
}

// LightningPayment represents a Lightning Network payment
type LightningPayment struct {
	Invoice     string `json:"invoice"`
	Amount      uint64 `json:"amount"`
	ZionTxHash  string `json:"zion_tx_hash"`
	Status      string `json:"status"`
	Timestamp   int64  `json:"timestamp"`
	PaymentHash string `json:"payment_hash"`
}

// Channel represents a Lightning Network channel
type Channel struct {
	ChannelID     string `json:"channel_id"`
	RemoteNodeID  string `json:"remote_node_id"`
	Capacity      uint64 `json:"capacity"`
	LocalBalance  uint64 `json:"local_balance"`
	RemoteBalance uint64 `json:"remote_balance"`
	Active        bool   `json:"active"`
}

// NodeInfo represents Lightning Network node information
type NodeInfo struct {
	PubKey      string    `json:"pub_key"`
	Alias       string    `json:"alias"`
	NumChannels uint32    `json:"num_channels"`
	Capacity    uint64    `json:"capacity"`
	Synced      bool      `json:"synced"`
	Testnet     bool      `json:"testnet"`
	Channels    []Channel `json:"channels"`
}

// PaymentRequest represents a payment request
type PaymentRequest struct {
	Invoice     string `json:"invoice"`
	ZionAddress string `json:"zion_address"`
	Amount      uint64 `json:"amount,omitempty"`
}

// InvoiceRequest represents an invoice creation request
type InvoiceRequest struct {
	Amount uint64 `json:"amount"`
	Memo   string `json:"memo"`
}

// ZionRPCClient handles communication with ZION blockchain
type ZionRPCClient struct {
	baseURL string
	client  *http.Client
}

// NewZionRPCClient creates a new ZION RPC client
func NewZionRPCClient(baseURL string) *ZionRPCClient {
	return &ZionRPCClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// GetBalance retrieves ZION balance for an address
func (zrc *ZionRPCClient) GetBalance(address string) (uint64, error) {
	// TODO: Implement actual ZION RPC call
	// For now, return mock balance
	return 1000000, nil
}

// SendTransaction sends a ZION transaction
func (zrc *ZionRPCClient) SendTransaction(from, to string, amount uint64) error {
	// TODO: Implement actual ZION transaction
	// For now, just log the transaction
	log.Printf("ZION TX: %s -> %s: %d ZION", from, to, amount)
	return nil
}

// LoadConfig loads configuration from environment variables
func LoadConfig() *Config {
	return &Config{
		ZionRPCURL:  getEnv("ZION_RPC_URL", "http://localhost:18089"),
		LNDHost:     getEnv("LND_HOST", "localhost:10009"),
		LNDTLSCert:  getEnv("LND_TLS_CERT_PATH", "/lnd-certs/tls.cert"),
		LNDMacaroon: getEnv("LND_ADMIN_MACAROON_PATH", "/lnd-certs/admin.macaroon"),
		BridgePort:  getEnv("BRIDGE_PORT", "8090"),
		LogLevel:    getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// NewZionLightningBridge creates a new bridge instance
func NewZionLightningBridge(config *Config) (*ZionLightningBridge, error) {
	// Load TLS certificate
	tlsCreds, err := credentials.NewClientTLSFromFile(config.LNDTLSCert, "")
	if err != nil {
		// Try insecure connection for development
		log.Printf("Warning: Could not load TLS cert, trying insecure connection: %v", err)
		tlsCreds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
	}

	// Load macaroon
	var creds credentials.PerRPCCredentials
	if _, err := os.Stat(config.LNDMacaroon); err == nil {
		macaroonBytes, err := ioutil.ReadFile(config.LNDMacaroon)
		if err != nil {
			return nil, fmt.Errorf("cannot read macaroon file: %v", err)
		}
		
		mac := &macaroon.Macaroon{}
		if err = mac.UnmarshalBinary(macaroonBytes); err != nil {
			return nil, fmt.Errorf("cannot unmarshal macaroon: %v", err)
		}
		
		creds = NewMacaroonCredential(mac)
	} else {
		log.Printf("Warning: Macaroon file not found, proceeding without auth: %v", err)
	}

	// Setup gRPC connection options
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(tlsCreds),
	}
	
	if creds != nil {
		opts = append(opts, grpc.WithPerRPCCredentials(creds))
	}

	// Connect to LND
	conn, err := grpc.Dial(config.LNDHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("cannot dial to lnd: %v", err)
	}

	lndClient := lnrpc.NewLightningClient(conn)

	// Create ZION RPC client
	zionRPC := NewZionRPCClient(config.ZionRPCURL)

	return &ZionLightningBridge{
		lndClient: lndClient,
		zionRPC:   zionRPC,
		config:    config,
	}, nil
}

// GetNodeInfo retrieves Lightning Network node information
func (zlb *ZionLightningBridge) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	info, err := zlb.lndClient.GetInfo(ctx, &lnrpc.GetInfoRequest{})
	if err != nil {
		return nil, err
	}

	channels, err := zlb.GetChannels(ctx)
	if err != nil {
		log.Printf("Warning: Could not get channels: %v", err)
		channels = []Channel{}
	}

	var totalCapacity uint64
	for _, ch := range channels {
		totalCapacity += ch.Capacity
	}

	return &NodeInfo{
		PubKey:      info.IdentityPubkey,
		Alias:       info.Alias,
		NumChannels: info.NumActiveChannels,
		Capacity:    totalCapacity,
		Synced:      info.SyncedToChain,
		Testnet:     info.Testnet,
		Channels:    channels,
	}, nil
}

// GetChannels retrieves all Lightning Network channels
func (zlb *ZionLightningBridge) GetChannels(ctx context.Context) ([]Channel, error) {
	channelsReq := &lnrpc.ListChannelsRequest{}
	channelsResp, err := zlb.lndClient.ListChannels(ctx, channelsReq)
	if err != nil {
		return nil, err
	}

	var channels []Channel
	for _, ch := range channelsResp.Channels {
		channel := Channel{
			ChannelID:     fmt.Sprintf("%d", ch.ChanId),
			RemoteNodeID:  ch.RemotePubkey,
			Capacity:      uint64(ch.Capacity),
			LocalBalance:  uint64(ch.LocalBalance),
			RemoteBalance: uint64(ch.RemoteBalance),
			Active:        ch.Active,
		}
		channels = append(channels, channel)
	}

	return channels, nil
}

// CreateInvoice creates a Lightning Network invoice
func (zlb *ZionLightningBridge) CreateInvoice(ctx context.Context, amount uint64, memo string) (*LightningPayment, error) {
	invoiceReq := &lnrpc.Invoice{
		Value: int64(amount),
		Memo:  memo,
	}

	invoice, err := zlb.lndClient.AddInvoice(ctx, invoiceReq)
	if err != nil {
		return nil, err
	}

	payment := &LightningPayment{
		Invoice:     invoice.PaymentRequest,
		Amount:      amount,
		Status:      "pending",
		Timestamp:   time.Now().Unix(),
		PaymentHash: hex.EncodeToString(invoice.RHash),
	}

	return payment, nil
}

// PayInvoice pays a Lightning Network invoice
func (zlb *ZionLightningBridge) PayInvoice(ctx context.Context, invoice, zionAddress string) error {
	// Decode invoice to get amount
	decodeReq := &lnrpc.PayReqString{PayReq: invoice}
	payReq, err := zlb.lndClient.DecodePayReq(ctx, decodeReq)
	if err != nil {
		return fmt.Errorf("cannot decode invoice: %v", err)
	}

	// Check ZION balance
	balance, err := zlb.zionRPC.GetBalance(zionAddress)
	if err != nil {
		return fmt.Errorf("cannot get ZION balance: %v", err)
	}

	if balance < uint64(payReq.NumSatoshis) {
		return fmt.Errorf("insufficient ZION balance: %d < %d", balance, payReq.NumSatoshis)
	}

	// Send Lightning payment
	sendReq := &lnrpc.SendRequest{
		PaymentRequest: invoice,
	}

	payment, err := zlb.lndClient.SendPaymentSync(ctx, sendReq)
	if err != nil {
		return fmt.Errorf("lightning payment failed: %v", err)
	}

	if payment.PaymentError != "" {
		return fmt.Errorf("payment error: %s", payment.PaymentError)
	}

	// Deduct from ZION balance
	err = zlb.zionRPC.SendTransaction(zionAddress, "lightning_pool_address", uint64(payReq.NumSatoshis))
	if err != nil {
		log.Printf("Warning: Lightning payment succeeded but ZION deduction failed: %v", err)
	}

	log.Printf("⚡ Lightning payment successful: %s", hex.EncodeToString(payment.PaymentHash))
	return nil
}

// HTTP Handlers

func (zlb *ZionLightningBridge) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"service":   "zion-lightning-bridge",
		"timestamp": time.Now().Unix(),
		"mantra":    "Jai Ram Ram Ram Sita Ram Ram Ram Hanuman!",
	})
}

func (zlb *ZionLightningBridge) handleGetNodeInfo(c *gin.Context) {
	ctx := context.Background()
	nodeInfo, err := zlb.GetNodeInfo(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, nodeInfo)
}

func (zlb *ZionLightningBridge) handleGetChannels(c *gin.Context) {
	ctx := context.Background()
	channels, err := zlb.GetChannels(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": channels})
}

func (zlb *ZionLightningBridge) handleCreateInvoice(c *gin.Context) {
	var req InvoiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	payment, err := zlb.CreateInvoice(ctx, req.Amount, req.Memo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, payment)
}

func (zlb *ZionLightningBridge) handlePayInvoice(c *gin.Context) {
	var req PaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx := context.Background()
	err := zlb.PayInvoice(ctx, req.Invoice, req.ZionAddress)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "success",
		"message": "Lightning payment completed",
		"mantra":  "⚡ Jai Ram Ram Ram Sita Ram Ram Ram Hanuman! ⚡",
	})
}

// MacaroonCredential wraps a macaroon to implement credentials.PerRPCCredentials
type MacaroonCredential struct {
	*macaroon.Macaroon
}

// NewMacaroonCredential creates a new macaroon credential
func NewMacaroonCredential(mac *macaroon.Macaroon) *MacaroonCredential {
	return &MacaroonCredential{mac}
}

// RequireTransportSecurity implements credentials.PerRPCCredentials
func (mc *MacaroonCredential) RequireTransportSecurity() bool {
	return true
}

// GetRequestMetadata implements credentials.PerRPCCredentials
func (mc *MacaroonCredential) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	macBytes, err := mc.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"macaroon": hex.EncodeToString(macBytes),
	}, nil
}

func main() {
	// Load configuration
	config := LoadConfig()

	// Create bridge instance
	bridge, err := NewZionLightningBridge(config)
	if err != nil {
		log.Fatalf("Failed to create bridge: %v", err)
	}

	// Setup Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Add CORS middleware
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
		
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		
		c.Next()
	})

	// Routes
	api := r.Group("/api/v1")
	{
		api.GET("/health", bridge.handleHealth)
		api.GET("/node/info", bridge.handleGetNodeInfo)
		api.GET("/channels", bridge.handleGetChannels)
		api.POST("/invoice", bridge.handleCreateInvoice)
		api.POST("/pay", bridge.handlePayInvoice)
	}

	// Legacy routes for compatibility
	r.GET("/health", bridge.handleHealth)

	log.Printf("🌩️ ZION Lightning Bridge starting on port %s", config.BridgePort)
	log.Printf("⚡ Jai Ram Ram Ram Sita Ram Ram Ram Hanuman! ⚡")
	log.Printf("🚀 Lightning Network integration ready!")
	
	if err := r.Run(":" + config.BridgePort); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}