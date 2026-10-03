package workflow

import (
	"fmt"
	"strings"
)

// RealisticTasks returns the four advanced benchmark tasks designed to exercise
// Tzro's core token reduction mechanisms:
// 1. macro_5_large_file_skeleton: 500+ line service module (AST skeletonization).
// 2. macro_6_verbose_log_compact: Panicking pipeline test suite with deep stack traces (log compaction).
// 3. macro_7_tabular_analysis: 120-row orders CSV dataset (tabular SQL ingestion & querying).
// 4. macro_8_multi_pkg_discovery: 15-file multi-package platform architecture (symbol discovery).
func RealisticTasks() []Task {
	return []Task{
		taskLargeFileSkeleton(),
		taskVerboseLogCompact(),
		taskTabularAnalysis(),
		taskMultiPkgDiscovery(),
	}
}

// -----------------------------------------------------------------------------
// Task 5: Large File Skeletonization (500+ lines)
// -----------------------------------------------------------------------------

func taskLargeFileSkeleton() Task {
	serviceGo := generateLargeServiceGo()
	testGo := `package billing

import "testing"

func TestCalculateTierDiscount(t *testing.T) {
	s := NewBillingService()
	if d := s.CalculateTierDiscount("basic", 10000); d != 0 {
		t.Fatalf("expected 0 discount for basic, got %d", d)
	}
	if d := s.CalculateTierDiscount("pro", 10000); d != 1000 {
		t.Fatalf("expected 1000 discount for pro, got %d", d)
	}
	if d := s.CalculateTierDiscount("enterprise", 10000); d != 2000 {
		t.Fatalf("expected 2000 discount for enterprise, got %d", d)
	}
}
`
	files := map[string]string{
		"go.mod":          "module acme/billing\n\ngo 1.22.0\n",
		"service.go":      serviceGo,
		"service_test.go": testGo,
	}

	return Task{
		ID:     "macro_5_large_file_skeleton",
		Prompt: "Fix the discount calculation for enterprise tier in service.go so that TestCalculateTierDiscount passes. Edit the files in this workspace and run the Go tests. Preserve the provided tests and go.mod.",
		Files:  files,
	}
}

func generateLargeServiceGo() string {
	var sb strings.Builder
	sb.WriteString(`package billing

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// BillingTier defines the service level.
type BillingTier string

const (
	TierBasic      BillingTier = "basic"
	TierPro        BillingTier = "pro"
	TierEnterprise BillingTier = "enterprise"
)

// Account represents a customer billing account.
type Account struct {
	ID        string
	Name      string
	Email     string
	Tier      BillingTier
	Balance   int
	CreatedAt time.Time
	Active    bool
}

// Subscription represents an active recurring subscription.
type Subscription struct {
	ID        string
	AccountID string
	Tier      BillingTier
	Price     int
	Interval  string
	RenewsAt  time.Time
	Status    string
}

// Invoice represents a billing invoice.
type Invoice struct {
	ID          string
	AccountID   string
	Subtotal    int
	Discount    int
	Tax         int
	Total       int
	Paid        bool
	IssuedAt    time.Time
	Items       []InvoiceItem
}

// InvoiceItem is a line item in an invoice.
type InvoiceItem struct {
	Description string
	Quantity    int
	UnitPrice   int
	Total       int
}

// TaxRule defines state/regional tax rates.
type TaxRule struct {
	Region string
	Rate   float64
}

// AuditEvent tracks billing state changes.
type AuditEvent struct {
	ID        string
	AccountID string
	Action    string
	Details   string
	Timestamp time.Time
}

// BillingService manages customer billing accounts, invoices, and subscriptions.
type BillingService struct {
	mu            sync.RWMutex
	accounts      map[string]*Account
	subscriptions map[string]*Subscription
	invoices      map[string]*Invoice
	auditLog      []AuditEvent
	taxRules      map[string]float64
}

// NewBillingService initializes a new billing service.
func NewBillingService() *BillingService {
	s := &BillingService{
		accounts:      make(map[string]*Account),
		subscriptions: make(map[string]*Subscription),
		invoices:      make(map[string]*Invoice),
		taxRules: map[string]float64{
			"US-CA": 0.0825,
			"US-NY": 0.08875,
			"US-TX": 0.0625,
			"EU":    0.20,
		},
	}
	return s
}

// RegisterAccount creates a new customer billing account.
func (s *BillingService) RegisterAccount(id, name, email string, tier BillingTier) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || email == "" {
		return nil, errors.New("id and email are required")
	}
	if _, exists := s.accounts[id]; exists {
		return nil, errors.New("account already exists")
	}
	acc := &Account{
		ID:        id,
		Name:      name,
		Email:     email,
		Tier:      tier,
		Balance:   0,
		CreatedAt: time.Now(),
		Active:    true,
	}
	s.accounts[id] = acc
	s.recordAudit(id, "account_created", fmt.Sprintf("tier=%s", tier))
	return acc, nil
}

// GetAccount retrieves an existing account by ID.
func (s *BillingService) GetAccount(id string) (*Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	acc, ok := s.accounts[id]
	if !ok {
		return nil, errors.New("account not found")
	}
	return acc, nil
}

// UpdateAccountTier changes the subscription tier for an account.
func (s *BillingService) UpdateAccountTier(id string, newTier BillingTier) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[id]
	if !ok {
		return errors.New("account not found")
	}
	oldTier := acc.Tier
	acc.Tier = newTier
	s.recordAudit(id, "tier_updated", fmt.Sprintf("%s -> %s", oldTier, newTier))
	return nil
}

// CreateSubscription creates a subscription for an account.
func (s *BillingService) CreateSubscription(subID, accID string, tier BillingTier, price int) (*Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[accID]; !ok {
		return nil, errors.New("account not found")
	}
	sub := &Subscription{
		ID:        subID,
		AccountID: accID,
		Tier:      tier,
		Price:     price,
		Interval:  "monthly",
		RenewsAt:  time.Now().AddDate(0, 1, 0),
		Status:    "active",
	}
	s.subscriptions[subID] = sub
	s.recordAudit(accID, "subscription_created", fmt.Sprintf("sub=%s tier=%s", subID, tier))
	return sub, nil
}

// CancelSubscription marks a subscription as cancelled.
func (s *BillingService) CancelSubscription(subID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subscriptions[subID]
	if !ok {
		return errors.New("subscription not found")
	}
	sub.Status = "cancelled"
	s.recordAudit(sub.AccountID, "subscription_cancelled", subID)
	return nil
}

// PauseSubscription temporarily suspends a subscription.
func (s *BillingService) PauseSubscription(subID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subscriptions[subID]
	if !ok {
		return errors.New("subscription not found")
	}
	sub.Status = "paused"
	s.recordAudit(sub.AccountID, "subscription_paused", subID)
	return nil
}

// ResumeSubscription activates a paused subscription.
func (s *BillingService) ResumeSubscription(subID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subscriptions[subID]
	if !ok {
		return errors.New("subscription not found")
	}
	sub.Status = "active"
	s.recordAudit(sub.AccountID, "subscription_resumed", subID)
	return nil
}

// CalculateTierDiscount calculates the discount in cents according to the tier.
// basic: 0% discount
// pro: 10% discount
// enterprise: 20% discount
func (s *BillingService) CalculateTierDiscount(tier string, amountCents int) int {
	switch strings.ToLower(tier) {
	case "basic":
		return 0
	case "pro":
		return amountCents * 10 / 100
	case "enterprise":
		// BUG: returns 0 instead of 20% discount
		return 0
	default:
		return 0
	}
}

// CalculateTax computes the regional tax for a given taxable amount.
func (s *BillingService) CalculateTax(region string, amountCents int) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.calculateTaxLocked(region, amountCents)
}

// calculateTaxLocked requires the caller to hold the service lock.
func (s *BillingService) calculateTaxLocked(region string, amountCents int) int {
	rate, ok := s.taxRules[region]
	if !ok {
		rate = 0.05
	}
	return int(float64(amountCents) * rate)
}

// GenerateInvoice generates an itemized invoice for an account.
func (s *BillingService) GenerateInvoice(invID, accID string, items []InvoiceItem, region string) (*Invoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[accID]
	if !ok {
		return nil, errors.New("account not found")
	}

	subtotal := 0
	for _, it := range items {
		subtotal += it.Quantity * it.UnitPrice
	}

	discount := s.CalculateTierDiscount(string(acc.Tier), subtotal)
	taxable := subtotal - discount
	if taxable < 0 {
		taxable = 0
	}
	tax := s.calculateTaxLocked(region, taxable)
	total := taxable + tax

	inv := &Invoice{
		ID:        invID,
		AccountID: accID,
		Subtotal:  subtotal,
		Discount:  discount,
		Tax:       tax,
		Total:     total,
		Paid:      false,
		IssuedAt:  time.Now(),
		Items:     items,
	}
	s.invoices[invID] = inv
	s.recordAudit(accID, "invoice_generated", fmt.Sprintf("inv=%s total=%d", invID, total))
	return inv, nil
}

// MarkInvoicePaid marks an invoice as settled.
func (s *BillingService) MarkInvoicePaid(invID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invoices[invID]
	if !ok {
		return errors.New("invoice not found")
	}
	inv.Paid = true
	s.recordAudit(inv.AccountID, "invoice_paid", invID)
	return nil
}

// ProcessRefund processes a partial or full refund for an invoice.
func (s *BillingService) ProcessRefund(invID string, refundCents int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inv, ok := s.invoices[invID]
	if !ok {
		return errors.New("invoice not found")
	}
	if !inv.Paid {
		return errors.New("cannot refund unpaid invoice")
	}
	if refundCents > inv.Total {
		return errors.New("refund exceeds invoice total")
	}
	inv.Total -= refundCents
	s.recordAudit(inv.AccountID, "refund_processed", fmt.Sprintf("inv=%s cents=%d", invID, refundCents))
	return nil
}

// RecordCreditAdjustments adds or subtracts credit balance for an account.
func (s *BillingService) RecordCreditAdjustments(accID string, deltaCents int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[accID]
	if !ok {
		return errors.New("account not found")
	}
	acc.Balance += deltaCents
	s.recordAudit(accID, "credit_adjusted", fmt.Sprintf("delta=%d balance=%d", deltaCents, acc.Balance))
	return nil
}

// ReconcileAccount verifies that account balance matches recorded ledger items.
func (s *BillingService) ReconcileAccount(accID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	acc, ok := s.accounts[accID]
	if !ok {
		return 0, errors.New("account not found")
	}
	unpaidTotal := 0
	for _, inv := range s.invoices {
		if inv.AccountID == accID && !inv.Paid {
			unpaidTotal += inv.Total
		}
	}
	return acc.Balance - unpaidTotal, nil
}

// GetAuditTrail returns all recorded audit events for an account.
func (s *BillingService) GetAuditTrail(accID string) []AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var trail []AuditEvent
	for _, ev := range s.auditLog {
		if ev.AccountID == accID {
			trail = append(trail, ev)
		}
	}
	return trail
}

func (s *BillingService) recordAudit(accID, action, details string) {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", accID, action, time.Now().UnixNano())))
	id := hex.EncodeToString(h[:8])
	s.auditLog = append(s.auditLog, AuditEvent{
		ID:        id,
		AccountID: accID,
		Action:    action,
		Details:   details,
		Timestamp: time.Now(),
	})
}
`)

	// Pad helper methods to reach 550+ lines with meaningful, compilable Go utility functions
	for i := 1; i <= 15; i++ {
		sb.WriteString(fmt.Sprintf(`
// ValidateParameterSet%d executes defensive validation on parameter block %d.
func (s *BillingService) ValidateParameterSet%d(param string, threshold int) bool {
	if len(param) == 0 {
		return false
	}
	if threshold < 0 {
		return false
	}
	hash := sha256.Sum256([]byte(fmt.Sprintf("%%s:%%d", param, threshold)))
	return len(hash) > 0
}

// ReconcileSubsystem%d coordinates reconciliation pass %d across audit records.
func (s *BillingService) ReconcileSubsystem%d(token string) (string, error) {
	if token == "" {
		return "", errors.New("token required")
	}
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:16]), nil
}
`, i, i, i, i, i, i))
	}

	return sb.String()
}

// -----------------------------------------------------------------------------
// Task 6: Verbose Log & Stack Trace Compaction
// -----------------------------------------------------------------------------

func taskVerboseLogCompact() Task {
	pipelineGo := `package pipeline

import (
	"errors"
	"strings"
)

// ProcessBatch executes multi-stage data transformation.
func ProcessBatch(items []string) []string {
	var result []string
	for _, it := range items {
		res := stage1(it)
		result = append(result, res)
	}
	return result
}

func stage1(it string) string { return stage2(it) }
func stage2(it string) string { return stage3(it) }
func stage3(it string) string { return stage4(it) }
func stage4(it string) string { return stage5(it) }
func stage5(it string) string { return stage6(it) }
func stage6(it string) string { return stage7(it) }
func stage7(it string) string { return stage8(it) }
func stage8(it string) string { return stage9(it) }
func stage9(it string) string { return stage10(it) }
func stage10(it string) string {
	// BUG: Empty string causes panic instead of returning "EMPTY"
	if it == "" {
		panic("empty item encountered in pipeline stage 10: index out of bounds")
	}
	return strings.ToUpper(it)
}

func CheckValid(it string) error {
	if it == "" {
		return errors.New("empty string invalid")
	}
	return nil
}
`

	testGo := `package pipeline

import "testing"

func TestProcessBatch(t *testing.T) {
	input := []string{"alpha", "", "beta", "gamma"}
	out := ProcessBatch(input)
	if len(out) != 4 {
		t.Fatalf("expected 4 items, got %d", len(out))
	}
	if out[0] != "ALPHA" {
		t.Fatalf("expected ALPHA for item 0, got %q", out[0])
	}
	if out[1] != "EMPTY" {
		t.Fatalf("expected EMPTY for item 1, got %q", out[1])
	}
	if out[2] != "BETA" {
		t.Fatalf("expected BETA for item 2, got %q", out[2])
	}
}
`

	return Task{
		ID:     "macro_6_verbose_log_compact",
		Prompt: "Fix the panic in pipeline.go when processing batches with empty items so that TestProcessBatch returns \"EMPTY\" for empty strings and passes. Edit the files in this workspace and run the Go tests. Preserve the provided tests and go.mod.",
		Files: map[string]string{
			"go.mod":           "module acme/pipeline\n\ngo 1.22.0\n",
			"pipeline.go":      pipelineGo,
			"pipeline_test.go": testGo,
		},
	}
}

// -----------------------------------------------------------------------------
// Task 7: Tabular Data Exploration & SQL Querying
// -----------------------------------------------------------------------------

func taskTabularAnalysis() Task {
	// Generate a ~120 row CSV of orders.
	// We'll deterministically arrange orders such that:
	// sum(amount_usd) WHERE region='APAC' AND status='completed' = 3450.00
	var sb strings.Builder
	sb.WriteString("order_id,customer_id,region,product_category,amount_usd,status\n")

	// 5 completed APAC orders totaling 3450.00: 500, 750, 1000, 200, 1000
	apacAmounts := []float64{500.00, 750.00, 1000.00, 200.00, 1000.00}
	for i, amt := range apacAmounts {
		sb.WriteString(fmt.Sprintf("ORD-%04d,CUST-%03d,APAC,electronics,%.2f,completed\n", 100+i, 200+i, amt))
	}

	// 5 cancelled APAC orders (should NOT be summed)
	for i := 0; i < 5; i++ {
		sb.WriteString(fmt.Sprintf("ORD-%04d,CUST-%03d,APAC,clothing,%.2f,cancelled\n", 200+i, 300+i, 150.00))
	}

	// 100 other orders in NA and EMEA
	for i := 0; i < 100; i++ {
		region := "NA"
		if i%2 == 0 {
			region = "EMEA"
		}
		status := "completed"
		if i%3 == 0 {
			status = "cancelled"
		}
		sb.WriteString(fmt.Sprintf("ORD-%04d,CUST-%03d,%s,appliances,%.2f,%s\n", 1000+i, 500+(i%20), region, float64(50+i*5), status))
	}

	analyticsGo := `package analytics

// GetAPACCompletedRevenue returns the total revenue from completed orders in the APAC region.
// Query data/orders.csv to determine the exact aggregated value.
func GetAPACCompletedRevenue() float64 {
	// TODO: Replace with the exact sum of amount_usd for region='APAC' and status='completed'
	return 0.0
}
`

	testGo := `package analytics

import "testing"

func TestGetAPACCompletedRevenue(t *testing.T) {
	expected := 3450.00
	actual := GetAPACCompletedRevenue()
	if actual != expected {
		t.Fatalf("expected revenue %.2f, got %.2f", expected, actual)
	}
}
`

	return Task{
		ID:     "macro_7_tabular_analysis",
		Prompt: "Inspect data/orders.csv to calculate the total sum of amount_usd for all orders where region is 'APAC' and status is 'completed'. Update GetAPACCompletedRevenue in analytics.go to return this exact value so that TestGetAPACCompletedRevenue passes. Edit the files in this workspace and run the Go tests. Preserve the provided tests and go.mod.",
		Files: map[string]string{
			"go.mod":            "module acme/analytics\n\ngo 1.22.0\n",
			"data/orders.csv":   sb.String(),
			"analytics.go":      analyticsGo,
			"analytics_test.go": testGo,
		},
	}
}

// -----------------------------------------------------------------------------
// Task 8: Multi-Package Discovery & Cross-Interface Wiring
// -----------------------------------------------------------------------------

func taskMultiPkgDiscovery() Task {
	files := map[string]string{
		"go.mod": "module acme/platform\n\ngo 1.22.0\n",
		"pkg/crypto/hasher.go": `package crypto

import (
	"crypto/sha256"
	"encoding/hex"
)

// TokenHasher computes cryptographic token hashes.
type TokenHasher interface {
	HashToken(raw string) string
}

// SHA256TokenHasher implements TokenHasher using SHA-256.
type SHA256TokenHasher struct{}

// NewSHA256TokenHasher creates a new TokenHasher instance.
func NewSHA256TokenHasher() TokenHasher {
	return &SHA256TokenHasher{}
}

// HashToken hashes a raw token string into hex-encoded SHA-256.
func (h *SHA256TokenHasher) HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
`,
		"pkg/crypto/aes.go": `package crypto

func EncryptStub(data []byte) []byte { return data }
func DecryptStub(data []byte) []byte { return data }
`,
		"pkg/crypto/random.go": `package crypto

func RandomToken() string { return "static-rand-token" }
`,
		"pkg/storage/store.go": `package storage

type Store interface {
	Get(key string) (string, error)
	Set(key, value string) error
}
`,
		"pkg/storage/memory.go": `package storage

import "errors"

type MemoryStore struct {
	data map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]string)}
}

func (m *MemoryStore) Get(key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (m *MemoryStore) Set(key, value string) error {
	m.data[key] = value
	return nil
}
`,
		"pkg/models/user.go": `package models

type User struct {
	ID    string
	Email string
}
`,
		"pkg/models/token.go": `package models

type Token struct {
	UserID    string
	TokenHash string
}
`,
		"pkg/config/settings.go": `package config

type Settings struct {
	Port int
	Env  string
}

func DefaultSettings() Settings {
	return Settings{Port: 8080, Env: "development"}
}
`,
		"pkg/metrics/collector.go": `package metrics

type Collector struct {
	Count int
}

func (c *Collector) Inc() { c.Count++ }
`,
		"pkg/auth/authenticator.go": `package auth

import (
	"errors"
	"acme/platform/pkg/crypto"
)

// Authenticator validates user credentials against expected cryptographic hashes.
type Authenticator struct {
	hasher crypto.TokenHasher
}

// NewAuthenticator creates a new Authenticator.
func NewAuthenticator(h crypto.TokenHasher) *Authenticator {
	return &Authenticator{hasher: h}
}

// VerifyToken compares the hash of rawToken against expectedHash.
func (a *Authenticator) VerifyToken(rawToken, expectedHash string) (bool, error) {
	if a.hasher == nil {
		return false, errors.New("hasher not configured")
	}
	// BUG: rawToken is not hashed using a.hasher.HashToken
	actualHash := ""
	return actualHash == expectedHash, nil
}
`,
		"pkg/auth/authenticator_test.go": `package auth

import (
	"testing"
	"acme/platform/pkg/crypto"
)

func TestAuthenticate_TokenVerification(t *testing.T) {
	hasher := crypto.NewSHA256TokenHasher()
	auth := NewAuthenticator(hasher)

	raw := "secret-auth-token-12345"
	expected := hasher.HashToken(raw)

	valid, err := auth.VerifyToken(raw, expected)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !valid {
		t.Fatal("expected token to verify successfully")
	}
}
`,
	}

	return Task{
		ID:     "macro_8_multi_pkg_discovery",
		Prompt: "In pkg/auth/authenticator.go, implement VerifyToken to hash rawToken using the TokenHasher interface and compare it against expectedHash so that TestAuthenticate_TokenVerification passes. Edit the files in this workspace and run the Go tests. Preserve the provided tests and go.mod.",
		Files:  files,
	}
}
