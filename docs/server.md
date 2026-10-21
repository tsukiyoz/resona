# Resona Server

Go 服务端使用 Noise UDP，默认监听 `127.0.0.1:9988`。转发 Opus，不依赖音频设备、codec 或 C 工具链。当前实验版本与客户端配套升级，握手版本见 [协议](noise-protocol.md)。

## 本地运行

```sh
CGO_ENABLED=0 go build -o build/bin/resona-server ./cmd/resona-server
build/bin/resona-server identity init
build/bin/resona-server owner init
build/bin/resona-server serve --listen 127.0.0.1:9988
```

CLI 使用 Cobra；`--help`、`serve --help`、`owner --help` 查看命令。
初始化私钥不会覆盖已有文件。`identity init` 输出公钥，客户端添加地址和公钥后连接。`owner init` 输出一次性认领码，不写日志；用户在客户端认领成为 owner。共享连接密码通过 `RESONA_SERVER_PASSWORD` 环境变量配置，缺省为空。旧的根命令启动方式和 `--init-key`、`--init-owner`、`--reset-owner-claim` 保留，现有 service/container 参数仍可使用。

`--noise-key` 指定 32 字节服务器私钥；默认位于系统用户配置目录的 `resona-server/noise.key`。`--access-dir` 指定数据访问锁和旧配置导入目录，默认 `resona-server/access`。配置与聊天共用该目录下 `chat/` 中的一个内置 JetStream，也可用 `--chat-dir` 显式指定完整 JetStream 目录。备份私钥和完整数据目录，不提交仓库；定制 `--chat-dir` 时它也必须进入备份。

## 管理资源 KV

频道、单调递增的频道 ID、owner 身份和认领码摘要/有效期保存在
`RESONA_RESOURCES` KV 桶的 `state` 文档中。一次 CAS 同时完成相关字段修改。
仅保存最新修订，单值最大 128 KiB、桶逻辑上限 2 MiB，不设置 TTL，
聊天淘汰不会删除管理资源。监听地址、连接密码和 Noise 私钥仍由启动参数、
环境与私钥文件管理，不发给订阅者。

第一次创建 KV 时严格导入已有 `channels.json`、`owner.json` 和未消费的
`claim.json`，原文件不改写。以后 KV 是唯一事实来源，旧 JSON 不再生效；损坏、
未知格式、被删除的 KV 状态拒绝启动，不自动重新认领。升级前停服备份；降级须
恢复对应旧版本的数据备份，不能直接依赖保留的旧 JSON（它们可能已过时）。

服务端只有一个可取消的 KV watch，共享给所有已认证连接，再通过现有
`WatchResources` 按兴趣投影；未改变的范围不重发。快照在 watch 回复之前入队，
不增加客户端 NATS 连接、每用户 KV consumer 或周期性配置轮询。监听失败或发现
非法状态时服务有界退出，交给进程管理器恢复，不继续以过期权限运行。
未来 HTTP 管理入口应复用同一套授权/校验/修改方法，不能绕过业务规则直接写 KV；
HTTP 服务、任意 selector、逐对象 delta 和多节点管理尚未实现。见 ADR-0033。

## 近期频道文字

JetStream 嵌入 server，连接仍只走 Noise UDP，不开放 NATS 端口。
默认持久化在 `--access-dir` 下的 `chat/`，`--chat-dir` 可指定共享数据根目录（也包含管理资源 KV）。
重启、离开再进入会恢复仍在保留范围内的消息；不是永久聊天归档。

| 参数 | 默认 | 含义 |
| --- | --- | --- |
| `--chat-max-messages` | 200 | 每频道最多条数（1–10000） |
| `--chat-max-bytes` | 67108864 | 整个聊天流最多逻辑字节（至少 16384） |
| `--chat-max-age` | 168h | 消息最长年龄（至少 1s） |

触及任一限制淘汰最旧消息。文件块、索引和元数据有额外占用，逻辑上限不等于
目录严格磁盘配额。文件保存明文正文；只授权服务端账号访问，备份完整目录，
同一数据目录只运行一个 server。当前启用同步写入确认，低频聊天写盘不持有语音
成员锁；磁盘故障可能导致聊天失败/结果未知，不把成功的传输 ACK 当成保存成功。
已有 Docker 数据卷也覆盖默认聊天目录。更多边界见 ADR-0031。

## 认领与频道

- 未认领服务器不会因认领码过期而不可恢复。未使用的码有效 24 小时。
- 服务停止后执行 `owner reset-claim` 生成新的 24 小时代码；已存在 owner 时拒绝覆盖。
- CLI 与运行服务通过权限目录锁互斥，不在线改写认领文件。
- owner 通过客户端创建、改名、修改描述/码率、删除空频道。普通成员不能越权。
- 默认频道不能删除；非空频道不能删除。频道数据保存在管理资源 KV，`channels.json` 仅首次导入。
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
docker compose run --rm server owner init
docker compose up -d server
```

刷新未使用的过期认领码时，将 `owner init` 换成 `owner reset-claim`。通过可信渠道交给预期的所有者，不贴入工单或公共日志。

公网运行需设置 `RESONA_BIND_IP=0.0.0.0` 并开放所选 UDP 端口，默认 9988。`RESONA_PORT` 修改主机端口。Compose 默认非 root、只读根文件系统、无额外 capability，日志滚动限制为两份 5MiB。

## 运维边界

需要从本机构建再上传时，运行 `make deploy VERSION=dev DEPLOY_ARCH=amd64`，产物在 `build/deploy/`；支持 arm64。通用模板、已有 data/access 布局升级流程和回退限制见 [部署包说明](../deploy/README.md)。该目标只生成包，不自动 SSH 或替换服务。旧 `build/deploy-*` 下的私有备份不迁入源码。

`resona-server --version` 输出版本、提交、dirty 状态和构建信息。开发树构建不是正式发布。更新时先备份数据，保留可回退镜像，客户端与服务端同时升级；部署到用户公网服务需单独授权。

命令和语音有速率上限、连接数上限 64、握手 cookie 与预算。慢控制消费者会断开，语音队列只保留新帧。没有完整拥塞控制、集群或 HTTP 管理服务。日志用 slog 写 stderr，不输出密码、认领码、私钥和聊天正文。
