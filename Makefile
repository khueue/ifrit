__default:
	cat Makefile

.PHONY: build install clean test run help fmt fix vet check

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
	$(GOTEST) -v ./...

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
	$(GOGET) -u ./...
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
