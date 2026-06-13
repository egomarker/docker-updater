BINARY := host-updater
CMD := ./cmd/host-updater
BIN_DIR := bin

.PHONY: build test fmt clean tidy

build:
	go build -o $(BIN_DIR)/$(BINARY) $(CMD)

test:
	go test ./...

fmt:
	gofmt -w $(shell find . -name '*.go' -type f)

clean:
	rm -rf $(BIN_DIR)

tidy:
	go mod tidy
