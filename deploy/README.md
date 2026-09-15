# Resona Linux 部署包

`deploy/` 保存可复用源码；`make deploy` 仅生成文件，不 SSH、不上传、不重启服务。

在仓库根目录执行，默认读取根目录 `VERSION` 并添加 `v` 前缀。临时覆盖仍可传 `VERSION=v0.1.1-test`，不会修改文件：

```sh
make deploy
make deploy DEPLOY_ARCH=arm64
```

默认架构为 amd64，与构建机器架构无关。产物在 `build/deploy/resona-server-版本-linux-架构/`，旁边有同名 `.tar.gz`。包包含静态 Linux 二进制、预编译 Dockerfile、受限构建上下文、升级脚本、许可证和 Go 构建元数据。开发树的 dirty 状态如实保留；仅指定版本号不代表正式发布。

生成器只复制明确列出的公共文件，不读取本机部署目录、私钥、密码或认领码。`make clean` 会删除新的 `build/deploy/`，不要在那里存放持久数据。旧的 `build/deploy-*` 目录可能包含私有备份，不自动删除或迁入 Git。

## 在目标机器构建镜像

上传并解压与目标 CPU 匹配的 tar.gz，进入解压目录：

```sh
docker build -t resona-server:v0.1.1 .
docker run --rm resona-server:v0.1.1 --version
```

目标机器只需要 Docker，不需要 Go、Rust 或仓库源码。镜像基于 scratch，默认以 UID/GID 10001 运行，没有 shell。上传地址不写死在脚本内。

## 更新已有 data/access 部署

`activate.sh IMAGE` 用于已有的独立 Docker 容器，数据布局为：

```text
/opt/resona/
  data/noise.key
  access/              # 所有者、权限和频道数据
  backups/             # 升级时生成，包含敏感数据，不上传
```

保持 data/access 的现有权限，容器 UID 10001 必须可读私钥、可写 access。执行前，核对下面的目录、容器名及公网绑定。脚本默认绑定 127.0.0.1；公网部署显式使用 0.0.0.0：

```sh
RESONA_ROOT=/opt/resona \
RESONA_CONTAINER=resona-server \
RESONA_BIND_IP=0.0.0.0 RESONA_PORT=9988 \
sh ./activate.sh resona-server:v0.1.1
```

脚本要求旧容器正在运行、新镜像已存在。它保存旧容器的环境变量到私有备份目录，停止旧容器后备份 data/access，再保留旧容器并启动新镜像。环境可能包含连接密码，备份目录/环境文件分别使用 0700/0600 权限，不打印环境内容。其他 Docker 参数由脚本明确指定，不复制任意旧容器的网络、挂载或启动参数；有自定义布局时先调整部署配置，不直接套用。

备份或启动失败会尝试重新启动旧容器，并报告恢复失败。成功后保留旧容器和数据备份，便于人工回退。启动检查只确认容器两秒后仍运行，不等于客户端握手、语音或数据迁移验收。旧新版本共用 access 目录，自动恢复容器不会自动恢复已经修改的数据；跨数据格式版本回退需要停服后从备份恢复。

首次安装可沿用仓库的 `compose.yaml` 和 `deploy/server-entrypoint.sh` 初始化流程；本升级脚本不创建或覆盖身份和 owner。命名卷 Compose 布局不直接适用此脚本。
