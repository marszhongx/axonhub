#!/bin/sh
# 一键打包 AxonHub 镜像：前端、后端都在 Docker 内构建，产物是一个单镜像。
#
# 用法:
#   export http_proxy=http://127.0.0.1:7890
#   export https_proxy=http://127.0.0.1:7890
#   ./scripts/docker/build.sh
#   ./scripts/docker/build.sh --no-cache    # 额外参数原样传给 docker build
#
# 构建要访问 Docker Hub / proxy.golang.org / npm，国内必须走代理。
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
REPO_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
IMAGE="${AXONHUB_IMAGE:-axonhub-pelican:latest}"

: "${http_proxy:?请先 export http_proxy，例如: export http_proxy=http://127.0.0.1:7890}"
: "${https_proxy:?请先 export https_proxy，例如: export https_proxy=http://127.0.0.1:7890}"

echo "==> 构建镜像 $IMAGE"
docker build -t "$IMAGE" "$@" "$REPO_DIR"
echo "==> 构建完成"