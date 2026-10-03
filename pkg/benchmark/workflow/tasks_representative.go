package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const billingInvoiceGrade = `package billing
import ("testing"; "time")
func TestInvoiceDiscountAndTax(t *testing.T) {
  for _, c := range []struct{tier BillingTier; discount,tax,total int}{
    {TierEnterprise,2000,660,8660}, {TierPro,1000,742,9742}, {TierBasic,0,825,10825},
  } {
    s := NewBillingService()
    account, err := s.RegisterAccount("account", "Example", "billing@example.test", c.tier)
    if err != nil { t.Fatal(err) }
    type result struct { invoice *Invoice; err error }
    done := make(chan result, 1)
    go func(){
      invoice, err := s.GenerateInvoice("invoice", account.ID, []InvoiceItem{{Description:"license", Quantity:1, UnitPrice:10000}}, "US-CA")
      done <- result{invoice,err}
    }()
    select {
    case got := <-done:
      if got.err != nil { t.Fatal(got.err) }
      if got.invoice.Subtotal != 10000 || got.invoice.Discount != c.discount || got.invoice.Tax != c.tax || got.invoice.Total != c.total {
        t.Fatalf("incorrect %s invoice: %+v", c.tier, got.invoice)
      }
    case <-time.After(2*time.Second):
      t.Fatal("invoice generation did not return; check service locking")
    }
  }
}
`

// RepresentativeTasks mixes small coding controls, unfamiliar code, incident
// diagnosis, and two data sizes. Prompts never prescribe a tool or expose checks.
func RepresentativeTasks() []Task {
	cases := representativeCases()
	tasks := make([]Task, len(cases))
	for i, c := range cases {
		tasks[i] = c.task
	}
	return tasks
}

type representativeCase struct {
	task     Task
	solution map[string]string
}

func representativeCases() []representativeCase {
	legacy := DefaultTasks()
	cache := legacy[0]
	cache.ID = "control_cache"
	rate := legacy[1]
	rate.ID = "control_rate_limiter"
	rate.Files["limiter.go"] = removeBugComments(rate.Files["limiter.go"])
	billing := taskLargeFileSkeleton()
	billing.ID = "billing_discount"
	// Keep the service's real account/invoice/subscription operations. Remove the
	// repetitive padding helpers from the earlier large-file diagnostic fixture.
	source := billing.Files["service.go"]
	if index := strings.Index(source, "// ValidateParameterSet1 "); index >= 0 {
		source = source[:index]
	}
	source = strings.ReplaceAll(source, "\t\t// BUG: returns 0 instead of 20% discount\n", "")
	billing.Files["service.go"] = source
	billing.Prompt = "Enterprise customers report that their invoices receive no tier discount. Investigate the billing service and repair the cause. The product rules are in README.md. Preserve existing APIs and tests, and run the Go tests."
	billing.Files["README.md"] = "# Billing service\nAccounts, subscriptions, invoices, and audit events are managed by BillingService. Tier discounts are basic 0%, pro 10%, enterprise 20% of the amount in cents. Existing tax and account operations must remain compatible.\n"
	billing.GradeFiles = map[string]string{"tier_private_test.go": billing.Files["service_test.go"], "invoice_private_test.go": billingInvoiceGrade}
	billing.Files["service_test.go"] = "package billing\nimport \"testing\"\nfunc TestService(t *testing.T){ if NewBillingService()==nil {t.Fatal(\"missing service\")} }\n"
	billingFix := strings.Replace(source, "case \"enterprise\":\n\t\treturn 0", "case \"enterprise\":\n\t\treturn amountCents * 20 / 100", 1)
	auth := taskMultiPkgDiscovery()
	auth.ID = "authentication_discovery"
	auth.Prompt = "Valid tokens are rejected during authentication. Find the cause in this repository and repair it without changing public interfaces. Preserve the tests and run the Go test suite."
	for name, body := range auth.Files {
		auth.Files[name] = removeBugComments(body)
	}
	authFix := strings.Replace(auth.Files["pkg/auth/authenticator.go"], "actualHash := \"\"", "actualHash := a.hasher.HashToken(rawToken)", 1)
	return []representativeCase{
		{task: cache, solution: map[string]string{"memory.go": `package cache
import "sync"
type MemoryDriver struct { mu sync.RWMutex; data map[string]string }
func NewMemoryDriver() *MemoryDriver {return &MemoryDriver{data:map[string]string{}}}
func (m *MemoryDriver) Get(k string)(string,error){m.mu.RLock(); defer m.mu.RUnlock();v,ok:=m.data[k];if !ok{return "",ErrNotFound};return v,nil}
func (m *MemoryDriver) Set(k,v string)error{m.mu.Lock();defer m.mu.Unlock();m.data[k]=v;return nil}
func (m *MemoryDriver) Delete(k string)error{m.mu.Lock();defer m.mu.Unlock();delete(m.data,k);return nil}
`}},
		{task: rate, solution: map[string]string{"limiter.go": strings.Replace(rate.Files["limiter.go"], "l.tokens <= 1", "l.tokens <= 0", 1)}},
		{task: billing, solution: map[string]string{"service.go": billingFix}},
		{task: auth, solution: map[string]string{"pkg/auth/authenticator.go": authFix}},
		tabularCase(256), tabularCase(8192), incidentCase(),
	}
}

func removeBugComments(body string) string {
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "// BUG:") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

type customerRevenue struct {
	Customer string `json:"customer"`
	Revenue  int    `json:"revenue_cents"`
}
type revenueSummary struct {
	Regions   map[string]int    `json:"revenue_cents_by_region"`
	Customers []customerRevenue `json:"top_customers"`
}

func tabularCase(rows int) representativeCase {
	var csv strings.Builder
	csv.WriteString("order_id,customer,region,amount_cents,status\n")
	summary := revenueSummary{Regions: map[string]int{}}
	customers := map[string]int{}
	regions := []string{"APAC", "EMEA", "NA", "LATAM"}
	// Interleave regions/statuses and use non-monotonic amounts, so a prefix or
	// hand-selected sample cannot produce the complete answer.
	for i := 0; i < rows; i++ {
		region := regions[(i*7+i/13)%len(regions)]
		customer := fmt.Sprintf("customer-%03d", (i*17+i/7)%97)
		cents := 100 + (i*7919+i*i*31)%90000
		status := "completed"
		if i%5 == 0 {
			status = "cancelled"
		}
		if i%11 == 0 {
			status = "pending"
		}
		fmt.Fprintf(&csv, "order-%05d,%s,%s,%d,%s\n", i, customer, region, cents, status)
		if status == "completed" {
			summary.Regions[region] += cents
			customers[customer] += cents
		}
	}
	for customer, total := range customers {
		summary.Customers = append(summary.Customers, customerRevenue{customer, total})
	}
	sort.Slice(summary.Customers, func(i, j int) bool {
		if summary.Customers[i].Revenue == summary.Customers[j].Revenue {
			return summary.Customers[i].Customer < summary.Customers[j].Customer
		}
		return summary.Customers[i].Revenue > summary.Customers[j].Revenue
	})
	summary.Customers = summary.Customers[:3]
	answer, _ := json.Marshal(summary)
	test := fmt.Sprintf(`package analysis
import("encoding/json";"os";"reflect";"testing")
func TestSummary(t *testing.T){
 var got,want struct { Regions map[string]int %s; Customers []struct{Customer string %s;Revenue int %s} %s }
 data,err:=os.ReadFile("summary.json");if err!=nil{t.Fatal(err)}
 if err=json.Unmarshal(data,&got);err!=nil{t.Fatal(err)}
 if err=json.Unmarshal([]byte(%q),&want);err!=nil{t.Fatal(err)}
 if !reflect.DeepEqual(got,want){t.Fatal("summary does not match complete dataset")}
}
`, "`json:\"revenue_cents_by_region\"`", "`json:\"customer\"`", "`json:\"revenue_cents\"`", "`json:\"top_customers\"`", string(answer))
	return representativeCase{task: Task{ID: fmt.Sprintf("revenue_%d", rows),
		Prompt:        "Analyze orders.csv and write summary.json. Include revenue_cents_by_region (an object mapping each region to its total completed-order revenue) and top_customers (the three customers with the highest completed-order revenue globally, as objects with customer and revenue_cents). Ignore pending/cancelled orders. Sort customers by revenue descending, then customer ascending for ties. Use integer cents, inspect the complete dataset, preserve input files, and verify the totals.",
		Files:         map[string]string{"orders.csv": csv.String()},
		ReadOnlyFiles: []string{"orders.csv"}, GradeFiles: map[string]string{"go.mod": "module analysis\n\ngo 1.22\n", "analysis.go": "package analysis\n", "summary_private_test.go": test}}, solution: map[string]string{"summary.json": string(answer) + "\n"}}
}

func incidentCase() representativeCase {
	var log strings.Builder
	log.WriteString("2026-09-12T09:04:02Z INFO request=ad72 route=/checkout status=500\npanic: runtime error: invalid memory address or nil pointer dereference\n\ngoroutine 42 [running]:\nshop/internal/pricing.(*Calculator).ApplyCoupon(...)\n\t/app/internal/pricing/coupon.go:87 +0x34\nshop/internal/checkout.(*Handler).ServeHTTP(...)\n\t/app/internal/checkout/handler.go:124 +0xa0\n")
	// Background goroutines resemble a production process dump. They contain no
	// additional incident cause and need not be loaded into model context.
	for i := 0; i < 96; i++ {
		fmt.Fprintf(&log, "\ngoroutine %d [IO wait]:\ninternal/poll.runtime_pollWait(...)\n\t/usr/local/go/src/runtime/netpoll.go:351 +0x84\ninternal/poll.(*pollDesc).wait(...)\n\t/usr/local/go/src/internal/poll/fd_poll_runtime.go:84 +0x28\nnet.(*conn).Read(...)\n\t/usr/local/go/src/net/net.go:196 +0x44\nnet/http.(*persistConn).readLoop(...)\n\t/usr/local/go/src/net/http/transport.go:2205 +0x15c\n", 100+i)
	}
	task := Task{ID: "incident_diagnosis", ReadOnlyFiles: []string{"incident.log"}, Prompt: "Investigate the checkout incident in incident.log. Write incident.json with request_id, application_file, application_line (integer), and cause. Use the earliest application frame at the panic, not background networking goroutines. Express cause as the concise error kind. Preserve the input log.", Files: map[string]string{"incident.log": log.String()}, GradeFiles: map[string]string{"go.mod": "module incident\n\ngo 1.22\n", "incident.go": "package incident\n", "incident_private_test.go": `package incident
import("encoding/json";"os";"strings";"testing")
func TestIncident(t *testing.T){var v struct{Request string ` + "`json:\"request_id\"`" + `;File string ` + "`json:\"application_file\"`" + `;Line int ` + "`json:\"application_line\"`" + `;Cause string ` + "`json:\"cause\"`" + `};b,e:=os.ReadFile("incident.json");if e!=nil{t.Fatal(e)};if e=json.Unmarshal(b,&v);e!=nil{t.Fatal(e)};if v.Request!="ad72" || !strings.HasSuffix(v.File,"internal/pricing/coupon.go") || v.Line!=87 || !strings.Contains(strings.ToLower(v.Cause),"nil pointer"){t.Fatal("incorrect incident diagnosis")}}
`}}
	return representativeCase{task: task, solution: map[string]string{"incident.json": `{"request_id":"ad72","application_file":"/app/internal/pricing/coupon.go","application_line":87,"cause":"nil pointer dereference"}`}}
}
