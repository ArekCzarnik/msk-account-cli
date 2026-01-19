APP_NAME=msk-account-cli

.PHONY: build tidy test clean

build:
	@echo "Building $(APP_NAME)"
	@mkdir -p bin
	@go build -o bin/$(APP_NAME) ./cmd/msk-admin

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin

