BACKEND_DIR = .
BINARY_NAME = easytv-api
DEPLOY_SCRIPT = deploy.sh
PACKAGE_TGZ = easytv-api.tgz
GOOS ?= linux
GOARCH ?= amd64
LINUX_GOOS = linux
LINUX_GOARCH = amd64

.PHONY: all dev start-backend build build-linux package clean test check hooks

all: build

dev: start-backend

start-backend:
	@echo "Starting backend dev server..."
	@cd $(BACKEND_DIR) && go run .

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY_NAME) .

build-linux:
	CGO_ENABLED=0 GOOS=$(LINUX_GOOS) GOARCH=$(LINUX_GOARCH) go build -ldflags="-s -w" -o $(BINARY_NAME) .

# 云效构建物
package: build-linux
	@test -f $(DEPLOY_SCRIPT)
	@chmod +x $(DEPLOY_SCRIPT) $(BINARY_NAME)
	@tar czvf $(PACKAGE_TGZ) $(BINARY_NAME) $(DEPLOY_SCRIPT)
	@echo "created $(PACKAGE_TGZ)"

clean:
	@rm -f $(BINARY_NAME) $(PACKAGE_TGZ)

test:
	@mkdir -p golang-report
	go test -count=1 -short -json -cover -coverprofile=golang-report/cover.out ./... > golang-report/report.jsonl || true

# 本地检查（与 .githooks/pre-push 一致）
check:
	go vet ./... && go test ./...

# 启用仓库内 git hooks（推送前自动 go vet + go test）
hooks:
	git config core.hooksPath .githooks
	@echo "git hooks 已启用：.githooks/pre-push"
