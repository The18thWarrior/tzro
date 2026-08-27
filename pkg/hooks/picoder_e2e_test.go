//go:build integration

package hooks

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Virtual workspace scaffolding — 6 packages, ~30 source files, 9 log files
// ---------------------------------------------------------------------------

// scaffoldWorkspace creates a realistic multi-package Go workspace in a temp dir.
// The workspace is large enough to require 15+ agent turns to fully explore.
func scaffoldWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		// ---- Root files ----
		"go.mod": `module acme/inventory

go 1.22.0

require (
	github.com/go-chi/chi/v5 v5.0.12
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/jmoiron/sqlx v1.3.5
	github.com/redis/go-redis/v9 v9.5.1
	golang.org/x/crypto v0.23.0
)
`,
		"main.go": `package main

import (
	"fmt"
	"log"
	"net/http"

	"acme/inventory/internal/api"
	"acme/inventory/internal/auth"
	"acme/inventory/internal/config"
	"acme/inventory/internal/db"
	"acme/inventory/internal/worker"
)

func main() {
	cfg := config.Load()
	store, err := db.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer store.Close()

	authSvc := auth.NewService(cfg.JWTSecret, cfg.TokenTTL)
	w := worker.New(store, cfg.WorkerConcurrency)
	go w.Start()
	defer w.Stop()

	router := api.NewRouter(store, authSvc, cfg)
	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("inventory service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, router))
}
`,
		"Makefile": `.PHONY: build test lint run migrate

build:
	go build -o bin/inventory ./...

test:
	go test -v -race ./...

lint:
	golangci-lint run ./...

run: build
	./bin/inventory

migrate:
	go run ./cmd/migrate/main.go
`,
		"README.md": `# Inventory Service

Multi-package Go service for product inventory management.

## Packages
- internal/api — HTTP handlers and routing
- internal/auth — JWT authentication and middleware
- internal/config — Environment-based configuration
- internal/db — Database layer (PostgreSQL via sqlx)
- internal/models — Domain types and validation
- internal/worker — Background job processing (stock sync, notifications)

## Known Issues
- TestUpdateStock is failing (see logs/test_db.log)
- Worker retry logic has a race condition (see logs/test_worker.log)
- Auth middleware returns 500 instead of 401 on expired tokens (see logs/test_auth.log)
- Build has type mismatch errors in products.go and service.go (see logs/build_output.log)
- Last staging deploy failed (see logs/deploy_error.log)
- Test coverage is below 50% threshold (see logs/coverage.txt)

## Quick Start
` + "```" + `
cp .env.example .env
make migrate
make run
` + "```" + `
`,
		".env.example": `DATABASE_URL=postgres://localhost:5432/inventory?sslmode=disable
JWT_SECRET=change-me-in-production
TOKEN_TTL=3600
WORKER_CONCURRENCY=4
REDIS_URL=redis://localhost:6379/0
PORT=8080
DEBUG=true
`,
		// ---- internal/config ----
		"internal/config/config.go": `package config

import (
	"os"
	"strconv"
)

// Config holds application configuration.
type Config struct {
	Port               int
	DatabaseURL        string
	JWTSecret          string
	TokenTTL           int
	Debug              bool
	RedisURL           string
	WorkerConcurrency  int
}

// Load reads configuration from environment variables.
func Load() Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8080"))
	ttl, _ := strconv.Atoi(getEnv("TOKEN_TTL", "3600"))
	concurrency, _ := strconv.Atoi(getEnv("WORKER_CONCURRENCY", "4"))
	return Config{
		Port:              port,
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://localhost/inventory"),
		JWTSecret:         getEnv("JWT_SECRET", "dev-secret"),
		TokenTTL:          ttl,
		Debug:             getEnv("DEBUG", "") == "true",
		RedisURL:          getEnv("REDIS_URL", "redis://localhost:6379/0"),
		WorkerConcurrency: concurrency,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
`,
		"internal/config/config_test.go": `package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg := Load()
	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}
}

func TestLoadFromEnv(t *testing.T) {
	os.Setenv("PORT", "9090")
	defer os.Unsetenv("PORT")
	cfg := Load()
	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}
}
`,
		// ---- internal/models ----
		"internal/models/product.go": `package models

import "fmt"

// Product represents an inventory item.
type Product struct {
	ID         int64  ` + "`json:\"id\" db:\"id\"`" + `
	Name       string ` + "`json:\"name\" db:\"name\"`" + `
	SKU        string ` + "`json:\"sku\" db:\"sku\"`" + `
	PriceCents int    ` + "`json:\"price_cents\" db:\"price_cents\"`" + `
	Category   string ` + "`json:\"category\" db:\"category\"`" + `
	StockQty   int    ` + "`json:\"stock_qty\" db:\"stock_qty\"`" + `
}

// PriceDisplay returns a human-readable price string.
func (p Product) PriceDisplay() string {
	return fmt.Sprintf("$%d.%02d", p.PriceCents/100, p.PriceCents%100)
}

// InStock returns true if the product has positive stock.
func (p Product) InStock() bool { return p.StockQty > 0 }

// Validate checks required fields.
func (p Product) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("product name is required")
	}
	if p.SKU == "" {
		return fmt.Errorf("product SKU is required")
	}
	if p.PriceCents < 0 {
		return fmt.Errorf("price cannot be negative")
	}
	return nil
}
`,
		"internal/models/order.go": `package models

import "time"

// OrderStatus enumerates order lifecycle states.
type OrderStatus string

const (
	OrderPending   OrderStatus = "pending"
	OrderConfirmed OrderStatus = "confirmed"
	OrderShipped   OrderStatus = "shipped"
	OrderDelivered OrderStatus = "delivered"
	OrderCancelled OrderStatus = "cancelled"
)

// OrderItem represents a line item in an order.
type OrderItem struct {
	ProductID int64 ` + "`json:\"product_id\" db:\"product_id\"`" + `
	Quantity  int   ` + "`json:\"quantity\" db:\"quantity\"`" + `
	UnitPrice int   ` + "`json:\"unit_price\" db:\"unit_price\"`" + `
}

// Order represents a customer order.
type Order struct {
	ID         int64       ` + "`json:\"id\" db:\"id\"`" + `
	CustomerID int64       ` + "`json:\"customer_id\" db:\"customer_id\"`" + `
	Items      []OrderItem ` + "`json:\"items\"`" + `
	Status     OrderStatus ` + "`json:\"status\" db:\"status\"`" + `
	TotalCents int         ` + "`json:\"total_cents\" db:\"total_cents\"`" + `
	CreatedAt  time.Time   ` + "`json:\"created_at\" db:\"created_at\"`" + `
}

// CalculateTotal computes the order total from line items.
func (o *Order) CalculateTotal() {
	total := 0
	for _, item := range o.Items {
		total += item.UnitPrice * item.Quantity
	}
	o.TotalCents = total
}
`,
		"internal/models/user.go": `package models

import "time"

// User represents a registered user.
type User struct {
	ID           int64     ` + "`json:\"id\" db:\"id\"`" + `
	Email        string    ` + "`json:\"email\" db:\"email\"`" + `
	PasswordHash string    ` + "`json:\"-\" db:\"password_hash\"`" + `
	Role         string    ` + "`json:\"role\" db:\"role\"`" + `
	CreatedAt    time.Time ` + "`json:\"created_at\" db:\"created_at\"`" + `
}

// IsAdmin checks if the user has admin privileges.
func (u User) IsAdmin() bool { return u.Role == "admin" }
`,
		// ---- internal/db ----
		"internal/db/store.go": `package db

import (
	"database/sql"
	"fmt"

	"acme/inventory/internal/models"
)

// Store wraps a database connection and provides repository methods.
type Store struct {
	db *sql.DB
}

// Connect opens a database connection.
func Connect(dsn string) (*Store, error) {
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("db connect: %w", err)
	}
	return &Store{db: conn}, nil
}

// Close closes the database connection.
func (s *Store) Close() error { return s.db.Close() }

// ListProducts returns all products with optional category filter.
func (s *Store) ListProducts(category string) ([]models.Product, error) {
	query := "SELECT id, name, sku, price_cents, category, stock_qty FROM products"
	args := []any{}
	if category != "" {
		query += " WHERE category = $1"
		args = append(args, category)
	}
	query += " ORDER BY name"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.SKU, &p.PriceCents, &p.Category, &p.StockQty); err != nil {
			return nil, err
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

// GetProduct retrieves a single product by ID.
func (s *Store) GetProduct(id int64) (*models.Product, error) {
	var p models.Product
	err := s.db.QueryRow(
		"SELECT id, name, sku, price_cents, category, stock_qty FROM products WHERE id = $1", id,
	).Scan(&p.ID, &p.Name, &p.SKU, &p.PriceCents, &p.Category, &p.StockQty)
	if err != nil { return nil, err }
	return &p, nil
}

// CreateProduct inserts a new product.
func (s *Store) CreateProduct(p models.Product) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		"INSERT INTO products (name, sku, price_cents, category, stock_qty) VALUES ($1,$2,$3,$4,$5) RETURNING id",
		p.Name, p.SKU, p.PriceCents, p.Category, p.StockQty,
	).Scan(&id)
	return id, err
}

// UpdateStock adjusts the stock quantity for a product.
func (s *Store) UpdateStock(productID int64, delta int) error {
	_, err := s.db.Exec("UPDATE products SET stock_qty = stock_qty + $1 WHERE id = $2", delta, productID)
	return err
}

// DeleteProduct removes a product by ID.
func (s *Store) DeleteProduct(id int64) error {
	_, err := s.db.Exec("DELETE FROM products WHERE id = $1", id)
	return err
}
`,
		"internal/db/orders.go": `package db

import (
	"database/sql"

	"acme/inventory/internal/models"
)

// CreateOrder inserts a new order and its items in a transaction.
func (s *Store) CreateOrder(order models.Order) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil { return 0, err }
	defer tx.Rollback()

	var orderID int64
	err = tx.QueryRow(
		"INSERT INTO orders (customer_id, status, total_cents) VALUES ($1, $2, $3) RETURNING id",
		order.CustomerID, order.Status, order.TotalCents,
	).Scan(&orderID)
	if err != nil { return 0, err }

	for _, item := range order.Items {
		_, err = tx.Exec(
			"INSERT INTO order_items (order_id, product_id, quantity, unit_price) VALUES ($1, $2, $3, $4)",
			orderID, item.ProductID, item.Quantity, item.UnitPrice,
		)
		if err != nil { return 0, err }
	}

	return orderID, tx.Commit()
}

// GetOrder retrieves an order by ID.
func (s *Store) GetOrder(id int64) (*models.Order, error) {
	var o models.Order
	err := s.db.QueryRow(
		"SELECT id, customer_id, status, total_cents, created_at FROM orders WHERE id = $1", id,
	).Scan(&o.ID, &o.CustomerID, &o.Status, &o.TotalCents, &o.CreatedAt)
	if err != nil { return nil, err }
	return &o, nil
}

// ListOrders returns orders for a customer.
func (s *Store) ListOrders(customerID int64) ([]models.Order, error) {
	rows, err := s.db.Query("SELECT id, customer_id, status, total_cents, created_at FROM orders WHERE customer_id = $1 ORDER BY created_at DESC", customerID)
	if err != nil { return nil, err }
	defer rows.Close()
	var orders []models.Order
	for rows.Next() {
		var o models.Order
		if err := rows.Scan(&o.ID, &o.CustomerID, &o.Status, &o.TotalCents, &o.CreatedAt); err != nil { return nil, err }
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// UpdateOrderStatus transitions an order's status.
func (s *Store) UpdateOrderStatus(id int64, status models.OrderStatus) error {
	result, err := s.db.Exec("UPDATE orders SET status = $1 WHERE id = $2", status, id)
	if err != nil { return err }
	n, _ := result.RowsAffected()
	if n == 0 { return sql.ErrNoRows }
	return nil
}
`,
		"internal/db/users.go": `package db

import "acme/inventory/internal/models"

// GetUserByEmail retrieves a user by email address.
func (s *Store) GetUserByEmail(email string) (*models.User, error) {
	var u models.User
	err := s.db.QueryRow(
		"SELECT id, email, password_hash, role, created_at FROM users WHERE email = $1", email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil { return nil, err }
	return &u, nil
}

// CreateUser inserts a new user.
func (s *Store) CreateUser(u models.User) (int64, error) {
	var id int64
	err := s.db.QueryRow(
		"INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id",
		u.Email, u.PasswordHash, u.Role,
	).Scan(&id)
	return id, err
}
`,
		"internal/db/migrations.go": `package db

import "database/sql"

// RunMigrations executes schema migrations.
func RunMigrations(conn *sql.DB) error {
	schema := ` + "`" + `
		CREATE TABLE IF NOT EXISTS users (
			id            SERIAL PRIMARY KEY,
			email         TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'user',
			created_at    TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS products (
			id          SERIAL PRIMARY KEY,
			name        TEXT NOT NULL,
			sku         TEXT UNIQUE NOT NULL,
			price_cents INTEGER NOT NULL DEFAULT 0,
			category    TEXT NOT NULL DEFAULT 'general',
			stock_qty   INTEGER NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ DEFAULT NOW(),
			updated_at  TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS orders (
			id          SERIAL PRIMARY KEY,
			customer_id INTEGER REFERENCES users(id),
			status      TEXT NOT NULL DEFAULT 'pending',
			total_cents INTEGER NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS order_items (
			id         SERIAL PRIMARY KEY,
			order_id   INTEGER REFERENCES orders(id) ON DELETE CASCADE,
			product_id INTEGER REFERENCES products(id),
			quantity   INTEGER NOT NULL DEFAULT 1,
			unit_price INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS worker_jobs (
			id         SERIAL PRIMARY KEY,
			type       TEXT NOT NULL,
			payload    JSONB NOT NULL DEFAULT '{}',
			status     TEXT NOT NULL DEFAULT 'pending',
			attempts   INTEGER NOT NULL DEFAULT 0,
			max_retries INTEGER NOT NULL DEFAULT 3,
			last_error TEXT,
			created_at TIMESTAMPTZ DEFAULT NOW(),
			run_at     TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_products_category ON products(category);
		CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders(customer_id);
		CREATE INDEX IF NOT EXISTS idx_worker_jobs_status ON worker_jobs(status, run_at);
	` + "`" + `
	_, err := conn.Exec(schema)
	return err
}
`,
		// ---- internal/auth ----
		"internal/auth/service.go": `package auth

import (
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Service handles authentication and token management.
type Service struct {
	jwtSecret string
	tokenTTL  int
}

// NewService creates a new auth service.
func NewService(secret string, ttl int) *Service {
	return &Service{jwtSecret: secret, tokenTTL: ttl}
}

// HashPassword hashes a plaintext password using bcrypt.
func (s *Service) HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hash), err
}

// CheckPassword verifies a password against a hash.
func (s *Service) CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// GenerateToken creates a signed JWT for a user.
func (s *Service) GenerateToken(userID int64, role string) (string, error) {
	// BUG: token generation uses wrong expiry calculation
	expiry := time.Now().Add(time.Duration(s.tokenTTL))  // missing * time.Second
	_ = expiry
	return fmt.Sprintf("jwt-token-for-%d-%s", userID, role), nil
}

// ValidateToken parses and validates a JWT token.
func (s *Service) ValidateToken(tokenStr string) (int64, string, error) {
	// TODO: implement real JWT validation
	return 0, "", fmt.Errorf("token validation not implemented")
}
`,
		"internal/auth/middleware.go": `package auth

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const UserIDKey contextKey = "user_id"
const UserRoleKey contextKey = "user_role"

// RequireAuth middleware validates JWT tokens.
func (s *Service) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if header == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(header, "Bearer ")
		userID, role, err := s.ValidateToken(token)
		if err != nil {
			// BUG: returns 500 instead of 401
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		ctx = context.WithValue(ctx, UserRoleKey, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAdmin middleware ensures the user has admin role.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := r.Context().Value(UserRoleKey).(string)
		if !ok || role != "admin" {
			http.Error(w, "admin access required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
`,
		// ---- internal/api ----
		"internal/api/router.go": `package api

import (
	"net/http"

	"acme/inventory/internal/auth"
	"acme/inventory/internal/config"
	"acme/inventory/internal/db"
)

// NewRouter creates the HTTP handler with all routes.
func NewRouter(store *db.Store, authSvc *auth.Service, cfg config.Config) http.Handler {
	mux := http.NewServeMux()

	// Public routes
	mux.HandleFunc("POST /api/auth/login", loginHandler(store, authSvc))
	mux.HandleFunc("POST /api/auth/register", registerHandler(store, authSvc))
	mux.HandleFunc("GET /health", healthHandler)

	// Protected routes
	mux.HandleFunc("GET /api/products", listProductsHandler(store))
	mux.HandleFunc("GET /api/products/{id}", getProductHandler(store))
	mux.HandleFunc("POST /api/products", createProductHandler(store))
	mux.HandleFunc("PATCH /api/products/{id}/stock", updateStockHandler(store))
	mux.HandleFunc("DELETE /api/products/{id}", deleteProductHandler(store))

	mux.HandleFunc("POST /api/orders", createOrderHandler(store))
	mux.HandleFunc("GET /api/orders/{id}", getOrderHandler(store))

	return LoggingMiddleware(mux)
}
`,
		"internal/api/products.go": `package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"acme/inventory/internal/db"
	"acme/inventory/internal/models"
)

func listProductsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		category := r.URL.Query().Get("category")
		products, err := store.ListProducts(category)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(products)
	}
}

func getProductHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		product, err := store.GetProduct(id)
		if err != nil {
			http.Error(w, "product not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(product)
	}
}

func createProductHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p models.Product
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := p.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, err := store.CreateProduct(p)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int64{"id": id})
	}
}

func updateStockHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var body struct {
			Delta float64 ` + "`json:\"delta\"`" + ` // BUG: should be int, causes type mismatch
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := store.UpdateStock(id, int(body.Delta)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteProductHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err := store.DeleteProduct(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
`,
		"internal/api/orders.go": `package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"acme/inventory/internal/db"
	"acme/inventory/internal/models"
)

func createOrderHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var order models.Order
		if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
			http.Error(w, "invalid order payload", http.StatusBadRequest)
			return
		}
		order.Status = models.OrderPending
		order.CalculateTotal()
		id, err := store.CreateOrder(order)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int64{"id": id})
	}
}

func getOrderHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		order, err := store.GetOrder(id)
		if err != nil {
			http.Error(w, "order not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(order)
	}
}

func loginHandler(store *db.Store, authSvc interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}

func registerHandler(store *db.Store, authSvc interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	}
}
`,
		"internal/api/middleware.go": `package api

import (
	"log"
	"net/http"
	"time"
)

// LoggingMiddleware logs request method, path, status code, and duration.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, rw.statusCode, time.Since(start))
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
`,
		"internal/api/health.go": `package api

import (
	"encoding/json"
	"net/http"
	"runtime"
)

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"go_version": runtime.Version(),
		"goroutines": runtime.NumGoroutine(),
	})
}
`,
		// ---- internal/worker ----
		"internal/worker/worker.go": `package worker

import (
	"log"
	"sync"
	"time"

	"acme/inventory/internal/db"
)

// Worker processes background jobs.
type Worker struct {
	store       *db.Store
	concurrency int
	quit        chan struct{}
	wg          sync.WaitGroup
}

// New creates a new worker pool.
func New(store *db.Store, concurrency int) *Worker {
	return &Worker{
		store:       store,
		concurrency: concurrency,
		quit:        make(chan struct{}),
	}
}

// Start launches worker goroutines.
func (w *Worker) Start() {
	for i := 0; i < w.concurrency; i++ {
		w.wg.Add(1)
		go w.run(i)
	}
	log.Printf("worker pool started with %d goroutines", w.concurrency)
}

// Stop gracefully shuts down the worker pool.
func (w *Worker) Stop() {
	close(w.quit)
	w.wg.Wait()
	log.Println("worker pool stopped")
}

func (w *Worker) run(id int) {
	defer w.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.quit:
			return
		case <-ticker.C:
			w.processJobs(id)
		}
	}
}

func (w *Worker) processJobs(workerID int) {
	// BUG: race condition — multiple workers can grab the same job
	// because we SELECT then UPDATE without a lock
	log.Printf("worker %d: polling for jobs", workerID)
}
`,
		"internal/worker/stock_sync.go": `package worker

import (
	"fmt"
	"log"
)

// SyncStock reconciles inventory counts with the external warehouse API.
func (w *Worker) SyncStock() error {
	log.Println("stock sync: starting reconciliation")
	products, err := w.store.ListProducts("")
	if err != nil {
		return fmt.Errorf("stock sync failed: %w", err)
	}
	for _, p := range products {
		if p.StockQty < 0 {
			log.Printf("stock sync: WARNING negative stock for %s (qty=%d)", p.SKU, p.StockQty)
		}
	}
	return nil
}
`,
		"internal/worker/notifications.go": `package worker

import (
	"fmt"
	"log"
)

// SendNotification dispatches order status notifications.
func (w *Worker) SendNotification(orderID int64, event string) error {
	order, err := w.store.GetOrder(orderID)
	if err != nil {
		return fmt.Errorf("notification: order %d not found: %w", orderID, err)
	}
	// BUG: doesn't check if notification was already sent (no idempotency)
	log.Printf("notification: sending %s for order %d (customer %d)", event, order.ID, order.CustomerID)
	return nil
}
`,
		"internal/worker/retry.go": `package worker

import (
	"log"
	"math"
	"time"
)

// RetryConfig controls retry behavior for failed jobs.
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
	}
}

// BackoffDuration computes the delay for a given attempt.
func (rc RetryConfig) BackoffDuration(attempt int) time.Duration {
	if attempt <= 0 {
		return rc.InitialBackoff
	}
	delay := float64(rc.InitialBackoff) * math.Pow(rc.Multiplier, float64(attempt))
	if delay > float64(rc.MaxBackoff) {
		delay = float64(rc.MaxBackoff)
	}
	return time.Duration(delay)
}

// ShouldRetry determines if a job should be retried.
func (rc RetryConfig) ShouldRetry(attempt int, err error) bool {
	if err == nil {
		return false
	}
	if attempt >= rc.MaxRetries {
		log.Printf("retry: max attempts (%d) reached, giving up", rc.MaxRetries)
		return false
	}
	return true
}
`,
		// ---- Verbose log files (each contains runtime frames the compactor will elide) ----
		"logs/test_db.log":       generateTestDBLog(),
		"logs/test_api.log":      generateTestAPILog(),
		"logs/test_auth.log":     generateTestAuthLog(),
		"logs/test_worker.log":   generateTestWorkerLog(),
		"logs/build_output.log":  generateBuildLog(),
		"logs/lint_output.log":   generateLintLog(),
		"logs/api_response.json": generateAPIResponseLog(),
		"logs/coverage.txt":      generateCoverageReport(),
		"logs/deploy_error.log":  generateDeployErrorLog(),
	}

	for relPath, content := range files {
		absPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(absPath), err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", absPath, err)
		}
	}

	return dir
}

// ---------------------------------------------------------------------------
// Verbose log generators (each produces content with runtime frames the compactor can elide)
// ---------------------------------------------------------------------------

func generateTestDBLog() string {
	var sb strings.Builder
	sb.WriteString("=== RUN   TestConnect\n--- PASS: TestConnect (0.02s)\n")
	sb.WriteString("=== RUN   TestListProducts\n--- PASS: TestListProducts (0.03s)\n")
	sb.WriteString("=== RUN   TestGetProduct\n--- PASS: TestGetProduct (0.01s)\n")
	sb.WriteString("=== RUN   TestCreateProduct\n--- PASS: TestCreateProduct (0.02s)\n")
	sb.WriteString("=== RUN   TestUpdateStock\n--- FAIL: TestUpdateStock (0.01s)\n")
	sb.WriteString("    store_test.go:89: expected stock_qty 15, got 10\n")
	sb.WriteString("    store_test.go:90: delta was applied but not persisted to DB\n")
	sb.WriteString("=== RUN   TestCreateProductDuplicate\n--- FAIL: TestCreateProductDuplicate (0.02s)\n")
	sb.WriteString("panic: runtime error: invalid memory address or nil pointer dereference\n")
	sb.WriteString("[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x1234567]\n\n")
	sb.WriteString("goroutine 42 [running]:\n")
	sb.WriteString("acme/inventory/internal/db.(*Store).CreateProduct(0xc0000b2000, {0xc0000fe000})\n")
	sb.WriteString("\t/app/internal/db/store.go:72 +0x1a5\n")
	sb.WriteString("acme/inventory/internal/db.TestCreateProductDuplicate(0xc000106d00)\n")
	sb.WriteString("\t/app/internal/db/store_test.go:45 +0x312\n")
	for i := 0; i < 30; i++ { sb.WriteString(fmt.Sprintf("testing.go:%d +0x%x\n", 800+i, 0x20+i)) }
	for i := 0; i < 25; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 200+i, 0x10+i)) }
	sb.WriteString("\n=== RUN   TestCreateOrder\n--- FAIL: TestCreateOrder (0.05s)\n")
	sb.WriteString("    orders_test.go:23: foreign key constraint violation: customer_id references users(id)\n")
	sb.WriteString("panic: runtime error: index out of range [5] with length 3\n\n")
	sb.WriteString("goroutine 43 [running]:\n")
	sb.WriteString("acme/inventory/internal/db.(*Store).CreateOrder(0xc0000b2000, {0xc0000fe200})\n")
	sb.WriteString("\t/app/internal/db/orders.go:18 +0x2b3\n")
	for i := 0; i < 20; i++ { sb.WriteString(fmt.Sprintf("testing.go:%d +0x%x\n", 900+i, 0x30+i)) }
	for i := 0; i < 20; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 250+i, 0x15+i)) }
	sb.WriteString("\n=== RUN   TestDeleteProduct\n--- PASS: TestDeleteProduct (0.01s)\n")
	sb.WriteString("=== RUN   TestUpdateOrderStatus\n--- PASS: TestUpdateOrderStatus (0.01s)\n")
	sb.WriteString("FAIL\tacme/inventory/internal/db\t0.18s\n")
	return sb.String()
}

func generateTestAPILog() string {
	var sb strings.Builder
	sb.WriteString("=== RUN   TestHealthEndpoint\n--- PASS: TestHealthEndpoint (0.01s)\n")
	sb.WriteString("=== RUN   TestListProductsHandler\n--- PASS: TestListProductsHandler (0.02s)\n")
	sb.WriteString("=== RUN   TestGetProductHandler\n--- PASS: TestGetProductHandler (0.01s)\n")
	sb.WriteString("=== RUN   TestCreateProductHandler_Valid\n--- PASS: TestCreateProductHandler_Valid (0.02s)\n")
	sb.WriteString("=== RUN   TestCreateProductHandler_Invalid\n--- PASS: TestCreateProductHandler_Invalid (0.01s)\n")
	sb.WriteString("=== RUN   TestUpdateStockHandler\n--- FAIL: TestUpdateStockHandler (0.01s)\n")
	sb.WriteString("    products_test.go:112: handler returned status 500, want 204\n")
	sb.WriteString("    products_test.go:113: response body: \"cannot use body.Delta (float64) as int\"\n")
	sb.WriteString("panic: interface conversion: interface {} is float64, not int\n\n")
	sb.WriteString("goroutine 55 [running]:\n")
	sb.WriteString("acme/inventory/internal/api.updateStockHandler.func1(0xc0001a2000, 0xc0001a4000)\n")
	sb.WriteString("\t/app/internal/api/products.go:78 +0x3a5\n")
	for i := 0; i < 15; i++ { sb.WriteString(fmt.Sprintf("net/http/server.go:%d +0x%x\n", 1800+i, 0x30+i)) }
	for i := 0; i < 15; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 300+i, 0x10+i)) }
	sb.WriteString("\n=== RUN   TestCreateOrderHandler\n--- FAIL: TestCreateOrderHandler (0.03s)\n")
	sb.WriteString("    orders_test.go:45: expected status 201, got 500\n")
	sb.WriteString("    orders_test.go:46: error: foreign key constraint\n")
	sb.WriteString("=== RUN   TestDeleteProductHandler\n--- PASS: TestDeleteProductHandler (0.01s)\n")
	sb.WriteString("=== RUN   TestLoggingMiddleware\n--- PASS: TestLoggingMiddleware (0.01s)\n")
	sb.WriteString("FAIL\tacme/inventory/internal/api\t0.14s\n")
	return sb.String()
}

func generateTestAuthLog() string {
	var sb strings.Builder
	sb.WriteString("=== RUN   TestHashPassword\n--- PASS: TestHashPassword (0.08s)\n")
	sb.WriteString("=== RUN   TestCheckPassword\n--- PASS: TestCheckPassword (0.07s)\n")
	sb.WriteString("=== RUN   TestGenerateToken\n--- FAIL: TestGenerateToken (0.01s)\n")
	sb.WriteString("    service_test.go:34: token expiry is in nanoseconds instead of seconds\n")
	sb.WriteString("    service_test.go:35: expected token to expire in ~3600s, got 3600ns\n")
	sb.WriteString("=== RUN   TestValidateToken\n--- FAIL: TestValidateToken (0.01s)\n")
	sb.WriteString("    service_test.go:50: ValidateToken returned error: token validation not implemented\n")
	sb.WriteString("=== RUN   TestRequireAuth_NoHeader\n--- PASS: TestRequireAuth_NoHeader (0.01s)\n")
	sb.WriteString("=== RUN   TestRequireAuth_InvalidToken\n--- FAIL: TestRequireAuth_InvalidToken (0.01s)\n")
	sb.WriteString("    middleware_test.go:28: expected status 401 Unauthorized, got 500 Internal Server Error\n")
	sb.WriteString("    middleware_test.go:29: BUG: middleware returns 500 when token validation fails\n")
	sb.WriteString("panic: runtime error: invalid memory address or nil pointer dereference\n\n")
	sb.WriteString("goroutine 60 [running]:\n")
	sb.WriteString("acme/inventory/internal/auth.(*Service).RequireAuth.func1(0xc000200000, 0xc000202000)\n")
	sb.WriteString("\t/app/internal/auth/middleware.go:25 +0x195\n")
	for i := 0; i < 20; i++ { sb.WriteString(fmt.Sprintf("net/http/server.go:%d +0x%x\n", 2000+i, 0x40+i)) }
	for i := 0; i < 15; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 350+i, 0x18+i)) }
	sb.WriteString("\n=== RUN   TestRequireAdmin\n--- PASS: TestRequireAdmin (0.01s)\n")
	sb.WriteString("FAIL\tacme/inventory/internal/auth\t0.21s\n")
	return sb.String()
}

func generateTestWorkerLog() string {
	var sb strings.Builder
	sb.WriteString("=== RUN   TestWorkerStartStop\n--- PASS: TestWorkerStartStop (0.10s)\n")
	sb.WriteString("=== RUN   TestProcessJobs_Race\n")
	sb.WriteString("==================\nWARNING: DATA RACE\n")
	sb.WriteString("Read at 0x00c0001a2020 by goroutine 72:\n")
	sb.WriteString("  acme/inventory/internal/worker.(*Worker).processJobs()\n")
	sb.WriteString("      /app/internal/worker/worker.go:68 +0x85\n\n")
	sb.WriteString("Previous write at 0x00c0001a2020 by goroutine 73:\n")
	sb.WriteString("  acme/inventory/internal/worker.(*Worker).processJobs()\n")
	sb.WriteString("      /app/internal/worker/worker.go:68 +0x9a\n")
	for i := 0; i < 25; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 400+i, 0x20+i)) }
	sb.WriteString("\nGoroutine 72 (running) created at:\n")
	sb.WriteString("  acme/inventory/internal/worker.(*Worker).Start()\n")
	sb.WriteString("      /app/internal/worker/worker.go:42 +0x78\n")
	for i := 0; i < 10; i++ { sb.WriteString(fmt.Sprintf("testing.go:%d +0x%x\n", 1000+i, 0x50+i)) }
	sb.WriteString("==================\n--- FAIL: TestProcessJobs_Race (0.05s)\n")
	sb.WriteString("=== RUN   TestSyncStock\n--- PASS: TestSyncStock (0.02s)\n")
	sb.WriteString("=== RUN   TestSendNotification\n--- FAIL: TestSendNotification (0.01s)\n")
	sb.WriteString("    notifications_test.go:18: duplicate notification sent for order 42\n")
	sb.WriteString("    notifications_test.go:19: expected idempotent behavior, got 2 sends\n")
	sb.WriteString("=== RUN   TestRetryBackoff\n--- PASS: TestRetryBackoff (0.01s)\n")
	sb.WriteString("=== RUN   TestShouldRetry\n--- PASS: TestShouldRetry (0.01s)\n")
	sb.WriteString("FAIL\tacme/inventory/internal/worker\t0.20s\n")
	return sb.String()
}

func generateBuildLog() string {
	var sb strings.Builder
	sb.WriteString("# acme/inventory/internal/api\n")
	sb.WriteString("internal/api/products.go:78:42: cannot use body.Delta (variable of type float64) as int value in argument to store.UpdateStock\n")
	sb.WriteString("# acme/inventory/internal/auth\n")
	sb.WriteString("internal/auth/service.go:35:47: cannot use s.tokenTTL (variable of type int) as time.Duration value in argument to time.Duration\n")
	sb.WriteString("\n--- Stack trace during build analysis ---\n")
	sb.WriteString("goroutine 1 [running]:\n")
	sb.WriteString("cmd/compile/internal/gc.Main(0xc0000b2000)\n")
	sb.WriteString("\t/usr/local/go/src/cmd/compile/internal/gc/main.go:350 +0x1a5\n")
	for i := 0; i < 15; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 300+i, 0x10+i)) }
	sb.WriteString("\nexit status 2\n")
	return sb.String()
}

func generateLintLog() string {
	var sb strings.Builder
	sb.WriteString("internal/api/orders.go:52:47: parameter 'authSvc' has type 'interface{}', consider using a concrete type (revive)\n")
	sb.WriteString("internal/api/orders.go:58:47: parameter 'authSvc' has type 'interface{}', consider using a concrete type (revive)\n")
	sb.WriteString("internal/auth/service.go:33:1: cyclomatic complexity 1 of function GenerateToken is low, consider inlining (gocyclo)\n")
	sb.WriteString("internal/auth/service.go:39:1: function ValidateToken always returns an error, dead code after return (staticcheck)\n")
	sb.WriteString("internal/db/store.go:57:3: error return value of rows.Close is not checked (errcheck)\n")
	sb.WriteString("internal/db/orders.go:15:2: error return value of tx.Rollback is not checked (errcheck)\n")
	sb.WriteString("internal/worker/worker.go:68:1: function processJobs has a data race on shared state (govet)\n")
	sb.WriteString("internal/worker/notifications.go:15:1: function SendNotification is not idempotent (custom-lint)\n")
	sb.WriteString("internal/models/order.go:42:1: method CalculateTotal mutates receiver, consider returning new value (revive)\n")
	for i := 0; i < 20; i++ { sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 500+i, 0x25+i)) }
	sb.WriteString("\n12 issues found (3 errors, 9 warnings)\n")
	return sb.String()
}

func generateAPIResponseLog() string {
	type product struct {
		ID        int    `json:"id"`
		Name      string `json:"name"`
		SKU       string `json:"sku"`
		Price     int    `json:"price_cents"`
		Category  string `json:"category"`
		StockQty  int    `json:"stock_qty"`
		CreatedAt string `json:"created_at"`
	}
	products := make([]product, 50)
	categories := []string{"electronics", "clothing", "food", "toys", "books"}
	for i := range products {
		products[i] = product{
			ID: i + 1, Name: fmt.Sprintf("Product %d - %s Edition", i+1, categories[i%len(categories)]),
			SKU: fmt.Sprintf("SKU-%06d", i+1), Price: (i+1)*999 + 50,
			Category: categories[i%len(categories)], StockQty: (i * 7) % 100,
			CreatedAt: fmt.Sprintf("2024-01-%02dT10:00:00Z", (i%28)+1),
		}
	}
	data, _ := json.MarshalIndent(products, "", "  ")
	return string(data)
}

func generateCoverageReport() string {
	var sb strings.Builder
	sb.WriteString("mode: atomic\n")
	pkgs := []struct{ pkg string; cov float64 }{
		{"internal/config", 92.3}, {"internal/models", 78.4}, {"internal/db", 45.2},
		{"internal/api", 38.7}, {"internal/auth", 31.5}, {"internal/worker", 22.1},
	}
	for _, p := range pkgs {
		sb.WriteString(fmt.Sprintf("acme/inventory/%s\tcoverage: %.1f%% of statements\n", p.pkg, p.cov))
	}
	sb.WriteString("\ntotal:\t(statements)\t48.2%\n\nWARNING: coverage below 50% threshold\nPackages below threshold:\n")
	for _, p := range pkgs {
		if p.cov < 50.0 { sb.WriteString(fmt.Sprintf("  - %s: %.1f%%\n", p.pkg, p.cov)) }
	}
	return sb.String()
}

func generateDeployErrorLog() string {
	var sb strings.Builder
	sb.WriteString("2024-06-15T14:32:01Z [deploy] Starting deployment to staging...\n")
	sb.WriteString("2024-06-15T14:32:05Z [deploy] Building Docker image...\n")
	sb.WriteString("2024-06-15T14:32:18Z [deploy] Image built: inventory:sha-a1b2c3d\n")
	sb.WriteString("2024-06-15T14:32:20Z [deploy] Pushing to registry...\n")
	sb.WriteString("2024-06-15T14:32:25Z [deploy] Running health checks...\n")
	sb.WriteString("2024-06-15T14:32:30Z [deploy] ERROR: health check failed after 3 retries\n")
	sb.WriteString("2024-06-15T14:32:30Z [deploy] Container logs:\n")
	sb.WriteString("  panic: runtime error: invalid memory address or nil pointer dereference\n")
	sb.WriteString("  goroutine 1 [running]:\n")
	sb.WriteString("  acme/inventory/internal/auth.(*Service).ValidateToken(...)\n")
	sb.WriteString("      /app/internal/auth/service.go:39 +0x85\n")
	sb.WriteString("  acme/inventory/internal/api.init()\n")
	sb.WriteString("      /app/internal/api/router.go:15 +0x120\n")
	for i := 0; i < 25; i++ { sb.WriteString(fmt.Sprintf("  runtime/proc.go:%d +0x%x\n", 600+i, 0x30+i)) }
	sb.WriteString("\n2024-06-15T14:32:31Z [deploy] DEPLOYMENT FAILED\n")
	sb.WriteString("2024-06-15T14:32:31Z [deploy] Rolling back to previous version...\n")
	sb.WriteString("2024-06-15T14:32:35Z [deploy] Rollback complete. Service stable on previous version.\n")
	sb.WriteString("2024-06-15T14:32:35Z [deploy] ACTION REQUIRED: Fix auth/service.go ValidateToken before next deploy\n")
	return sb.String()
}

// ---------------------------------------------------------------------------
// .env loader
// ---------------------------------------------------------------------------

func loadEnv(t *testing.T) (apiKey, model string) {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		envPath := filepath.Join(dir, ".env")
		if f, err := os.Open(envPath); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") { continue }
				if k, v, ok := strings.Cut(line, "="); ok {
					switch strings.TrimSpace(k) {
					case "OPENROUTER_API_KEY": apiKey = strings.TrimSpace(v)
					case "OPENROUTER_MODEL": model = strings.TrimSpace(v)
					}
				}
			}
			f.Close()
			break
		}
		dir = filepath.Dir(dir)
	}
	if model == "" { model = "openai/gpt-4o-mini" }
	return
}

// ---------------------------------------------------------------------------
// OpenRouter chat completion types
// ---------------------------------------------------------------------------

type orToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type orTool struct {
	Type     string         `json:"type"`
	Function orToolFunction `json:"function"`
}

type orToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type orMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []orToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type orRequest struct {
	Model    string      `json:"model"`
	Messages []orMessage `json:"messages"`
	Tools    []orTool    `json:"tools,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type orUsage struct {
	PromptTokens        int                  `json:"prompt_tokens"`
	CompletionTokens    int                  `json:"completion_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *promptTokensDetails `json:"prompt_tokens_details,omitempty"`
	Cost                float64              `json:"cost,omitempty"`
	CacheDiscount       float64              `json:"cache_discount,omitempty"`
}

type orChoice struct {
	Message      orMessage `json:"message"`
	FinishReason string    `json:"finish_reason"`
}

type orResponse struct {
	Choices []orChoice `json:"choices"`
	Usage   orUsage    `json:"usage"`
}

// ---------------------------------------------------------------------------
// Local tool execution
// ---------------------------------------------------------------------------

func executeLocalTool(workspaceDir, toolName, argsJSON string) (string, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse tool args: %w", err)
	}

	switch toolName {
	case "list_dir":
		path := args["path"]
		if path == "" { path = "." }
		absPath := filepath.Join(workspaceDir, path)
		entries, err := os.ReadDir(absPath)
		if err != nil { return fmt.Sprintf("error: %v", err), nil }
		var sb strings.Builder
		for _, e := range entries {
			kind := "file"
			if e.IsDir() { kind = "dir" }
			info, _ := e.Info()
			size := int64(0)
			if info != nil { size = info.Size() }
			sb.WriteString(fmt.Sprintf("%s (%s, %d bytes)\n", e.Name(), kind, size))
		}
		return sb.String(), nil

	case "read_file":
		path := args["path"]
		absPath := filepath.Join(workspaceDir, path)
		data, err := os.ReadFile(absPath)
		if err != nil { return fmt.Sprintf("error: %v", err), nil }
		return string(data), nil

	case "run_command":
		cmd := args["command"]
		switch {
		case strings.Contains(cmd, "go test") && strings.Contains(cmd, "db"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "test_db.log"))
			return string(data), nil
		case strings.Contains(cmd, "go test") && strings.Contains(cmd, "api"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "test_api.log"))
			return string(data), nil
		case strings.Contains(cmd, "go test") && strings.Contains(cmd, "auth"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "test_auth.log"))
			return string(data), nil
		case strings.Contains(cmd, "go test") && strings.Contains(cmd, "worker"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "test_worker.log"))
			return string(data), nil
		case strings.Contains(cmd, "go test"):
			// Full test suite: concatenate all test logs
			var sb strings.Builder
			for _, name := range []string{"test_db.log", "test_api.log", "test_auth.log", "test_worker.log"} {
				data, err := os.ReadFile(filepath.Join(workspaceDir, "logs", name))
				if err == nil { sb.WriteString(string(data)); sb.WriteString("\n") }
			}
			return sb.String(), nil
		case strings.Contains(cmd, "go build"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "build_output.log"))
			return string(data), nil
		case strings.Contains(cmd, "lint"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "lint_output.log"))
			return string(data), nil
		case strings.Contains(cmd, "coverage") || strings.Contains(cmd, "cover"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "coverage.txt"))
			return string(data), nil
		case strings.Contains(cmd, "curl") || (strings.Contains(cmd, "cat") && strings.Contains(cmd, "api_response")):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "api_response.json"))
			return string(data), nil
		default:
			return fmt.Sprintf("command executed: %s\nexit code: 0", cmd), nil
		}

	default:
		return fmt.Sprintf("unknown tool: %s", toolName), nil
	}
}

// ---------------------------------------------------------------------------
// Agent loop
// ---------------------------------------------------------------------------

// RunResult holds the metrics from a single agent run.
type RunResult struct {
	PromptTokens     int
	CompletionTokens int
	WallClockMs      int64
	FinalAnswer      string
	Turns            int
	ToolOutputBytes  int
	// Billing
	TotalCostUSD     float64
	CacheDiscountUSD float64
	CachedTokens     int
	CacheWriteTokens int
}

func agentLoop(t *testing.T, apiKey, model, workspaceDir string, hooked bool) RunResult {
	t.Helper()

	tools := []orTool{
		{
			Type: "function",
			Function: orToolFunction{
				Name:        "list_dir",
				Description: "List the contents of a directory in the workspace. Returns file names, types, and sizes.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]string{"type": "string", "description": "Relative path from workspace root. Use '.' for root."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: orToolFunction{
				Name:        "read_file",
				Description: "Read the full contents of a file in the workspace.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: orToolFunction{
				Name:        "run_command",
				Description: "Run a shell command in the workspace. Returns stdout and stderr. Supports: go test ./path/to/pkg, go build ./..., golangci-lint run, go test -cover, cat logs/filename.log, curl, etc.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]string{"type": "string", "description": "The shell command to execute."},
					},
					"required": []string{"command"},
				},
			},
		},
	}

	systemPrompt := `You are a senior software engineer performing a thorough codebase audit and debugging session. You have three tools: list_dir, read_file, and run_command.

IMPORTANT RULES:
- You MUST call only ONE tool per turn. Do not batch multiple tool calls in a single response. After each tool call, wait for the result before deciding your next action.
- Be thorough and systematic — explore every directory, read every source file, run all available commands.

Your task is a comprehensive codebase audit. Follow these steps IN ORDER:

PHASE 1 — DISCOVERY (explore all directories and read every file):
1. List the root directory
2. Read README.md and Makefile to understand the project structure
3. List each subdirectory (internal/config, internal/models, internal/db, internal/api, internal/auth, internal/worker, logs)
4. Read EVERY source file in EVERY package, one file at a time
5. Read the .env.example file

PHASE 2 — RUN DIAGNOSTICS:
6. Run: go build ./...
7. Run: go test ./internal/db/...
8. Run: go test ./internal/api/...
9. Run: go test ./internal/auth/...
10. Run: go test ./internal/worker/...
11. Run: golangci-lint run ./...

PHASE 3 — INVESTIGATE LOGS:
12. Read each log file in the logs/ directory one at a time (there are 9 log files)

PHASE 4 — FINAL REPORT:
13. Output a comprehensive diagnostic report covering:
    - Architecture overview with dependency graph
    - Bug inventory (list every bug found with file:line references)
    - Test failure root cause analysis for each package
    - Build error analysis
    - Lint findings
    - Code coverage analysis
    - Deployment failure analysis
    - Prioritized fix recommendations

Remember: ONE tool call per turn. Be methodical.`

	messages := []orMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Please perform a thorough codebase audit of this project. Remember to use only one tool call at a time and explore systematically."},
	}

	client := &http.Client{Timeout: 60 * time.Second}
	var result RunResult
	start := time.Now()

	const maxTurns = 50

	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1

		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, err := json.Marshal(reqBody)
		if err != nil { t.Fatalf("turn %d: marshal request: %v", turn, err) }

		req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-hook-benchmark")

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("turn %d: API request failed (ending run): %v", turn, err)
			break
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Logf("turn %d: API returned %d (ending run): %s", turn, resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
			break
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			t.Logf("turn %d: unparseable API response (ending run, likely context overflow)", turn)
			break
		}

		result.PromptTokens += orResp.Usage.PromptTokens
		result.CompletionTokens += orResp.Usage.CompletionTokens
		result.TotalCostUSD += orResp.Usage.Cost
		result.CacheDiscountUSD += orResp.Usage.CacheDiscount
		if orResp.Usage.PromptTokensDetails != nil {
			result.CachedTokens += orResp.Usage.PromptTokensDetails.CachedTokens
			result.CacheWriteTokens += orResp.Usage.PromptTokensDetails.CacheWriteTokens
		}

		if len(orResp.Choices) == 0 {
			t.Logf("turn %d: no choices in response (ending run)", turn)
			break
		}

		choice := orResp.Choices[0]
		assistantMsg := choice.Message
		messages = append(messages, assistantMsg)

		// If no tool calls, we have the final answer
		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			result.FinalAnswer = assistantMsg.Content
			break
		}

		// Execute each tool call
		for _, tc := range assistantMsg.ToolCalls {
			rawOutput, err := executeLocalTool(workspaceDir, tc.Function.Name, tc.Function.Arguments)
			if err != nil { rawOutput = fmt.Sprintf("tool error: %v", err) }

			toolOutput := rawOutput

			// In hooked mode, pipe through Pi-Coder post-tool compaction
			if hooked {
				hookInput := PiCoderPostToolInput{ToolName: tc.Function.Name, ToolOutput: rawOutput}
				hookJSON, _ := json.Marshal(hookInput)
				var hookOut bytes.Buffer
				if err := HandlePiCoderPostTool(bytes.NewReader(hookJSON), &hookOut, nil); err == nil {
					var hookResp PiCoderPostToolOutput
					if err := json.Unmarshal(hookOut.Bytes(), &hookResp); err == nil {
						if s, ok := hookResp.ToolOutput.(string); ok {
							toolOutput = s
						}
					}
				}
			}

			result.ToolOutputBytes += len(toolOutput)
			messages = append(messages, orMessage{
				Role: "tool", Content: toolOutput, ToolCallID: tc.ID,
			})
		}

		t.Logf("turn %d: %d tool calls executed (hooked=%v)", turn, len(assistantMsg.ToolCalls), hooked)
	}

	result.WallClockMs = time.Since(start).Milliseconds()
	return result
}

// ---------------------------------------------------------------------------
// Helpers for tzro-powered agent loop
// ---------------------------------------------------------------------------

// buildTzroBinary compiles the tzro binary to a temp directory and returns its path.
func buildTzroBinary(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "tzro")

	// Find the repo root (we're in pkg/hooks/)
	repoRoot, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(repoRoot, "cmd", "tzro", "main.go")); err == nil {
			break
		}
		repoRoot = filepath.Dir(repoRoot)
	}

	buildCmd := exec.Command("go", "build", "-o", binPath, filepath.Join(repoRoot, "cmd", "tzro"))
	buildCmd.Dir = repoRoot
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build tzro binary: %v\n%s", err, string(out))
	}

	t.Logf("tzro binary built: %s", binPath)
	return binPath
}

// executeLocalToolWithTzro handles tool calls, including tzro_probe, tzro_skeleton, tzro_expand.
func executeLocalToolWithTzro(tzroBin, workspaceDir, toolName, argsJSON string) (string, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse tool args: %w", err)
	}

	switch toolName {
	case "tzro_probe":
		query := args["query"]
		cmd := exec.Command(tzroBin, "probe", query)
		cmd.Dir = workspaceDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("probe error: %v\n%s", err, string(out)), nil
		}
		return string(out), nil

	case "tzro_skeleton":
		filePath := args["file"]
		absPath := filePath
		if !filepath.IsAbs(filePath) {
			absPath = filepath.Join(workspaceDir, filePath)
		}
		cmd := exec.Command(tzroBin, "skeleton", absPath)
		cmd.Dir = workspaceDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("skeleton error: %v\n%s", err, string(out)), nil
		}
		return string(out), nil

	case "tzro_expand":
		hash := args["hash"]
		cmd := exec.Command(tzroBin, "expand", hash)
		cmd.Dir = workspaceDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("expand error: %v\n%s", err, string(out)), nil
		}
		return string(out), nil

	default:
		return executeLocalTool(workspaceDir, toolName, argsJSON)
	}
}

// agentLoopWithTzro runs an agent loop with tzro discovery tools available.
func agentLoopWithTzro(t *testing.T, apiKey, model, tzroBin, workspaceDir string) RunResult {
	t.Helper()

	tools := []orTool{
		{Type: "function", Function: orToolFunction{
			Name: "tzro_probe",
			Description: "Search for symbols, functions, types, or patterns across the entire codebase. Returns exact file:line locations in <500 tokens. USE THIS FIRST.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]string{"type": "string", "description": "Search query."},
			}, "required": []string{"query"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name: "tzro_skeleton",
			Description: "Get a compressed overview of a source file (imports + signatures, bodies elided as hash tags). 70-90% smaller than full file.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"file": map[string]string{"type": "string", "description": "Path to source file (relative to workspace root)."},
			}, "required": []string{"file"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name: "tzro_expand",
			Description: "Retrieve a function body by its hash from skeleton output (e.g. '// [body elided: #abc123]'). Returns only ~20 lines.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"hash": map[string]string{"type": "string", "description": "Hash from skeleton elision comment."},
			}, "required": []string{"hash"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name: "read_file",
			Description: "Read the full contents of a file. Use for READMEs, configs, log files. Prefer tzro_skeleton for source code.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
			}, "required": []string{"path"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name: "run_command",
			Description: "Run a shell command (go test, go build, etc.).",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"command": map[string]string{"type": "string", "description": "Shell command to execute."},
			}, "required": []string{"command"}},
		}},
	}

	systemPrompt := `You are a senior software engineer with efficient codebase exploration tools:

- tzro_probe: Search the entire codebase for symbols/patterns. USE THIS FIRST.
- tzro_skeleton: Compressed file overview (signatures only, bodies elided). 70-90% smaller.
- tzro_expand: Retrieve a specific function body by hash from skeleton output.
- read_file: Full file read (use for READMEs, configs, logs — prefer skeleton for source).
- run_command: Run shell commands (go test, go build, etc.)

IMPORTANT: ONE tool call per turn.

Task: Efficiently diagnose all issues in this codebase.
1. tzro_probe to discover structure
2. tzro_skeleton on key files
3. tzro_expand only when needed
4. read_file for READMEs and logs
5. run_command for tests/builds
6. Final diagnostic report`

	messages := []orMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Efficiently diagnose this codebase using the tzro discovery tools. One tool call per turn."},
	}

	client := &http.Client{Timeout: 60 * time.Second}
	var result RunResult
	start := time.Now()
	const maxTurns = 30

	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1
		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-tools-benchmark")

		resp, err := client.Do(req)
		if err != nil { t.Logf("turn %d: request failed (ending): %v", turn, err); break }
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Logf("turn %d: API %d (ending): %s", turn, resp.StatusCode, string(respBody[:min(len(respBody), 300)]))
			break
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			t.Logf("turn %d: unparseable (ending)", turn); break
		}

		result.PromptTokens += orResp.Usage.PromptTokens
		result.CompletionTokens += orResp.Usage.CompletionTokens
		result.TotalCostUSD += orResp.Usage.Cost
		result.CacheDiscountUSD += orResp.Usage.CacheDiscount
		if orResp.Usage.PromptTokensDetails != nil {
			result.CachedTokens += orResp.Usage.PromptTokensDetails.CachedTokens
			result.CacheWriteTokens += orResp.Usage.PromptTokensDetails.CacheWriteTokens
		}

		if len(orResp.Choices) == 0 { t.Logf("turn %d: no choices (ending)", turn); break }
		choice := orResp.Choices[0]
		assistantMsg := choice.Message
		messages = append(messages, assistantMsg)

		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			result.FinalAnswer = assistantMsg.Content
			break
		}

		for _, tc := range assistantMsg.ToolCalls {
			rawOutput, err := executeLocalToolWithTzro(tzroBin, workspaceDir, tc.Function.Name, tc.Function.Arguments)
			if err != nil { rawOutput = fmt.Sprintf("tool error: %v", err) }

			// Apply hooks
			toolOutput := rawOutput
			hookInput := PiCoderPostToolInput{ToolName: tc.Function.Name, ToolOutput: rawOutput}
			hookJSON, _ := json.Marshal(hookInput)
			var hookOut bytes.Buffer
			if err := HandlePiCoderPostTool(bytes.NewReader(hookJSON), &hookOut, nil); err == nil {
				var hookResp PiCoderPostToolOutput
				if err := json.Unmarshal(hookOut.Bytes(), &hookResp); err == nil {
					if s, ok := hookResp.ToolOutput.(string); ok { toolOutput = s }
				}
			}

			result.ToolOutputBytes += len(toolOutput)
			messages = append(messages, orMessage{Role: "tool", Content: toolOutput, ToolCallID: tc.ID})
		}

		t.Logf("turn %d: [%s] (cost=$%.6f)", turn, assistantMsg.ToolCalls[0].Function.Name, orResp.Usage.Cost)
	}

	result.WallClockMs = time.Since(start).Milliseconds()
	return result
}

func min(a, b int) int {
	if a < b { return a }
	return b
}

// ---------------------------------------------------------------------------
// Main E2E test: 3-way comparison (Baseline / Hooked / Full Tzro)
// ---------------------------------------------------------------------------

func TestPiCoderE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E benchmark in short mode")
	}

	apiKey, model := loadEnv(t)
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set in .env — skipping E2E benchmark")
	}

	// Build tzro binary for probe/skeleton/expand
	tzroBin := buildTzroBinary(t)

	workspace := scaffoldWorkspace(t)
	t.Logf("workspace: %s", workspace)
	t.Logf("model: %s", model)
	t.Logf("tzro binary: %s", tzroBin)

	// Pre-index: run tzro skeleton on all .go files to populate the hash store
	t.Log("Pre-indexing workspace with tzro skeleton...")
	err := filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil { return err }
		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			cmd := exec.Command(tzroBin, "skeleton", path)
			cmd.Dir = workspace
			cmd.CombinedOutput()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("pre-index failed: %v", err)
	}
	t.Log("Pre-indexing complete")

	// --- Run A: Baseline (no hooks, brute-force) ---
	t.Log("=== Run A: Baseline (raw, no hooks) ===")
	baseline := agentLoop(t, apiKey, model, workspace, false)

	// --- Run B: Tzro-Hooked (post-tool compaction only) ---
	t.Log("=== Run B: Tzro-Hooked (Pi-Coder post-tool compaction) ===")
	hooked := agentLoop(t, apiKey, model, workspace, true)

	// --- Run C: Full Tzro (probe + skeleton + expand + hooks) ---
	t.Log("=== Run C: Full Tzro (probe + skeleton + expand + hooks) ===")
	fullTzro := agentLoopWithTzro(t, apiKey, model, tzroBin, workspace)

	// --- Savings calculations ---
	pct := func(base, val int) float64 {
		if base == 0 { return 0 }
		return (1.0 - float64(val)/float64(base)) * 100
	}
	pctF := func(base, val float64) float64 {
		if base == 0 { return 0 }
		return (1.0 - val/base) * 100
	}
	pctMs := func(base, val int64) float64 {
		if base == 0 { return 0 }
		return (1.0 - float64(val)/float64(base)) * 100
	}

	// --- 3-way comparison table ---
	t.Logf("\n"+
		"┌──────────────┬────────────┬────────────┬──────────────┬──────────────┬──────────┬───────┐\n"+
		"│ Mode         │ Prompt Tok │ Compl Tok  │ Tool Out (B) │    Cost ($)  │ Wall (s) │ Turns │\n"+
		"├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────┼───────┤\n"+
		"│ Baseline     │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │\n"+
		"│ Hooked       │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │\n"+
		"│ Full Tzro    │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │\n"+
		"├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────┼───────┤\n"+
		"│ Hooked Δ     │ %9.1f%% │          — │ %11.1f%% │ %11.1f%% │ %7.1f%% │     — │\n"+
		"│ Full Tzro Δ  │ %9.1f%% │          — │ %11.1f%% │ %11.1f%% │ %7.1f%% │     — │\n"+
		"└──────────────┴────────────┴────────────┴──────────────┴──────────────┴──────────┴───────┘",
		baseline.PromptTokens, baseline.CompletionTokens, baseline.ToolOutputBytes, baseline.TotalCostUSD, float64(baseline.WallClockMs)/1000, baseline.Turns,
		hooked.PromptTokens, hooked.CompletionTokens, hooked.ToolOutputBytes, hooked.TotalCostUSD, float64(hooked.WallClockMs)/1000, hooked.Turns,
		fullTzro.PromptTokens, fullTzro.CompletionTokens, fullTzro.ToolOutputBytes, fullTzro.TotalCostUSD, float64(fullTzro.WallClockMs)/1000, fullTzro.Turns,
		pct(baseline.PromptTokens, hooked.PromptTokens), pct(baseline.ToolOutputBytes, hooked.ToolOutputBytes), pctF(baseline.TotalCostUSD, hooked.TotalCostUSD), pctMs(baseline.WallClockMs, hooked.WallClockMs),
		pct(baseline.PromptTokens, fullTzro.PromptTokens), pct(baseline.ToolOutputBytes, fullTzro.ToolOutputBytes), pctF(baseline.TotalCostUSD, fullTzro.TotalCostUSD), pctMs(baseline.WallClockMs, fullTzro.WallClockMs),
	)

	// Billing detail
	t.Logf("\nBilling:")
	t.Logf("  Baseline:  cost=$%.6f  cached=%d  cache_write=%d",
		baseline.TotalCostUSD, baseline.CachedTokens, baseline.CacheWriteTokens)
	t.Logf("  Hooked:    cost=$%.6f  cached=%d  cache_write=%d",
		hooked.TotalCostUSD, hooked.CachedTokens, hooked.CacheWriteTokens)
	t.Logf("  Full Tzro: cost=$%.6f  cached=%d  cache_write=%d",
		fullTzro.TotalCostUSD, fullTzro.CachedTokens, fullTzro.CacheWriteTokens)

	// Answer lengths
	results := map[string]RunResult{"baseline": baseline, "hooked": hooked, "full_tzro": fullTzro}
	for name, r := range results {
		if r.FinalAnswer != "" {
			t.Logf("%s answer: %d chars", name, len(r.FinalAnswer))
		} else {
			t.Logf("WARNING: %s did not produce final answer (%d turns)", name, r.Turns)
		}
	}
}

