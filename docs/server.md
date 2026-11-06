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
初始化私钥不会覆盖已有文件。`identity init` 输出公钥；无密码服务器通过可信邀请公钥连接，有密码服务器可只用地址和连接密码，通过 OPAQUE 认证发现的公钥。`owner init` 输出一次性认领码，不写日志；用户在客户端认领成为 owner。所有操作使用显式子命令；根命令只显示帮助，不启动服务。参数放在对应叶子命令之后，例如 `serve --listen ...` 或 `owner init --access-dir ...`。

### 共享连接密码

停止服务，在同一数据目录显式初始化或修改连接密码：

```sh
build/bin/resona-server password set --access-dir /path/to/access --password-stdin
# 或生成随机密码，持久化成功后仅向 stdout 展示一次：
build/bin/resona-server password reset --access-dir /path/to/access
```

`set` 从 stdin 读取 UTF-8 密码，允许输入结尾一个换行（含 CRLF），不回显密码。
自定义 `--chat-dir` 时管理命令必须传入相同目录。新数据目录默认为无密码公开服；
有密码服务器必须先执行上述命令。`serve` 不再接受 `--password-file` 或密码环境变量，
旧部署升级时先停服备份，通过 `password set --password-stdin` 显式导入原密码，
再移除旧启动参数、环境变量和明文文件。不会自动读取或覆盖旧秘密文件。

私有 `RESONA_AUTH` KV 保存版本化的 OPAQUE 注册记录和配套服务端密钥，不保存
明文密码；FileStorage、History=1、无 TTL。普通管理资源、state/events 和日志不包含
该记录；完整数据备份仍须按私钥保管。初始化标记与管理资源一起保存，已经初始化
的认证桶/记录缺失、被删除、格式无效或配置了 TTL 时拒绝启动，不降级为公开服。

owner 可在后台“服务器设置 → 服务器连接密码”修改或随机重置。修改至少 8 字节、
最多 1024 字节，不允许换行/NUL；不支持通过空值关闭密码。随机重置生成至少 128 bit
随机密码，仅响应展示一次。变化先 KV CAS 持久化再发布，重启加载相同认证记录；现有连麦不重启，
新连接和重连使用新密码。后台没有旧密码查询接口。取消等待或存储/网络结果未知时，
刷新检查状态；若随机新密码没有收到，核对修订后再次主动重置。服务端不能找回
明文。写入失败回读权威记录；若连回读也失败，暂停新连接认证，恢复存储并重启，
原有语音连接继续。不会自动重发修改或向日志输出秘密。

空密码公开服必须使用含可信公钥的邀请；共享密码持有者
也可冒充同密码服务器，特别需要固定服务器身份时仍填写可信公钥。OPAQUE 仅在连接
时运行，不改变语音逐包路径。协议已升级 RN07，旧客户端须同步更新（ADR-0036）。

`--noise-key` 指定 32 字节服务器私钥；默认位于系统用户配置目录的 `resona-server/noise.key`。`--access-dir` 指定数据访问锁和旧配置导入目录，默认 `resona-server/access`。配置与聊天共用该目录下 `chat/` 中的一个内置 JetStream，也可用 `--chat-dir` 显式指定完整 JetStream 目录。备份私钥和完整数据目录，不提交仓库；定制 `--chat-dir` 时它也必须进入备份。

`serve --voice-queue-slots 16` 配置每位收听者的语音待发队列，默认 16，允许 1–256，
重启生效，不存入管理资源 KV。启动日志通过 `voice_queue_slots` 显示实际配置。
队列满时覆盖最旧包，发送前仍丢弃等待超过 100 ms 的包；增大容量不保证不丢包，
也不改变 Noise 接收队列。正式实现保留直接 socket 写入模型，不启用实验 batch writer。

### 连接与身份日志

`client connected` 是设备进入服务器的时刻，此时身份为 `identity_type=guest`，
`connection_name` 保存握手昵称；`name` 是事件发生时的当前昵称。账号在连接后恢复，
因此该条游客记录本身不是登录失败。成功注册或密码/Token 登录后另记
`client authenticated`，包含 `auth_method`、`account_uid`、`account_name` 及账号昵称。
改昵称记 `client profile updated`（含 `previous_name`），退出账号记 `client logged out`
并标识退出的账号；`client disconnected` 使用退出连接时的最新身份和昵称。
通过相同的 `session`、`member_id`、远端地址关联这些事件，不用昵称充当身份 ID。
不记录密码、Token 或私钥；这些低频日志在资源锁外输出，没有语音逐包日志。

## 管理资源 KV

频道、单调递增的频道 ID、owner/admin 身份、认领码摘要/有效期、封禁和在线设置保存在
`RESONA_RESOURCES` KV 桶的 `state` 文档中。一次 CAS 同时完成相关字段修改。
仅保存最新修订，单值最大 128 KiB、桶逻辑上限 2 MiB，不设置 TTL，
聊天淘汰不会删除管理资源。认领码由 `owner init` 显式生成并仅 stdout 展示一次，
有效期 24 小时；到期立即拒绝，不依赖物理删除。认领成功在同一次 CAS 中绑定 owner
并消费摘要；重启不延长有效期，不默认创建明文文件。已绑定 owner 不能重置认领码。
连接密码认证数据另存私有 `RESONA_AUTH` KV，不参与资源订阅；监听地址与 Noise
私钥仍由启动配置管理。

第一次创建 KV 时只导入已有 `channels.json`，原文件不改写。
旧设备的 owner/admin、管理凭据、认领码及封禁在本次账号体系升级中清空一次，
不迁移到账号；旧 `owner.json` 和 `claim.json` 不再读取。
以后 KV 是管理资源的事实来源，旧 JSON 不再生效；损坏、
未知格式、被删除的 KV 状态拒绝启动，不自动重新认领。升级前停服备份；降级须
恢复对应旧版本的数据备份，不能直接依赖保留的旧 JSON（它们可能已过时）。

服务端只有一个可取消的 KV watch，共享给所有已认证连接，再通过现有
`WatchResources` 按兴趣投影；未改变的范围不重发。快照在 watch 回复之前入队，
不增加客户端 NATS 连接、每用户 KV consumer 或周期性配置轮询。监听失败或发现
非法状态时服务有界退出，交给进程管理器恢复，不继续以过期权限运行。
原生频道命令与可选 HTTP 后台复用业务授权和校验；网页也不能绕过规则直接写 KV。
任意 selector、逐对象 delta 和多节点管理尚未实现。见 ADR-0033、ADR-0034。

## 游客与用户

所有设备先以游客连接。设备密钥仅验证连接，不代表账号或管理权限。
进入服务器后在 **设置 → 身份** 注册或使用账号密码登录。
账号仅属于当前服务器；每个账号有独立随机 UID，跨设备登录后 UID 不变。
登录会恢复服务器保存的昵称，修改资料后推送到在线客户端与管理后台；
游客昵称只用于当前连接。当前注册上限为 10000 个账号。

账号、昵称、Argon2id 加盐密码摘要和 Token 摘要保存为
`--chat-dir/accounts.sqlite`（默认 `--access-dir/chat/accounts.sqlite`）。
采用 WAL/FULL，权限 0600；角色分配仍保存在 KV，账号密码不进入 KV 或资源事件。
备份时停止服务，保存整个 chat 目录（包括 SQLite/WAL 和 JetStream），
不要只复制 SQLite 主文件。数据库损坏或未来 schema 不会自动重建覆盖。

同 UID 的新语音登录替换旧连接。每次登录轮换账号 Token，旧 Token 立即失效，
包括已经断网的旧设备；Token 默认 30 天有效，仅由客户端内部管理，不显示登录或导出入口。
客户端通过 macOS 钥匙串或 Windows 凭据库保留它，用于同目标断线恢复及退出应用后续登，
不写普通偏好。踢出停止自动重连，但不清除账号恢复凭据，用户可主动再次连接。
临时繁忙的恢复认证按现有退避重试，失效/被替换明确失败，不静默退为游客。
退出账号撤销 Token 并断开，重新连接为游客；改密码撤销旧凭据。

忘记密码时，停止服务并对同一数据目录执行（新密码从 stdin 输入，不作命令参数）：

```sh
build/bin/resona-server account reset-password --access-dir /path/to/access --username alice --password-stdin
```

输入新密码并结束 stdin；需要至少 8 字节、最多 256 字节。UID、昵称和角色保留，
旧账号 Token 与基于账号登录的后台会话失效。服务运行期间恢复命令拒绝获取锁。
本版不含中央账号、三方登录和邮箱找回。见 ADR-0035。

## 管理后台

后台静态页面编入同一个 Go 二进制，不需要 Node、独立 Web 服务或对外 NATS 端口。
默认不开启；本机访问可使用：

```sh
build/bin/resona-server serve --admin-listen 127.0.0.1:9989
```

浏览器访问 `http://127.0.0.1:9989/admin/`，使用账号密码登录。
用户先在客户端注册，再用新认领码认领 owner；普通注册不会获得管理权限。
只有 Owner 能看到“管理员”页面，按 UID、账号和昵称选取已有注册账号授予或撤销
admin。撤权不删除账号，也不踢出语音连接；若其所在频道要求管理员，则退回默认普通频道。
不提供管理员令牌或 Token 登录；旧令牌记录在读取时丢弃，后续资源写入清除，已有角色保留。

| 模块 | owner | admin | member |
| --- | --- | --- | --- |
| 后台查看 | 可以 | 可以 | 不可登录 |
| 频道新增、编辑、删除空频道、排序 | 可以 | 可以 | 不可以 |
| 移动、踢出普通成员/游客 | 可以 | 可以 | 不可以 |
| 封禁/解封非 Owner 身份 | 可以 | 不可以 | 不可以 |
| 管理其他 admin | 可以 | 不可以 | 不可以 |
| 委派角色、在线设置 | 可以 | 不可以 | 不可以 |

owner 不能被管理员踢出或封禁，所有权不可转移；同 UID 新登录仍替换旧设备连接。
在线成员操作同时核对 ID 和会话实例，持久角色/封禁按 UID 生效，不依赖昵称。
Casbin RBAC 策略目前固定在代码中，KV 保存角色分配；尚不开放任意策略编辑。

频道只分普通与管理员频道（`admin_only`），默认频道必须普通。普通用户/游客不能
主动进入管理员频道；管理员/Owner 先进入目标频道后，可以将其拖入临时参与。
临时准入不写 KV，离开、断线或更换账号 UID 即失效；邀请者离开不影响已拉入者。
权限收紧或管理员被撤权时，失去准入的成员转入默认频道，保留麦克风与收听状态。

在线设置包括服务器名称、人数上限和近期聊天的条数/容量/时长。初次启动将默认值
保存到 KV，之后 KV 优先；修改启动默认值不会覆盖已有设置。人数上限 1–64，
不能降至当前在线人数以下；每频道 1–10000 条，全服逻辑容量 16 KiB–4 GiB，
时长 1 秒–365 天。缩小保留范围可能立即删除旧消息，网页保存前要求确认。
KV 保存期望设置后应用 JetStream retention；存储错误/超时可能结果未知，应刷新核对，
不能把它视作已回滚。监听地址、Noise 私钥和数据目录仍属于启动配置。

连接密码另用 `PUT /api/admin/v1/server-password`（指定新密码）或 `POST`（随机重置）；
两者携带 `connection_password.revision` 的随机字符串，而非普通 KV 数字 revision。
仅 owner 可调用，同样要求会话、Origin 和 CSRF；原密码及 verifier 不在 state/events 中。

接口位于 `/api/admin/v1`：`login/logout/session/state/events`，频道 CRUD 与排序、
成员 move/kick、角色、封禁和 settings。写请求携带 CSRF token，
资源修改还须携带状态中的 `revision`；冲突返回 409，不自动覆盖。凭据/权限失效返回
401，页面清空数据并回登录。设置草稿跨栏目保留；网络中断时保留只读视图，重新
连接拉完整快照。取消等待只终止本地等待，已提交的操作可能仍完成，需要刷新核对。

页面通过一条 SSE 接收合并后的完整状态；每订阅仅一个容量为 1 的唤醒槽位，
共用原有 KV watch，无逐用户 NATS consumer。最多 64 个后台会话/订阅、32 个并行 API
请求；仅活跃 SSE 每 20 秒保活，慢写 5 秒截止，浏览器请求 8 秒截止，不进入语音热路径。
账号密码登录的后台会话最长 8 小时，改密码或失去管理角色后失效。

公网使用 HTTPS 反向代理，保持 Host，并显式配置实际浏览器来源，例如
`--admin-listen 127.0.0.1:9989 --admin-origin https://admin.example.com`。
仅允许配置的 Host 和写请求 Origin；Secure、HttpOnly、SameSite=Strict Cookie，
不信任转发头自动推导来源。代理须禁用 SSE 缓冲并允许长连接；本版未在公网代理环境验收。
新增 KV 字段不保证旧服务端可读，升级前停服备份完整数据，降级恢复匹配版本备份。

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
- owner/admin 通过客户端或后台创建、改名、修改描述/码率、删除空频道。普通成员不能越权。
- 默认频道不能删除；非空频道不能删除。频道数据保存在管理资源 KV，`channels.json` 仅首次导入。
- owner 可拖动排序或通过右键上移/下移。KV 数组顺序为所有客户端的共同显示顺序，
  新频道追加末尾；默认频道 ID 与位置独立，移到末尾仍是默认入口且不能删除。
  旧频道记录读取时将原首项迁移为默认 ID，首次成功写入保存 v2；回退旧 server
  需恢复升级前备份，不能让旧版读取已经写入的新格式。
- `--channels` 可指定初始 JSON 数组，包含 ID、Name、Description、Bitrate；已有持久配置优先。
- 后台支持 admin 委派、移动、踢出和带到期时间的身份封禁；私密频道尚未实现。

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

命令和语音有速率上限、连接数上限 64、握手 cookie 与预算。慢控制消费者会断开，语音队列只保留新帧。没有完整拥塞控制或集群。日志用 slog 写 stderr，不输出密码、管理凭据、认领码、私钥和聊天正文。
