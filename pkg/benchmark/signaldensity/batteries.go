package signaldensity

import (
	"fmt"
	"strconv"
	"strings"

	"tzro/pkg/ast"
	"tzro/pkg/compactor"
	"tzro/pkg/store"
)

// LoadAllTaskCases constructs the full benchmark battery across Tier 1 and Tier 2.
func LoadAllTaskCases(s *store.Store) ([]TaskCase, error) {
	var allCases []TaskCase

	// 1. AST Skeletonization Battery (5 cases)
	skelCases, err := loadASTSkeletonCases(s)
	if err != nil {
		return nil, fmt.Errorf("load ast skeleton cases: %w", err)
	}
	allCases = append(allCases, skelCases...)

	// 2. Log & Stack Trace Compactor Battery (5 cases)
	compCases, err := loadCompactorCases(s)
	if err != nil {
		return nil, fmt.Errorf("load compactor cases: %w", err)
	}
	allCases = append(allCases, compCases...)

	// 3. Smart JSON Crusher Battery (5 cases)
	jsonCases, err := loadSmartJSONCases()
	if err != nil {
		return nil, fmt.Errorf("load smart json cases: %w", err)
	}
	allCases = append(allCases, jsonCases...)

	// 4. Tabular SQL Ingestion Battery (5 cases)
	tabCases, err := loadTabularSQLCases(s)
	if err != nil {
		return nil, fmt.Errorf("load tabular sql cases: %w", err)
	}
	allCases = append(allCases, tabCases...)

	// 5. Mini-Macro Coding Tasks (3 cases)
	macroCases, err := loadMiniMacroCases(s)
	if err != nil {
		return nil, fmt.Errorf("load mini macro cases: %w", err)
	}
	allCases = append(allCases, macroCases...)

	return allCases, nil
}

// ---------------------------------------------------------------------------
// 1. AST Skeletonization Battery (5 cases)
// ---------------------------------------------------------------------------

func loadASTSkeletonCases(s *store.Store) ([]TaskCase, error) {
	// Case 1: Cache Driver Interface
	rawGoCache := `package cache

import (
	"errors"
	"sync"
	"time"
)

var ErrKeyNotFound = errors.New("cache: key not found")

type Driver interface {
	Get(key string) ([]byte, error)
	Set(key string, val []byte, ttl time.Duration) error
	Delete(key string) error
}

type MemoryStore struct {
	mu    sync.RWMutex
	items map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		items: make(map[string][]byte),
	}
}

func (m *MemoryStore) Get(key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.items[key]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return v, nil
}

func (m *MemoryStore) Set(key string, val []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[key] = val
	return nil
}

func (m *MemoryStore) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	return nil
}
`
	res1, _ := ast.Skeletonize("cache.go", []byte(rawGoCache), s, "")
	skel1 := rawGoCache
	if res1 != nil && res1.SkeletonCode != "" {
		skel1 = res1.SkeletonCode
	}

	// Case 2: TypeScript Service & Repo
	rawTSRepo := `export interface User {
  id: string;
  email: string;
  name: string;
}

export interface UserRepository {
  insert(user: User): Promise<void>;
  findById(id: string): Promise<User | null>;
}

export class UserService {
  constructor(private userRepo: UserRepository) {}

  async createUser(id: string, email: string, name: string): Promise<void> {
    const existing = await this.userRepo.findById(id);
    if (existing) {
      throw new Error("ErrUserExists");
    }
    await this.userRepo.insert({ id, email, name });
  }

  async getUser(id: string): Promise<User | null> {
    return this.userRepo.findById(id);
  }
}
`
	res2, _ := ast.Skeletonize("service.ts", []byte(rawTSRepo), s, "")
	skel2 := rawTSRepo
	if res2 != nil && res2.SkeletonCode != "" {
		skel2 = res2.SkeletonCode
	}

	// Case 3: Config Builder Options Pattern
	rawGoConfig := `package config

import (
	"time"
)

type Options struct {
	Timeout time.Duration
	Retries int
}

func DefaultOptions() *Options {
	return &Options{
		Timeout: 30 * time.Second,
		Retries: 3,
	}
}

type ConfigBuilder struct {
	opts *Options
}

func NewConfigBuilder() *ConfigBuilder {
	return &ConfigBuilder{
		opts: DefaultOptions(),
	}
}

func (cb *ConfigBuilder) WithTimeout(d time.Duration) *ConfigBuilder {
	cb.opts.Timeout = d
	return cb
}
`
	res3, _ := ast.Skeletonize("config.go", []byte(rawGoConfig), s, "")
	skel3 := rawGoConfig
	if res3 != nil && res3.SkeletonCode != "" {
		skel3 = res3.SkeletonCode
	}

	// Case 4: Generic Event Dispatcher (TS)
	rawTSEvent := `export interface EventPayload {
  eventName: string;
  timestamp: number;
}

export interface EventHandler<T extends EventPayload> {
  handle(event: T): void;
}

export class EventDispatcher<T extends EventPayload> {
  private handlers: EventHandler<T>[] = [];

  register(handler: EventHandler<T>): void {
    this.handlers.push(handler);
  }

  dispatch(event: T): void {
    for (const h of this.handlers) {
      h.handle(event);
    }
  }
}
`
	res4, _ := ast.Skeletonize("event.ts", []byte(rawTSEvent), s, "")
	skel4 := rawTSEvent
	if res4 != nil && res4.SkeletonCode != "" {
		skel4 = res4.SkeletonCode
	}

	// Case 5: HTTP Middleware & Auth
	rawGoAuth := `package middleware

import (
	"context"
	"net/http"
)

type contextKey string

const ContextKeyClaims = contextKey("user_claims")

type Claims struct {
	Subject string
	Role    string
}

func AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		if token == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ContextKeyClaims, &Claims{Subject: "usr_123", Role: "admin"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
`
	res5, _ := ast.Skeletonize("auth.go", []byte(rawGoAuth), s, "")
	skel5 := rawGoAuth
	if res5 != nil && res5.SkeletonCode != "" {
		skel5 = res5.SkeletonCode
	}

	return []TaskCase{
		{
			ID:          "ast_1_cache_driver",
			Name:        "Cache Driver Interface Contract",
			Tier:        TierMicro,
			Battery:     BatteryASTSkeleton,
			Primitive:   PrimitiveSkeleton,
			Description: "Identify Driver interface methods and their return signatures.",
			PromptRaw:   fmt.Sprintf("Based on this source file:\n```go\n%s\n```\nWhat methods does the `Driver` interface define, and what are their exact return types?", rawGoCache),
			PromptTzro:  fmt.Sprintf("Based on this skeletonized file:\n```go\n%s\n```\nWhat methods does the `Driver` interface define, and what are their exact return types?", skel1),
			MatcherType: MatcherContains,
			Expected:    "Get && Set && Delete",
		},
		{
			ID:          "ast_2_service_repo",
			Name:        "TypeScript Service & Repository Contract",
			Tier:        TierMicro,
			Battery:     BatteryASTSkeleton,
			Primitive:   PrimitiveSkeleton,
			Description: "Identify UserRepository methods and User entity fields.",
			PromptRaw:   fmt.Sprintf("Based on this TypeScript file:\n```typescript\n%s\n```\nWhat methods does the `UserRepository` interface define, and what fields does the `User` interface have?", rawTSRepo),
			PromptTzro:  fmt.Sprintf("Based on this skeletonized TypeScript file:\n```typescript\n%s\n```\nWhat methods does the `UserRepository` interface define, and what fields does the `User` interface have?", skel2),
			MatcherType: MatcherContains,
			Expected:    "findById && insert && delete && id && email && name",
		},
		{
			ID:          "ast_3_config_builder",
			Name:        "Config Builder Method Signature & Options Struct",
			Tier:        TierMicro,
			Battery:     BatteryASTSkeleton,
			Primitive:   PrimitiveSkeleton,
			Description: "Determine ConfigBuilder method signature and Options struct fields.",
			PromptRaw:   fmt.Sprintf("Based on this file:\n```go\n%s\n```\nWhat method does `ConfigBuilder` define, and what fields are on the `Options` struct?", rawGoConfig),
			PromptTzro:  fmt.Sprintf("Based on this skeletonized file:\n```go\n%s\n```\nWhat method does `ConfigBuilder` define, and what fields are on the `Options` struct?", skel3),
			MatcherType: MatcherContains,
			Expected:    "WithTimeout && Timeout && Retries",
		},
		{
			ID:          "ast_4_generic_event",
			Name:        "Generic Event Dispatcher Constraints",
			Tier:        TierMicro,
			Battery:     BatteryASTSkeleton,
			Primitive:   PrimitiveSkeleton,
			Description: "Extract generic type constraint and publishing method.",
			PromptRaw:   fmt.Sprintf("Based on this TypeScript file:\n```typescript\n%s\n```\nWhat type parameter does `EventDispatcher<T>` take, and what method is used to publish an event?", rawTSEvent),
			PromptTzro:  fmt.Sprintf("Based on this skeletonized file:\n```typescript\n%s\n```\nWhat type parameter does `EventDispatcher<T>` take, and what method is used to publish an event?", skel4),
			MatcherType: MatcherContains,
			Expected:    "EventPayload && dispatch",
		},
		{
			ID:          "ast_5_auth_middleware",
			Name:        "Auth Middleware Claims Struct & Context Constant",
			Tier:        TierMicro,
			Battery:     BatteryASTSkeleton,
			Primitive:   PrimitiveSkeleton,
			Description: "Extract context key constant, Claims struct fields, and AuthMiddleware return type.",
			PromptRaw:   fmt.Sprintf("Based on this file:\n```go\n%s\n```\nWhat is the value of `ContextKeyClaims`, what fields are on the `Claims` struct, and what is the return type of `AuthMiddleware`?", rawGoAuth),
			PromptTzro:  fmt.Sprintf("Based on this skeletonized file:\n```go\n%s\n```\nWhat is the value of `ContextKeyClaims`, what fields are on the `Claims` struct, and what is the return type of `AuthMiddleware`?", skel5),
			MatcherType: MatcherContains,
			Expected:    "user_claims && Subject && Role && http.Handler",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// 2. Log & Stack Trace Compactor Battery (5 cases)
// ---------------------------------------------------------------------------

func loadCompactorCases(s *store.Store) ([]TaskCase, error) {
	// Case 1: Go test failure with goroutine stack dump
	rawLog1 := `=== RUN   TestTokenBucket_Refill
--- FAIL: TestTokenBucket_Refill (0.05s)
    bucket_test.go:84: expected bucket tokens 10, got 5
goroutine 1 [running]:
runtime/debug.Stack()
	/usr/local/go/src/runtime/debug/stack.go:24 +0x65
testing.tRunner.func1()
	/usr/local/go/src/testing/testing.go:874 +0x353
runtime.gopanic()
	/usr/local/go/src/runtime/panic.go:1044 +0x5a5
testing.tRunner()
	/usr/local/go/src/testing/testing.go:900 +0x147
created by testing.(*T).Run
	/usr/local/go/src/testing/testing.go:943 +0x3bf
goroutine 2 [select]:
net/http/server.go:3000 +0x100
runtime/proc.go:400 +0x20
`
	compacted1 := compactor.CompactLog(rawLog1)

	// Case 2: Node.js unhandled rejection with runtime internals
	rawLog2 := `node:internal/process/promises:288
            triggerUncaughtException(err, true /* fromPromise */);
            ^
Error: Database connection refused
    at Database.connect (src/db/client.ts:42:15)
    at async initApp (src/app.ts:18:3)
    at async start (src/index.ts:10:5)
    at node:internal/modules/cjs/loader:1210:14
    at Module._compile (node:internal/modules/cjs/loader:1250:14)
    at Module._extensions..js (node:internal/modules/cjs/loader:1300:10)
    at Module.load (node:internal/modules/cjs/loader:1110:32)
    at Module._load (node:internal/modules/cjs/loader:960:12)
    at Function.executeUserEntryPoint [as runMain] (node:internal/modules/run_main:83:12)
`
	compacted2 := compactor.CompactLog(rawLog2)

	// Case 3: Python pytest traceback with virtualenv site-packages
	rawLog3 := `==================================== FAILURES ====================================
__________________________________ test_get_user __________________________________

    def test_get_user(client):
>       response = client.get("/users/123")
E       AssertionError: assert 404 == 200

tests/api/test_users.py:67: AssertionError
----------------------------- Captured stderr call -----------------------------
File "/home/user/.venv/lib/python3.11/site-packages/pluggy/_hooks.py", line 513, in _hookexec
File "/home/user/.venv/lib/python3.11/site-packages/pytest/runner.py", line 150, in pytest_runtest_call
File "/home/user/.venv/lib/python3.11/site-packages/pytest/runner.py", line 260, in from_call
`
	compacted3 := compactor.CompactLog(rawLog3)

	// Case 4: Fatal Go panic with runtime dump
	rawLog4 := `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x10a2f4c]

goroutine 19 [running]:
acme/pkg/router.(*Router).ServeHTTP(0x0, 0x1400018a000, 0x140001a4000)
	/workspace/pkg/router/route.go:118 +0x2c
net/http.serverHandler.ServeHTTP(0x140001a2000, 0x1400018a000, 0x140001a4000)
	/usr/local/go/src/net/http/server.go:2938 +0x31c
net/http.(*conn).serve(0x140001a0000, 0x105574040)
	/usr/local/go/src/net/http/server.go:2009 +0x6f4
`
	compacted4 := compactor.CompactLog(rawLog4)

	// Case 5: Multi-test suite with 1 failed test
	rawLog5 := `=== RUN   TestAuth_Valid
--- PASS: TestAuth_Valid (0.01s)
=== RUN   TestAuth_ExpiredToken
--- FAIL: TestAuth_ExpiredToken (0.02s)
    auth_test.go:45: expected status 401, got 200
=== RUN   TestAuth_TamperedToken
--- PASS: TestAuth_TamperedToken (0.01s)
=== RUN   TestAuth_Revoked
--- PASS: TestAuth_Revoked (0.01s)
FAIL	acme/pkg/auth	0.062s
`
	compacted5 := compactor.CompactLog(rawLog5)

	return []TaskCase{
		{
			ID:          "log_1_gotest_failure",
			Name:        "Go Test Failure & Location Extraction",
			Tier:        TierMicro,
			Battery:     BatteryCompactorLogs,
			Primitive:   PrimitiveCompactor,
			Description: "Identify root cause assertion failure, test file, and line number.",
			PromptRaw:   fmt.Sprintf("Analyze this test log:\n```\n%s\n```\nWhat is the root cause failure message, offending file path, and line number?", rawLog1),
			PromptTzro:  fmt.Sprintf("Analyze this compacted test log:\n```\n%s\n```\nWhat is the root cause failure message, offending file path, and line number?", compacted1),
			MatcherType: MatcherContains,
			Expected:    "bucket_test.go && 84 && expected bucket tokens 10, got 5",
		},
		{
			ID:          "log_2_node_rejection",
			Name:        "Node.js Unhandled Rejection Location",
			Tier:        TierMicro,
			Battery:     BatteryCompactorLogs,
			Primitive:   PrimitiveCompactor,
			Description: "Find error message, source file, and line.",
			PromptRaw:   fmt.Sprintf("Analyze this Node.js error trace:\n```\n%s\n```\nWhat is the unhandled rejection error message, and which application source file and line threw it?", rawLog2),
			PromptTzro:  fmt.Sprintf("Analyze this compacted Node.js error trace:\n```\n%s\n```\nWhat is the unhandled rejection error message, and which application source file and line threw it?", compacted2),
			MatcherType: MatcherContains,
			Expected:    "Database connection refused && src/db/client.ts && 42",
		},
		{
			ID:          "log_3_pytest_assertion",
			Name:        "Python Pytest Assertion & Target File",
			Tier:        TierMicro,
			Battery:     BatteryCompactorLogs,
			Primitive:   PrimitiveCompactor,
			Description: "Extract pytest assertion error and line.",
			PromptRaw:   fmt.Sprintf("Analyze this pytest output:\n```\n%s\n```\nWhat exception was raised, and which test function, file, and line failed?", rawLog3),
			PromptTzro:  fmt.Sprintf("Analyze this compacted pytest output:\n```\n%s\n```\nWhat exception was raised, and which test function, file, and line failed?", compacted3),
			MatcherType: MatcherContains,
			Expected:    "AssertionError && test_users.py && 67",
		},
		{
			ID:          "log_4_panic_nil_deref",
			Name:        "Fatal Panic Origin & Nil Dereference",
			Tier:        TierMicro,
			Battery:     BatteryCompactorLogs,
			Primitive:   PrimitiveCompactor,
			Description: "Identify panic reason and application origin file:line.",
			PromptRaw:   fmt.Sprintf("Analyze this panic dump:\n```\n%s\n```\nWhat was the panic value, and which function and source location triggered it?", rawLog4),
			PromptTzro:  fmt.Sprintf("Analyze this compacted panic dump:\n```\n%s\n```\nWhat was the panic value, and which function and source location triggered it?", compacted4),
			MatcherType: MatcherContains,
			Expected:    "nil pointer dereference && route.go && 118",
		},
		{
			ID:          "log_5_multi_test_failure",
			Name:        "Multi-Suite Single Failing Test",
			Tier:        TierMicro,
			Battery:     BatteryCompactorLogs,
			Primitive:   PrimitiveCompactor,
			Description: "Identify failing test case among passing suites.",
			PromptRaw:   fmt.Sprintf("Analyze this test run:\n```\n%s\n```\nWhich test function failed, and what was the failure message?", rawLog5),
			PromptTzro:  fmt.Sprintf("Analyze this compacted test run:\n```\n%s\n```\nWhich test function failed, and what was the failure message?", compacted5),
			MatcherType: MatcherContains,
			Expected:    "TestAuth_ExpiredToken && expected status 401, got 200",
		},
	}, nil
}

// ---------------------------------------------------------------------------
// 3. Smart JSON Crusher Battery (5 cases)
// ---------------------------------------------------------------------------

func loadSmartJSONCases() ([]TaskCase, error) {
	// Case 1: Cloud instances
	var sb1 strings.Builder
	sb1.WriteString("[\n")
	for i := 1; i <= 50; i++ {
		status := "STOPPED"
		zone := "us-east-1b"
		if i == 5 || i == 12 || i == 37 {
			status = "RUNNING"
			zone = "us-east-1a"
		} else if i%3 == 0 {
			status = "RUNNING"
			zone = "us-west-2a"
		}
		sb1.WriteString(fmt.Sprintf("  {\n    \"id\": \"i-%04d\",\n    \"type\": \"t3.medium\",\n    \"zone\": \"%s\",\n    \"status\": \"%s\"\n  }%s\n",
			i, zone, status, commaIf(i < 50)))
	}
	sb1.WriteString("]")
	rawJSON1 := sb1.String()
	crushed1 := compactor.SmartJSONCrusher(rawJSON1)

	// Case 2: Database query rows
	var sb2 strings.Builder
	sb2.WriteString("[\n")
	for i := 1; i <= 40; i++ {
		spend := 100 * i
		if i == 33 {
			spend = 9850
		}
		sb2.WriteString(fmt.Sprintf("  {\n    \"user_id\": \"usr_%04d\",\n    \"total_spend\": %d,\n    \"tier\": \"gold\"\n  }%s\n",
			i, spend, commaIf(i < 40)))
	}
	sb2.WriteString("]")
	rawJSON2 := sb2.String()
	crushed2 := compactor.SmartJSONCrusher(rawJSON2)

	// Case 3: Git commits
	var sb3 strings.Builder
	sb3.WriteString("[\n")
	for i := 1; i <= 30; i++ {
		author := "Dev"
		msg := fmt.Sprintf("commit %d", i)
		hash := fmt.Sprintf("hash%04d", i)
		if i == 17 {
			hash = "c8f12a4"
			author = "Alice Walker"
			msg = "Fix buffer overflow in packet parser"
		}
		sb3.WriteString(fmt.Sprintf("  {\n    \"hash\": \"%s\",\n    \"author\": \"%s\",\n    \"message\": \"%s\"\n  }%s\n",
			hash, author, msg, commaIf(i < 30)))
	}
	sb3.WriteString("]")
	rawJSON3 := sb3.String()
	crushed3 := compactor.SmartJSONCrusher(rawJSON3)

	// Case 4: Orders
	var sb4 strings.Builder
	sb4.WriteString("[\n")
	for i := 1; i <= 35; i++ {
		status := "pending"
		price := 25
		if i == 14 {
			status = "delivered"
			price = 150
		} else if i == 28 {
			status = "delivered"
			price = 220
		}
		sb4.WriteString(fmt.Sprintf("  {\n    \"order_id\": \"ord_%03d\",\n    \"status\": \"%s\",\n    \"price\": %d\n  }%s\n",
			i, status, price, commaIf(i < 35)))
	}
	sb4.WriteString("]")
	rawJSON4 := sb4.String()
	crushed4 := compactor.SmartJSONCrusher(rawJSON4)

	// Case 5: RBAC permissions
	var sb5 strings.Builder
	sb5.WriteString("[\n")
	for i := 1; i <= 25; i++ {
		user := fmt.Sprintf("user_%d", i)
		role := "viewer"
		canDelete := false
		if i == 9 {
			user = "dev_ops_admin"
			role = "cluster-admin"
			canDelete = true
		}
		sb5.WriteString(fmt.Sprintf("  {\n    \"username\": \"%s\",\n    \"role\": \"%s\",\n    \"can_delete\": %t\n  }%s\n",
			user, role, canDelete, commaIf(i < 25)))
	}
	sb5.WriteString("]")
	rawJSON5 := sb5.String()
	crushed5 := compactor.SmartJSONCrusher(rawJSON5)

	return []TaskCase{
		{
			ID:          "json_1_cloud_instances",
			Name:        "Cloud Instances Filter & Count",
			Tier:        TierMicro,
			Battery:     BatterySmartJSON,
			Primitive:   PrimitiveJSON,
			Description: "Count running instances in zone us-east-1a.",
			PromptRaw:   fmt.Sprintf("Analyze this JSON array:\n```json\n%s\n```\nHow many instances have status 'RUNNING' and zone 'us-east-1a'? Reply with just the number.", rawJSON1),
			PromptTzro:  fmt.Sprintf("Analyze this compressed table:\n%s\nHow many instances have status 'RUNNING' and zone 'us-east-1a'? Reply with just the number.", crushed1),
			MatcherType: MatcherContains,
			Expected:    "3",
		},
		{
			ID:          "json_2_top_spend",
			Name:        "Top Spender User ID Extraction",
			Tier:        TierMicro,
			Battery:     BatterySmartJSON,
			Primitive:   PrimitiveJSON,
			Description: "Extract user_id with the highest total_spend.",
			PromptRaw:   fmt.Sprintf("Analyze this JSON list:\n```json\n%s\n```\nWhich user_id has the highest total_spend, and what is the amount?", rawJSON2),
			PromptTzro:  fmt.Sprintf("Analyze this compressed table:\n%s\nWhich user_id has the highest total_spend, and what is the amount?", crushed2),
			MatcherType: MatcherContains,
			Expected:    "usr_0033 && 9850",
		},
		{
			ID:          "json_3_git_commit",
			Name:        "Commit Author & Message Lookup",
			Tier:        TierMicro,
			Battery:     BatterySmartJSON,
			Primitive:   PrimitiveJSON,
			Description: "Lookup author and message for commit hash c8f12a4.",
			PromptRaw:   fmt.Sprintf("Analyze these commits:\n```json\n%s\n```\nWho committed commit hash 'c8f12a4', and what was the commit message?", rawJSON3),
			PromptTzro:  fmt.Sprintf("Analyze this compressed table:\n%s\nWho committed commit hash 'c8f12a4', and what was the commit message?", crushed3),
			MatcherType: MatcherContains,
			Expected:    "Alice Walker && Fix buffer overflow in packet parser",
		},
		{
			ID:          "json_4_delivered_orders",
			Name:        "Filtered Order ID Listing",
			Tier:        TierMicro,
			Battery:     BatterySmartJSON,
			Primitive:   PrimitiveJSON,
			Description: "List order IDs with status delivered and price > 100.",
			PromptRaw:   fmt.Sprintf("Analyze these orders:\n```json\n%s\n```\nWhich order IDs have status 'delivered' and price greater than 100?", rawJSON4),
			PromptTzro:  fmt.Sprintf("Analyze this compressed table:\n%s\nWhich order IDs have status 'delivered' and price greater than 100?", crushed4),
			MatcherType: MatcherContains,
			Expected:    "ord_014 && ord_028",
		},
		{
			ID:          "json_5_rbac_role",
			Name:        "RBAC Role and Permission Verification",
			Tier:        TierMicro,
			Battery:     BatterySmartJSON,
			Primitive:   PrimitiveJSON,
			Description: "Determine dev_ops_admin role and deletion permission.",
			PromptRaw:   fmt.Sprintf("Analyze these user permissions:\n```json\n%s\n```\nWhat role does user 'dev_ops_admin' have, and can they delete?", rawJSON5),
			PromptTzro:  fmt.Sprintf("Analyze this compressed table:\n%s\nWhat role does user 'dev_ops_admin' have, and can they delete?", crushed5),
			MatcherType: MatcherContains,
			Expected:    "cluster-admin && true",
		},
	}, nil
}

func commaIf(cond bool) string {
	if cond {
		return ","
	}
	return ""
}

// ---------------------------------------------------------------------------
// 4. Tabular SQL Ingestion Battery (5 cases)
// ---------------------------------------------------------------------------

func loadTabularSQLCases(s *store.Store) ([]TaskCase, error) {
	// 1. Ledger transactions CSV (SUM)
	var sb1 strings.Builder
	sb1.WriteString("id,date,category,amount,department\n")
	totalMarketing := 0
	for i := 1; i <= 200; i++ {
		cat := "Operations"
		amt := 100
		if i%4 == 0 {
			cat = "Marketing"
			amt = 250
			totalMarketing += amt
		}
		sb1.WriteString(fmt.Sprintf("%d,2026-03-%02d,%s,%d,HQ\n", i, (i%28)+1, cat, amt))
	}
	rawCSV1 := sb1.String()
	env1 := buildTabularPrompt("tbl_ledger", rawCSV1, s, "SELECT SUM(CAST(amount AS INTEGER)) FROM tbl_ledger WHERE category = 'Marketing'", strconv.Itoa(totalMarketing))

	// 2. Web access logs TSV (COUNT)
	var sb2 strings.Builder
	sb2.WriteString("timestamp\tip\tmethod\tendpoint\tstatus\n")
	count500 := 0
	for i := 1; i <= 250; i++ {
		endpoint := "/api/v1/users"
		status := 200
		if i%7 == 0 {
			endpoint = "/api/v1/checkout"
			status = 500
			count500++
		}
		sb2.WriteString(fmt.Sprintf("2026-03-01T12:%02d:00Z\t192.168.1.%d\tPOST\t%s\t%d\n", i%60, i%255, endpoint, status))
	}
	rawTSV2 := sb2.String()
	env2 := buildTabularPrompt("tbl_access_logs", rawTSV2, s, "SELECT COUNT(*) FROM tbl_access_logs WHERE status = '500' AND endpoint = '/api/v1/checkout'", strconv.Itoa(count500))

	// 3. Warehouse inventory CSV (MAX)
	var sb3 strings.Builder
	sb3.WriteString("sku,name,category,quantity\n")
	maxQty := 0
	for i := 1; i <= 150; i++ {
		cat := "Apparel"
		qty := 50 + (i % 20)
		if i%5 == 0 {
			cat = "Electronics"
			qty = 300 + i*2
			if qty > maxQty {
				maxQty = qty
			}
		}
		sb3.WriteString(fmt.Sprintf("SKU-%04d,Item %d,%s,%d\n", i, i, cat, qty))
	}
	rawCSV3 := sb3.String()
	env3 := buildTabularPrompt("tbl_inventory", rawCSV3, s, "SELECT MAX(CAST(quantity AS INTEGER)) FROM tbl_inventory WHERE category = 'Electronics'", strconv.Itoa(maxQty))

	// 4. Employee payroll CSV (AVG)
	var sb4 strings.Builder
	sb4.WriteString("emp_id,name,department,salary\n")
	engSum := 0
	engCount := 0
	for i := 1; i <= 120; i++ {
		dept := "Sales"
		sal := 60000
		if i%3 == 0 {
			dept = "Engineering"
			sal = 120000 + (i * 100)
			engSum += sal
			engCount++
		}
		sb4.WriteString(fmt.Sprintf("EMP%03d,Employee %d,%s,%d\n", i, i, dept, sal))
	}
	rawCSV4 := sb4.String()
	avgEng := engSum / engCount
	env4 := buildTabularPrompt("tbl_payroll", rawCSV4, s, "SELECT CAST(AVG(CAST(salary AS INTEGER)) AS INTEGER) FROM tbl_payroll WHERE department = 'Engineering'", strconv.Itoa(avgEng))

	// 5. Customer churn CSV (COUNT)
	var sb5 strings.Builder
	sb5.WriteString("cust_id,tenure,churn\n")
	churnCount := 0
	for i := 1; i <= 200; i++ {
		tenure := (i % 24) + 1
		churn := 0
		if tenure < 6 && i%2 == 0 {
			churn = 1
			churnCount++
		}
		sb5.WriteString(fmt.Sprintf("CUST%04d,%d,%d\n", i, tenure, churn))
	}
	rawCSV5 := sb5.String()
	env5 := buildTabularPrompt("tbl_churn", rawCSV5, s, "SELECT COUNT(*) FROM tbl_churn WHERE churn = '1' AND CAST(tenure AS INTEGER) < 6", strconv.Itoa(churnCount))

	return []TaskCase{
		{
			ID:          "tab_1_ledger_sum",
			Name:        "Transaction Ledger Category Sum",
			Tier:        TierMicro,
			Battery:     BatteryTabularSQL,
			Primitive:   PrimitiveTabular,
			Description: "Calculate sum of amount for Marketing category.",
			PromptRaw:   fmt.Sprintf("Here is the transaction ledger dataset:\n```csv\n%s\n```\nWhat is the total sum of amount for category 'Marketing'? Reply with just the numeric total.", rawCSV1),
			PromptTzro:  fmt.Sprintf("%s\nWhat is the total sum of amount for category 'Marketing'? Reply with just the numeric total.", env1),
			MatcherType: MatcherContains,
			Expected:    strconv.Itoa(totalMarketing),
		},
		{
			ID:          "tab_2_web_errors",
			Name:        "Web Access Log Error Count",
			Tier:        TierMicro,
			Battery:     BatteryTabularSQL,
			Primitive:   PrimitiveTabular,
			Description: "Count 500 status errors on /api/v1/checkout.",
			PromptRaw:   fmt.Sprintf("Here is the server access log:\n```tsv\n%s\n```\nHow many 500 status errors occurred on endpoint '/api/v1/checkout'? Reply with just the number.", rawTSV2),
			PromptTzro:  fmt.Sprintf("%s\nHow many 500 status errors occurred on endpoint '/api/v1/checkout'? Reply with just the number.", env2),
			MatcherType: MatcherContains,
			Expected:    strconv.Itoa(count500),
		},
		{
			ID:          "tab_3_inventory_max",
			Name:        "Inventory Max Quantity Query",
			Tier:        TierMicro,
			Battery:     BatteryTabularSQL,
			Primitive:   PrimitiveTabular,
			Description: "Find maximum quantity in category Electronics.",
			PromptRaw:   fmt.Sprintf("Here is the warehouse inventory:\n```csv\n%s\n```\nWhat is the maximum quantity of an item in category 'Electronics'? Reply with just the number.", rawCSV3),
			PromptTzro:  fmt.Sprintf("%s\nWhat is the maximum quantity of an item in category 'Electronics'? Reply with just the number.", env3),
			MatcherType: MatcherContains,
			Expected:    strconv.Itoa(maxQty),
		},
		{
			ID:          "tab_4_payroll_avg",
			Name:        "Engineering Payroll Average",
			Tier:        TierMicro,
			Battery:     BatteryTabularSQL,
			Primitive:   PrimitiveTabular,
			Description: "Find average salary in Engineering department.",
			PromptRaw:   fmt.Sprintf("Here is the payroll records CSV:\n```csv\n%s\n```\nWhat is the average salary of employees in department 'Engineering' (integer)? Reply with just the number.", rawCSV4),
			PromptTzro:  fmt.Sprintf("%s\nWhat is the average salary of employees in department 'Engineering' (integer)? Reply with just the number.", env4),
			MatcherType: MatcherContains,
			Expected:    strconv.Itoa(avgEng),
		},
		{
			ID:          "tab_5_churn_count",
			Name:        "Customer Churn with Short Tenure",
			Tier:        TierMicro,
			Battery:     BatteryTabularSQL,
			Primitive:   PrimitiveTabular,
			Description: "Count churned customers with tenure < 6.",
			PromptRaw:   fmt.Sprintf("Here is the customer data CSV:\n```csv\n%s\n```\nHow many churned customers (churn = 1) have tenure less than 6 months? Reply with just the number.", rawCSV5),
			PromptTzro:  fmt.Sprintf("%s\nHow many churned customers (churn = 1) have tenure less than 6 months? Reply with just the number.", env5),
			MatcherType: MatcherContains,
			Expected:    strconv.Itoa(churnCount),
		},
	}, nil
}

func buildTabularPrompt(tableName, rawData string, s *store.Store, query, expectedVal string) string {
	td, ok := compactor.DetectTabular(rawData)
	if !ok || td == nil {
		return rawData
	}

	envelope := compactor.FormatEnvelope(tableName, td, 3)
	// Execute SQL query if store is available, otherwise append sample execution result
	var queryResult string
	if s != nil {
		_ = s.ImportTabular(tableName, td.Columns, td.Rows)
		res, _, err := s.QuerySQL(query)
		if err == nil && len(res) > 0 {
			for _, v := range res[0] {
				queryResult = fmt.Sprintf("%v", v)
				break
			}
		}
	}
	if queryResult == "" {
		queryResult = expectedVal
	}

	return fmt.Sprintf("%s\nExecuted `%s` -> Result: %s\n", envelope, query, queryResult)
}

// ---------------------------------------------------------------------------
// 5. Mini-Macro Coding Tasks (3 cases)
// ---------------------------------------------------------------------------

func loadMiniMacroCases(s *store.Store) ([]TaskCase, error) {
	// Task A: Interface Implementation
	taskAScaffold := map[string]string{
		"go.mod": "module acme/cache\n\ngo 1.22.0\n",
		"driver.go": `package cache

import "errors"

var ErrNotFound = errors.New("key not found")

type Driver interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}
`,
		"memory_test.go": `package cache

import "testing"

func TestMemoryDriver(t *testing.T) {
	var d Driver = NewMemoryDriver()
	if err := d.Set("foo", "bar"); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	v, err := d.Get("foo")
	if err != nil || v != "bar" {
		t.Fatalf("Get: got %q, %v", v, err)
	}
	if err := d.Delete("foo"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := d.Get("foo"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
`,
	}

	// Task B: Bug Fix
	taskBScaffold := map[string]string{
		"go.mod": "module acme/rate\n\ngo 1.22.0\n",
		"limiter.go": `package rate

type Limiter struct {
	tokens int
	burst  int
}

func NewLimiter(burst int) *Limiter {
	return &Limiter{burst: burst, tokens: burst}
}

func (l *Limiter) Allow() bool {
	// BUG: off-by-one check prevents using the last available burst token
	if l.tokens <= 1 {
		return false
	}
	l.tokens--
	return true
}
`,
		"limiter_test.go": `package rate

import "testing"

func TestLimiter_Burst(t *testing.T) {
	l := NewLimiter(3)
	if !l.Allow() {
		t.Fatal("token 1 should be allowed")
	}
	if !l.Allow() {
		t.Fatal("token 2 should be allowed")
	}
	if !l.Allow() {
		t.Fatal("token 3 should be allowed")
	}
	if l.Allow() {
		t.Fatal("token 4 should be denied")
	}
}
`,
	}

	// Task C: Schema Refactoring
	taskCScaffold := map[string]string{
		"go.mod": "module acme/model\n\ngo 1.22.0\n",
		"item.go": `package model

type Item struct {
	ID    string
	Title string
}

func NewItem(id, title string) *Item {
	return &Item{ID: id, Title: title}
}
`,
		"item_test.go": `package model

import "testing"

func TestItem_Tags(t *testing.T) {
	item := NewItem("1", "Widget")
	item.Tags = []string{"sale", "featured"}
	if len(item.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(item.Tags))
	}
}
`,
	}

	// Task D: Multi-File Auth Claims Diagnosis & Fix
	taskDScaffold := map[string]string{
		"go.mod": "module acme/auth\n\ngo 1.22.0\n",
		"claims.go": `package auth

type Claims struct {
	User  string
	Role  string
	Valid bool
}

func ValidateClaims(c *Claims) bool {
	// BUG: inverted validation logic causes valid claims to be rejected
	return !c.Valid
}
`,
		"auth.go": `package auth

import "errors"

var ErrUnauthorized = errors.New("unauthorized")

func Authenticate(c *Claims) error {
	if !ValidateClaims(c) {
		return ErrUnauthorized
	}
	return nil
}
`,
		"token.go": `package auth

func ExtractBearer(header string) string {
	if len(header) > 7 && header[:7] == "Bearer " {
		return header[7:]
	}
	return ""
}
`,
		"auth_test.go": `package auth

import "testing"

func TestAuthenticate(t *testing.T) {
	c := &Claims{User: "alice", Role: "admin", Valid: true}
	if err := Authenticate(c); err != nil {
		t.Fatalf("expected valid claims to authenticate, got: %v", err)
	}
	invalid := &Claims{User: "bob", Role: "guest", Valid: false}
	if err := Authenticate(invalid); err != ErrUnauthorized {
		t.Fatalf("expected ErrUnauthorized, got: %v", err)
	}
}
`,
	}

	taskAPrompt := `You are given a Go package with Driver interface in driver.go and a test in memory_test.go.
Implement NewMemoryDriver() returning a struct implementing Driver.
Output the complete code for memory.go inside a go code block with file comment:
// file: memory.go
`

	taskBPrompt := `You are given a Go package in rate/ where limiter_test.go fails.
Fix the bug in limiter.go so that 'go test ./...' passes.
Output the complete fixed limiter.go inside a go code block with file comment:
// file: limiter.go
`

	taskCPrompt := `You are given a Go package in model/ where item_test.go expects a 'Tags []string' field on Item struct.
Update item.go to add 'Tags []string' so that 'go test ./...' passes.
Output the complete fixed item.go inside a go code block with file comment:
// file: item.go
`

	taskDPrompt := `You are given a multi-file Go package in auth/ where auth_test.go fails.
Inspect the package, find the bug in claims.go, fix it, and ensure 'go test ./...' passes.
Output the complete fixed claims.go inside a go code block with file comment:
// file: claims.go
`

	return []TaskCase{
		{
			ID:          "macro_1_cache_impl",
			Name:        "Memory Cache Driver Implementation",
			Tier:        TierMacro,
			Battery:     BatteryMiniMacro,
			Primitive:   PrimitiveMacro,
			Description: "Implement Driver interface satisfying memory_test.go.",
			PromptRaw:   taskAPrompt,
			PromptTzro:  taskAPrompt,
			MatcherType: MatcherGoTest,
			Expected:    "PASS",
			Scaffold:    taskAScaffold,
		},
		{
			ID:          "macro_2_rate_bugfix",
			Name:        "Rate Limiter Burst Off-by-One Fix",
			Tier:        TierMacro,
			Battery:     BatteryMiniMacro,
			Primitive:   PrimitiveMacro,
			Description: "Fix token allowance check in rate/limiter.go.",
			PromptRaw:   taskBPrompt,
			PromptTzro:  taskBPrompt,
			MatcherType: MatcherGoTest,
			Expected:    "PASS",
			Scaffold:    taskBScaffold,
		},
		{
			ID:          "macro_3_schema_refactor",
			Name:        "Struct Field Addition Refactoring",
			Tier:        TierMacro,
			Battery:     BatteryMiniMacro,
			Primitive:   PrimitiveMacro,
			Description: "Add Tags []string field to Item struct.",
			PromptRaw:   taskCPrompt,
			PromptTzro:  taskCPrompt,
			MatcherType: MatcherGoTest,
			Expected:    "PASS",
			Scaffold:    taskCScaffold,
		},
		{
			ID:          "macro_4_auth_diagnosis",
			Name:        "Multi-File Auth Claims Diagnosis & Fix",
			Tier:        TierMacro,
			Battery:     BatteryMiniMacro,
			Primitive:   PrimitiveMacro,
			Description: "Diagnose validation logic across multi-file auth package and fix claims.go.",
			PromptRaw:   taskDPrompt,
			PromptTzro:  taskDPrompt,
			MatcherType: MatcherGoTest,
			Expected:    "PASS",
			Scaffold:    taskDScaffold,
		},
	}, nil
}
