# Resona Server

Go 服务端使用 Noise UDP，默认监听 `127.0.0.1:9988`。转发 Opus，不依赖音频设备、codec 或 C 工具链。当前实验版本与客户端配套升级，握手版本见 [协议](noise-protocol.md)。

## 本地运行

```sh
CGO_ENABLED=0 go build -o build/bin/resona-server ./cmd/resona-server
build/bin/resona-server --init-key
build/bin/resona-server --init-owner
build/bin/resona-server --listen 127.0.0.1:9988
```

初始化私钥不会覆盖已有文件。`--init-key` 输出公钥，客户端添加地址和公钥后连接。`--init-owner` 输出一次性认领码，不写日志；用户在客户端认领成为 owner。共享连接密码通过 `RESONA_SERVER_PASSWORD` 环境变量配置，缺省为空。

`--noise-key` 指定 32 字节服务器私钥；默认位于系统用户配置目录的 `resona-server/noise.key`。`--access-dir` 指定所有者与频道持久化目录，默认 `resona-server/access`。备份私钥和完整权限目录，不提交仓库。

## 认领与频道

- 未认领服务器不会因认领码过期而不可恢复。未使用的码有效 24 小时。
- 服务停止后执行 `--reset-owner-claim` 生成新的 24 小时代码；已存在 owner 时拒绝覆盖。
- CLI 与运行服务通过权限目录锁互斥，不在线改写认领文件。
- owner 通过客户端创建、改名、修改描述/码率、删除空频道。普通成员不能越权。
- 默认频道不能删除；非空频道不能删除。频道数据保存在权限目录的 `channels.json`。
- `--channels` 可指定初始 JSON 数组，包含 ID、Name、Description、Bitrate；已有持久配置优先。
- 当前只有 owner/member，管理员委派、踢人、封禁和私密频道尚未实现。

## Docker Compose

```sh
docker compose up -d --build
docker compose logs server
```

首次启动自动生成私钥并输出公钥，默认只暴露本机 UDP 9988。数据保存在 `server-data` 命名卷；重建容器保留身份和频道。不要使用 `down -v` 清除需要保留的数据。

认领码需要离线操作，同一数据卷不能有两个进程持有权限目录锁：

```sh
docker compose stop server
docker compose run --rm server --init-owner
docker compose up -d server
```

刷新未使用的过期认领码时，将 `--init-owner` 换成 `--reset-owner-claim`。通过可信渠道交给预期的所有者，不贴入工单或公共日志。

公网运行需设置 `RESONA_BIND_IP=0.0.0.0` 并开放所选 UDP 端口，默认 9988。`RESONA_PORT` 修改主机端口。Compose 默认非 root、只读根文件系统、无额外 capability，日志滚动限制为两份 5MiB。

## 运维边界

`resona-server --version` 输出版本、提交、dirty 状态和构建信息。开发树构建不是正式发布。更新时先备份数据，保留可回退镜像，客户端与服务端同时升级；部署到用户公网服务需单独授权。

命令和语音有速率上限、连接数上限 64、握手 cookie 与预算。慢控制消费者会断开，语音队列只保留新帧。没有完整拥塞控制、集群或 HTTP 管理服务。日志用 slog 写 stderr，不输出密码、认领码、私钥和聊天正文。
