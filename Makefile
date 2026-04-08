PROJECT_ROOT := $(shell pwd)
BIN_DIR := $(PROJECT_ROOT)/bin
GO := /usr/local/go/bin/go
BINARY := $(BIN_DIR)/webserver

.PHONY: build run clean

build:
	@mkdir -p $(BIN_DIR)
	cd $(PROJECT_ROOT) && $(GO) build -o $(BINARY) ./cmd/server/
	@echo "Build complete: $(BINARY)"

run: build
	$(BINARY) -port 8080 -contentDir $(PROJECT_ROOT)

clean:
	rm -rf $(BIN_DIR)
	@echo "Clean complete"
