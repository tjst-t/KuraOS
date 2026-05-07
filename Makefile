PROJECT_NAME := kuraos
BIN_DIR      := bin
BIN          := $(BIN_DIR)/kura
CMD          := ./cmd/kura
PID_FILE     := /tmp/$(PROJECT_NAME)-dev.pid
LOG_FILE     := /tmp/$(PROJECT_NAME)-dev.log
PORTMAN_ENV  := /tmp/$(PROJECT_NAME)-portman.env

.PHONY: build serve stop test lint tidy fmt vet clean

build:
	@mkdir -p $(BIN_DIR)
	@if [ -f go.mod ]; then \
	  go build -o $(BIN) $(CMD); \
	else \
	  echo "==> go.mod not found yet — skip build (project bootstrap pending)"; \
	fi

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
	  echo "==> Starting kura on port $$KURA_PORT (log: $(LOG_FILE))" && \
	  KURA_PORT=$$KURA_PORT nohup $(BIN) > $(LOG_FILE) 2>&1 & \
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
	@rm -rf $(BIN_DIR) $(PID_FILE) $(LOG_FILE) $(PORTMAN_ENV)
