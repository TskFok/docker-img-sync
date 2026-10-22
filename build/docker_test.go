package build

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestComposeFileContract(t *testing.T) {
	root := repoRoot(t)
	compose := readRepoFile(t, root, "compose.yaml")
	example := readRepoFile(t, root, ".env.example")

	requireSubstrings(t, "compose.yaml", compose, []string{
		"image: mysql:8.4",
		"MYSQL_ROOT_PASSWORD: ${MYSQL_ROOT_PASSWORD:?请在 .env 中设置 MYSQL_ROOT_PASSWORD}",
		"MYSQL_DATABASE: ${MYSQL_DATABASE:?请在 .env 中设置 MYSQL_DATABASE}",
		"MYSQL_USER: ${MYSQL_USER:?请在 .env 中设置 MYSQL_USER}",
		"MYSQL_PASSWORD: ${MYSQL_PASSWORD:?请在 .env 中设置 MYSQL_PASSWORD}",
		"TZ: Asia/Shanghai",
		"--character-set-server=utf8mb4",
		"--collation-server=utf8mb4_general_ci",
		"--default-time-zone=+08:00",
		"mysql-data:/var/lib/mysql",
		`mysqladmin ping -h 127.0.0.1 -uroot -p$$MYSQL_ROOT_PASSWORD`,
		"condition: service_healthy",
		"image: dockerimgsync:latest",
		"context: .",
		"dockerfile: Dockerfile",
		`"8080:8080"`,
		"./.env:/app/.env:ro",
		"HTTPS_PROXY: ${HTTPS_PROXY:-}",
	})
	if got := strings.Count(compose, "TZ: Asia/Shanghai"); got != 2 {
		t.Errorf("compose.yaml 中 TZ: Asia/Shanghai 出现 %d 次，mysql 与 app 应各有一次", got)
	}
	requireSubstrings(t, ".env.example", example, []string{
		"MYSQL_ROOT_PASSWORD=",
		"MYSQL_DATABASE=",
		"MYSQL_USER=",
		"MYSQL_PASSWORD=",
		"主机名写 mysql",
	})
}

func TestDockerImageBuildContract(t *testing.T) {
	root := repoRoot(t)
	dockerfile := readRepoFile(t, root, "Dockerfile")
	makefile := readRepoFile(t, root, "makefile")
	ignore := readRepoFile(t, root, ".dockerignore")

	requireSubstrings(t, "Dockerfile", dockerfile, []string{
		"FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build",
		"ARG TARGETOS",
		"ARG TARGETARCH",
		"CGO_ENABLED=0",
		"GOOS=${TARGETOS}",
		"GOARCH=${TARGETARCH}",
		"go build -o /out/cli -ldflags \"-w -s\" -trimpath ./bin/cli/main.go",
		"FROM alpine:3.21",
		"ca-certificates tzdata",
		"ENV TZ=Asia/Shanghai",
		"COPY --from=build /out/cli /app/cli",
		"EXPOSE 8080",
		"ENTRYPOINT [\"/app/cli\", \"serve\"]",
	})

	requireSubstrings(t, "makefile", makefile, []string{
		"IMAGE ?= dockerimgsync",
		"TAG ?= latest",
		"docker-build: docker-build-amd64 docker-build-arm64",
		"docker-build-amd64: require-docker",
		"docker buildx build --platform linux/amd64 --provenance=false --sbom=false --load -t $(IMAGE):$(TAG)-amd64 .",
		"docker-build-arm64: require-docker",
		"docker buildx build --platform linux/arm64 --provenance=false --sbom=false --load -t $(IMAGE):$(TAG)-arm64 .",
		"未安装 Docker，无法构建镜像",
		"未安装 Docker buildx，无法跨平台构建镜像",
	})

	requireSubstrings(t, ".dockerignore", ignore, []string{
		".git",
		".env",
		"cli",
	})
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("无法定位测试文件")
	}
	return filepath.Dir(filepath.Dir(file))
}

func readRepoFile(t *testing.T, root, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", name, err)
	}
	return string(data)
}

func requireSubstrings(t *testing.T, name, content string, wants []string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Errorf("%s 缺少约定内容 %q", name, want)
		}
	}
}
