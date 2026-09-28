#!/bin/sh
# 一键启动 AxonHub 容器：停掉同名旧容器，按下面配置重建，并等待健康检查通过。
#
# 用法:
#   export MYSQL_USER_NAME=root
#   export MYSQL_PASSWORD=your-password
#   export MYSQL_HOST=127.0.0.1     # 可选，默认 127.0.0.1
#   export MYSQL_PORT=3306          # 可选，默认 3306
#   ./scripts/docker/start.sh
#
# 容器里访问宿主机 MySQL：MYSQL_HOST 是 127.0.0.1/localhost/::1 时会自动换成 host.docker.internal。
set -eu

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"

NAME=axonhub
IMAGE=axonhub-pelican:latest
PORT=8090
DATA_DIR="$HOME/axonhub-pelican-data"
DB_NAME=axonhub

: "${MYSQL_USER_NAME:?请先 export MYSQL_USER_NAME}"
: "${MYSQL_PASSWORD:?请先 export MYSQL_PASSWORD}"
DB_HOST="${MYSQL_HOST:-127.0.0.1}"
DB_PORT="${MYSQL_PORT:-3306}"

case "$DB_HOST" in
  127.0.0.1|localhost|::1) DB_HOST=host.docker.internal ;;
esac

DB_DSN="${MYSQL_USER_NAME}:${MYSQL_PASSWORD}@tcp(${DB_HOST}:${DB_PORT})/${DB_NAME}?charset=utf8mb4&parseTime=True&loc=Local"

if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "镜像不存在: $IMAGE" >&2
  echo "先执行: $SCRIPT_DIR/build.sh" >&2
  exit 1
fi

mkdir -p "$DATA_DIR"

echo "==> 停止并移除旧容器 $NAME"
docker stop --timeout 30 "$NAME" >/dev/null 2>&1 || true
docker rm "$NAME" >/dev/null 2>&1 || true

echo "==> 启动容器 $NAME (MySQL ${DB_HOST}:${DB_PORT}/${DB_NAME})"
docker run -d --name "$NAME" \
  -p "$PORT:8090" \
  -e "AXONHUB_DB_DIALECT=mysql" \
  -e "AXONHUB_DB_DSN=$DB_DSN" \
  -v "$DATA_DIR:/app/pelican-data" \
  --restart unless-stopped \
  --log-opt max-size=20m \
  --log-opt max-file=5 \
  "$IMAGE" >/dev/null

echo "==> 等待服务就绪 ..."
i=0
while [ "$i" -lt 60 ]; do
  if curl -fsS "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
    echo "==> 已就绪: http://127.0.0.1:$PORT"
    exit 0
  fi
  i=$((i + 1))
  sleep 1
done

echo "启动超时，最近日志:" >&2
docker logs --tail 50 "$NAME" >&2
exit 1
