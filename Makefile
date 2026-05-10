PROJECT_NAME := kuraos
BIN_DIR      := bin
BIN          := $(BIN_DIR)/kura
CMD          := ./cmd/kura
PID_FILE     := /tmp/$(PROJECT_NAME)-dev.pid
LOG_FILE     := /tmp/$(PROJECT_NAME)-dev.log
PORTMAN_ENV  := /tmp/$(PROJECT_NAME)-portman.env

# Tailwind output that gets embedded into the kura binary.
TAILWIND_OUT := internal/ui/dist/kura.css
TAILWIND_IN  := internal/ui/src/kura.css
TAILWIND_CFG := tailwind.config.js

.PHONY: build serve stop test lint tidy fmt vet clean ui-css ui-deps

build: ui-css
	@mkdir -p $(BIN_DIR)
	@if [ -f go.mod ]; then \
	  go build -o $(BIN) $(CMD); \
	else \
	  echo "==> go.mod not found yet — skip build (project bootstrap pending)"; \
	fi

# ui-deps installs Tailwind into node_modules. We avoid running it on every
# build by checking whether the binary already exists (idempotent).
ui-deps:
	@if [ ! -x node_modules/.bin/tailwindcss ]; then \
	  echo "==> Installing Tailwind CLI (first run)"; \
	  npm install --no-audit --no-fund --silent; \
	fi

# ui-css recompiles Tailwind only if the source CSS or templates changed.
# The output is committed so a fresh `go build` works without Node.js.
ui-css: ui-deps $(TAILWIND_OUT)

$(TAILWIND_OUT): $(TAILWIND_IN) $(TAILWIND_CFG) $(shell find internal/ui/templates -type f 2>/dev/null)
	@mkdir -p $(dir $(TAILWIND_OUT))
	@echo "==> Building Tailwind -> $(TAILWIND_OUT)"
	@./node_modules/.bin/tailwindcss -c $(TAILWIND_CFG) -i $(TAILWIND_IN) -o $(TAILWIND_OUT) --minify

serve: build
	@if [ -f $(PID_FILE) ]; then \
	  OLD_PID=$$(cat $(PID_FILE)); \
	  if kill -0 $$OLD_PID 2>/dev/null; then \
	    echo "==> Killing previous process (PID: $$OLD_PID)..."; \
	    kill $$OLD_PID; \
	    for i in $$(seq 1 50); do kill -0 $$OLD_PID 2>/dev/null || break; sleep 0.1; done; \
	    kill -0 $$OLD_PID 2>/dev/null && kill -9 $$OLD_PID 2>/dev/null || true; \
	  fi; \
	  rm -f $(PID_FILE); \
	fi
	@portman env --name kura --expose --output $(PORTMAN_ENV)
	@if [ ! -x $(BIN) ]; then \
	  echo "==> $(BIN) not built yet — cannot start kura. Bootstrap the Go project first."; \
	  exit 1; \
	fi
	@. $(PORTMAN_ENV) && \
	  : $${KURA_STATE_DB:=/tmp/kuraos-dev-state.db} && \
	  echo "==> Starting kura on port $$KURA_PORT (state: $$KURA_STATE_DB, log: $(LOG_FILE))" && \
	  KURA_PORT=$$KURA_PORT KURA_STATE_DB=$$KURA_STATE_DB nohup $(BIN) > $(LOG_FILE) 2>&1 & \
	  echo $$! > $(PID_FILE) && \
	  echo "    PID: $$(cat $(PID_FILE))"

stop:
	@if [ -f $(PID_FILE) ]; then \
	  OLD_PID=$$(cat $(PID_FILE)); \
	  if kill -0 $$OLD_PID 2>/dev/null; then \
	    echo "==> Stopping kura (PID: $$OLD_PID)..."; \
	    kill $$OLD_PID; \
	    for i in $$(seq 1 50); do kill -0 $$OLD_PID 2>/dev/null || break; sleep 0.1; done; \
	    kill -0 $$OLD_PID 2>/dev/null && kill -9 $$OLD_PID 2>/dev/null || true; \
	  fi; \
	  rm -f $(PID_FILE); \
	fi
	@portman release --name kura 2>/dev/null || true

test:
	@if [ -f go.mod ]; then \
	  go test ./...; \
	else \
	  echo "==> go.mod not found yet — no tests to run"; \
	fi

lint: fmt vet

fmt:
	@if [ -f go.mod ]; then \
	  test -z "$$(gofmt -l .)" || { echo "gofmt found unformatted files:"; gofmt -l .; exit 1; }; \
	fi

vet:
	@if [ -f go.mod ]; then go vet ./...; fi

tidy:
	@if [ -f go.mod ]; then go mod tidy; fi

clean:
	@rm -rf $(BIN_DIR) $(PID_FILE) $(LOG_FILE) $(PORTMAN_ENV) $(TAILWIND_OUT)

# ---- App registry test fixture --------------------------------------
# Build a signed local registry under tests/fixtures/app-registry/ so the
# Apps Store UI can be exercised without depending on an external service.
# `sign` runs every time manifests change; `serve` exposes :9999 on the LAN
# so the VM can fetch.

app-registry-sign: build
	bash tests/fixtures/app-registry/sign.sh

app-registry-serve:
	bash tests/fixtures/app-registry/serve.sh
