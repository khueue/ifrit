__default:
	cat Makefile

.PHONY: build install clean test cover run help fmt fix vet check nilaway

# Binary name
BINARY_NAME=ifrit

# Build directory
BUILD_DIR=bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GORELEASE=CGO_ENABLED=0 $(GOCMD) build -trimpath
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

fmt:
	$(GOCMD) tool goimports -w .
	$(GOCMD) tool gofumpt -w .

fix:
	$(GOCMD) fix ./...

vet:
	$(GOCMD) vet ./...

nilaway:
	$(GOCMD) tool nilaway -exclude-pkgs=github.com/khueue/ifrit/cmd ./...

check: fix fmt vet

# Build the project
build:
	@ echo "Building $(BINARY_NAME)..."
	@ mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(BINARY_NAME) -v

# Install to /usr/local/bin
install: clean
	@ echo "Building and installing $(BINARY_NAME) to /usr/local/bin..."
	@ mkdir -p $(BUILD_DIR)
	$(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)
	@ sudo cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@ sudo codesign -s - /usr/local/bin/$(BINARY_NAME)
	@ echo "$(BINARY_NAME) installed successfully!"

# Install to ~/bin (no sudo required)
install-user: clean
	@ echo "Building and installing $(BINARY_NAME) to ~/bin..."
	@ mkdir -p $(BUILD_DIR)
	$(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)
	@ mkdir -p ~/bin
	@ cp $(BUILD_DIR)/$(BINARY_NAME) ~/bin/$(BINARY_NAME)
	@ codesign -s - ~/bin/$(BINARY_NAME)
	@ echo "$(BINARY_NAME) installed to ~/bin"
	@ echo "Make sure ~/bin is in your PATH"

# Clean build artifacts
clean:
	@ echo "Cleaning..."
	@ $(GOCLEAN)
	@ rm -rf $(BUILD_DIR)

# Run tests
test:
	@ echo "Running tests..."
	$(GOTEST) -race ./...

# Run tests and check that enough of the code is covered by them.
# The end-to-end tests run a coverage-instrumented build of ifrit, so that
# the commands they exercise count as well.
COVER_MIN=90
COVER_DIR=$(CURDIR)/$(BUILD_DIR)/cover
COVER_PKGS=$(shell $(GOCMD) list ./... | grep -v -e /internal/check -e /internal/fakedocker | paste -sd, -)

cover:
	@ rm -rf $(COVER_DIR) && mkdir -p $(COVER_DIR)/data
	@ $(GOBUILD) -cover -coverpkg=$(COVER_PKGS) -o $(COVER_DIR)/ifrit
	@ IFRIT_COVER_BINARY=$(COVER_DIR)/ifrit IFRIT_COVER_DIR=$(COVER_DIR)/data \
		$(GOTEST) -count=1 -cover -coverpkg=$(COVER_PKGS) ./... -args -test.gocoverdir=$(COVER_DIR)/data \
		> $(COVER_DIR)/test.log 2>&1 || { cat $(COVER_DIR)/test.log; exit 1; }
	@ $(GOCMD) tool covdata textfmt -i=$(COVER_DIR)/data -o=$(COVER_DIR)/coverage.out
	@ $(GOCMD) tool cover -func=$(COVER_DIR)/coverage.out | awk -v min=$(COVER_MIN) ' \
		$$1 == "total:" { total = $$NF + 0; next } \
		$$NF + 0 < 100 { print } \
		END { \
			printf "\nTotal coverage: %.1f%% (minimum: %d%%)\n", total, min; \
			if (total < min) { print "FAIL: coverage is below the minimum"; exit 1 } \
		}'
	@ echo "Details: go tool cover -html=$(BUILD_DIR)/cover/coverage.out"

# Download dependencies
deps:
	@ echo "Downloading dependencies..."
	$(GOGET) -v ./...
	$(GOMOD) download

# Tidy dependencies
tidy:
	@ echo "Tidying dependencies..."
	$(GOMOD) tidy

# Upgrade all dependencies (including major versions)
upgrade:
	@ echo "Upgrading dependencies..."
	$(GOGET) -u ./... tool
	$(GOMOD) tidy

# Run the application (useful for development)
run: build
	@ $(BUILD_DIR)/$(BINARY_NAME)

# Build for multiple platforms
build-all: clean
	@ echo "Building for multiple platforms..."
	@ mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64
	GOOS=linux GOARCH=arm64 $(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64
	GOOS=darwin GOARCH=amd64 $(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64
	GOOS=darwin GOARCH=arm64 $(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64
	GOOS=windows GOARCH=amd64 $(GORELEASE) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe
	@ echo "Built binaries in $(BUILD_DIR)/"

video:
	# brew install vhs
	vhs assets/demo.tape
