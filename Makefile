.PHONY: run build test clean

# 默认目标
.DEFAULT_GOAL := run

# 运行开发服务器
run:
	go run ./cmd/server/

# 构建二进制
build:
	go build -o bin/dms-access ./cmd/server/

# 运行所有测试
test:
	go test ./... -v -timeout 120s

# 清理构建产物
clean:
	rm -rf bin/

# 代码检查
lint:
	go vet ./...
