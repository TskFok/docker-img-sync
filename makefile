mac:
	go env -w GOARCH=amd64
	go env -w GOOS=darwin
	go env -w CGO_ENABLED=0
	go env -w GO111MODULE=on
	go env -w GOPROXY=https://goproxy.cn,direct
	go mod  tidy

linux:
	go env -w GOARCH=amd64
	go env -w GOOS=linux
	go env -w CGO_ENABLED=0
	go env -w GO111MODULE=on
	go env -w GOPROXY=https://goproxy.cn,direct
	go mod  tidy

build-file-cli:
	go build -o cli -ldflags "-w -s"  -trimpath bin/cli/main.go

build-cli-linux: linux build-file-cli

build-cli-mac: mac build-file-cli

update:
	go mody tidy

IMAGE ?= dockerimgsync
TAG ?= latest

.PHONY: docker-build docker-build-amd64 docker-build-arm64 require-docker

docker-build: docker-build-amd64 docker-build-arm64

docker-build-amd64: require-docker
	docker buildx build --platform linux/amd64 --provenance=false --sbom=false --load -t $(IMAGE):$(TAG)-amd64 .

docker-build-arm64: require-docker
	docker buildx build --platform linux/arm64 --provenance=false --sbom=false --load -t $(IMAGE):$(TAG)-arm64 .

require-docker:
	@command -v docker >/dev/null 2>&1 || { echo "未安装 Docker，无法构建镜像"; exit 1; }
	@docker buildx version >/dev/null 2>&1 || { echo "未安装 Docker buildx，无法跨平台构建镜像"; exit 1; }
