# tzro - Makefile

SHELL := /bin/bash
REPORT_JSON ?= docs/benchmarks/workflows-20260928.json
REPORT_MD ?= docs/benchmarks/workflows-20260928.md
MODEL ?= minimax/minimax-m3
MAX_COST ?= 2.0
INPUT_PRICE ?= 0.30
OUTPUT_PRICE ?= 1.20
CACHE_READ_PRICE ?= 0.06
CACHE_WRITE_PRICE ?= 0.00

.PHONY: all build test benchmark-publish benchmark-run benchmark-preflight

all: build

build:
	go build -o bin/tzro ./cmd/tzro

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

# benchmark-publish generates the markdown benchmark report from saved structured JSON
# if available, avoiding unnecessary paid provider requests.
# If the JSON report does not exist, it runs the benchmark suite within explicit cost limits.
benchmark-publish:
	@if [ -f "$(REPORT_JSON)" ]; then \
		echo "Publishing benchmark report from existing $(REPORT_JSON)..."; \
		python3 scripts/generate_benchmark_report.py "$(REPORT_JSON)" "$(REPORT_MD)"; \
	else \
		echo "No existing report found at $(REPORT_JSON). Running benchmark suite..."; \
		$(MAKE) benchmark-run; \
	fi

# benchmark-preflight runs preflight verification across Baseline, Standard, and Full
# without sending any paid provider requests.
benchmark-preflight: build
	bin/tzro bench workflows \
		--model $(MODEL) \
		--profiles baseline,standard,full \
		--decision-bin "$$PWD/bin/jev-score" \
		--decision-model "$$PWD/models/decision/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf" \
		--decision-version "JEV v3 (libllama 9770, Qwen3.5)" \
		--extractor-bin "$$(which python3)" \
		--extractor-arg "$$PWD/bin/gliner_worker.py" \
		--extractor-model "$$PWD/.cache/huggingface/hub/models--fastino--gliner2.5-base-v1/snapshots/1a8bc24e00dc7300b9017c81d63e3dcdabb26596" \
		--extractor-version "GLiNER 2.5 (gliner2 2.0.0, torch 2.8.0)"

# benchmark-run executes the live workflow benchmark across all installation profiles
# within explicit cost limits and writes both JSON and Markdown reports.
benchmark-run: build
	@if [ -z "$$TZRO_BENCH_API_KEY" ]; then \
		if [ -f .env ]; then \
			export TZRO_BENCH_API_KEY=$$(grep OPENROUTER_API_KEY .env | cut -d '=' -f2); \
		fi; \
	fi; \
	if [ -z "$$TZRO_BENCH_API_KEY" ]; then \
		echo "Error: TZRO_BENCH_API_KEY or OPENROUTER_API_KEY in .env is required for paid benchmark runs." >&2; \
		exit 1; \
	fi; \
	bin/tzro bench workflows \
		--model $(MODEL) \
		--profiles baseline,standard,full \
		--run \
		--max-cost $(MAX_COST) \
		--timeout 3m \
		--max-turns 20 \
		--input-price $(INPUT_PRICE) \
		--output-price $(OUTPUT_PRICE) \
		--cache-read-price $(CACHE_READ_PRICE) \
		--cache-write-price $(CACHE_WRITE_PRICE) \
		--decision-bin "$$PWD/bin/jev-score" \
		--decision-model "$$PWD/models/decision/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf" \
		--decision-version "JEV v3 (libllama 9770, Qwen3.5)" \
		--extractor-bin "$$(which python3)" \
		--extractor-arg "$$PWD/bin/gliner_worker.py" \
		--extractor-model "$$PWD/.cache/huggingface/hub/models--fastino--gliner2.5-base-v1/snapshots/1a8bc24e00dc7300b9017c81d63e3dcdabb26596" \
		--extractor-version "GLiNER 2.5 (gliner2 2.0.0, torch 2.8.0)" \
		--output "$(REPORT_JSON)"
	python3 scripts/generate_benchmark_report.py "$(REPORT_JSON)" "$(REPORT_MD)"
