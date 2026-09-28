# Fork 镜像：打包 / 启动 / 自动更新

跟踪上游的机制：fork 上永远只领先上游一个 commit（鹈鹕模块 + 这些脚本）。
`.github/workflows/custom-image.yml` 每 6 小时（以及每次 push）自动 rebase 到上游、构建 arm64 镜像并推到
`ghcr.io/marszhongx/axonhub`，上游更新后本地不需要重新构建。

## 日常：跟进上游（推荐）

```bash
docker pull ghcr.io/marszhongx/axonhub:latest
export MYSQL_USER_NAME=root MYSQL_PASSWORD=******
./scripts/docker/start.sh
```

## `docker/build.sh`

本地自行构建（改了自己代码、或 CI rebase 冲突时用），产物 tag 与 CI 相同。

```bash
# 构建要访问 Docker Hub / proxy.golang.org / npm，国内必须先开代理
export http_proxy=http://127.0.0.1:7890
export https_proxy=http://127.0.0.1:7890

./scripts/docker/build.sh
./scripts/docker/build.sh --no-cache    # 额外参数原样传给 docker build
```

**说明：** 没配 `http_proxy` / `https_proxy` 会直接报错退出。

## `docker/start.sh`

一键启动容器：停掉同名旧容器，按配置重建并等待健康检查通过。

```bash
export MYSQL_USER_NAME=root
export MYSQL_PASSWORD=******
export MYSQL_HOST=127.0.0.1   # 可选，默认 127.0.0.1
export MYSQL_PORT=3306        # 可选，默认 3306
./scripts/docker/start.sh
```

**配置：**

- 数据库连接读环境变量：`MYSQL_USER_NAME`、`MYSQL_PASSWORD` 必填，`MYSQL_HOST`、`MYSQL_PORT` 可选
- `MYSQL_HOST` 为 `127.0.0.1`/`localhost`/`::1` 时会自动换成 `host.docker.internal`，容器里才能连到宿主机 MySQL
- 数据库名 `axonhub`、容器名、端口、数据目录在 `start.sh` 顶部直接改
- 镜像默认 `ghcr.io/marszhongx/axonhub:latest`，可用 `AXONHUB_IMAGE` 覆盖
- 启动会跑数据库自动迁移（不要加 `AXONHUB_DB_DISABLE_AUTO_MIGRATION`，否则代码新增字段会报 `Unknown column`）

## 自动更新（watchtower）

机器上跑了一个 watchtower 实例，盯 `wb-proxy` 和 `axonhub` 两个容器，每个整点检查一次 GHCR，有新镜像就自动重建：

```bash
docker run -d --name watchtower --restart unless-stopped \
  -v /var/run/docker.sock:/var/run/docker.sock \
  nickfedor/watchtower:latest \
  --cleanup \
  --stop-timeout 60s \
  --schedule "0 0 * * * *" \
  wb-proxy axonhub
```

注意：watchtower 同时只能跑一个实例。第二个实例启动时会把它认为「多余的」 watchtower 容器停掉并删除（已经踩过一次），所以新容器要加进来就直接改这一条的容器列表。

改成每天凌晨只跑一次：`--schedule "0 0 4 * * *"` 加 `-e TZ=Asia/Shanghai`（`wb-proxy` 也会跟着变）。

## CI rebase 冲突了怎么办

workflow 失败会发通知。本地解决一次即可：

```bash
git fetch upstream
git rebase upstream/unstable     # 解决冲突
git push --force-with-lease origin unstable
```
