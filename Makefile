# tzro - Makefile

SHELL := /bin/bash
RUN_ID ?= workflows-$(shell date -u +%Y%m%dT%H%M%SZ)
REPORT_JSON ?= docs/benchmarks/$(RUN_ID).json
REPORT_MD ?= $(REPORT_JSON:.json=.md)
REPEATS ?= 3
PROFILES ?= baseline,standard
TOTAL_CAP ?= 20.0
MODEL ?= minimax/minimax-m3
MAX_COST ?= 2.0
# Conservative provider ceilings; verify current prices before a paid run.
INPUT_PRICE ?= 0.75
OUTPUT_PRICE ?= 3.00
CACHE_READ_PRICE ?= 0.75
CACHE_WRITE_PRICE ?= 0.00

PREFLIGHT_RUNTIME_ARGS =
ifneq ($(findstring full,$(PROFILES)),)
PREFLIGHT_RUNTIME_ARGS = \
	--decision-bin "$$PWD/bin/jev-score" \
	--decision-model "$$PWD/models/decision/Jev-Style-0.8B-Decision-v3-Q4_K_M.gguf" \
	--decision-version "JEV v3 (libllama 9770, Qwen3.5)" \
	--extractor-bin "$$(command -v python3)" \
	--extractor-arg "$$PWD/bin/gliner_worker.py" \
	--extractor-model "$$HOME/.cache/huggingface/hub/models--fastino--gliner2.5-base-v1/snapshots/1a8bc24e00dc7300b9017c81d63e3dcdabb26596" \
	--extractor-version "GLiNER 2.5 (gliner2 2.0.0, torch 2.8.0)"
endif

.PHONY: all build test benchmark-publish benchmark-run benchmark-preflight benchmark-validate

all: build

build:
	go build -o bin/tzro ./cmd/tzro

test:
	CGO_ENABLED=1 go test -race -count=1 ./...

# Rendering saved evidence never starts paid requests.
benchmark-publish:
	@test -f "$(REPORT_JSON)" || { echo "Missing saved report: $(REPORT_JSON)" >&2; exit 1; }
	python3 scripts/generate_benchmark_report.py "$(REPORT_JSON)" "$(REPORT_MD)"

benchmark-validate:
	python3 scripts/verify_release_benchmark.py "$(REPORT_JSON)"

# benchmark-preflight verifies the selected profiles (Baseline and Standard by default)
# without sending any paid provider requests.
benchmark-preflight: build
	bin/tzro bench workflows \
		--model $(MODEL) \
		--profiles "$(PROFILES)" $(PREFLIGHT_RUNTIME_ARGS)

# The persistent ledger reserves in-flight usage across diagnostic and validation runs.
benchmark-run: build
	python3 scripts/run_workflow_validation.py --run-id "$(RUN_ID)" \
		--model "$(MODEL)" --profiles "$(PROFILES)" --repeats "$(REPEATS)" \
		--total-cap "$(TOTAL_CAP)" --run-cap "$(MAX_COST)" \
		--input-price "$(INPUT_PRICE)" --output-price "$(OUTPUT_PRICE)" \
		--cache-read-price "$(CACHE_READ_PRICE)"
