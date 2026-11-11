# 产品与验收约定

## 自动缩放与弹窗隔离（2026-11-09）

- 自动缩放使用系统倍率 D 和当前显示器像素短边 H：R=clamp(H/1080,1,1.5)，
  最终倍率 S=max(D,R)。GPUI 已应用系统 D，主题只额外应用 A=S/D；基础字号
  为 14 逻辑像素，主界面、输入、辅助文字与 rem 控件随之调整。不暴露手动三档，
  不保存自动计算值；主题预览、取消及重置均重新使用当前显示器值。
- 启动、窗口 DPI/显示器/分辨率变化时更新，无后台轮询。依据完整显示器而非窗口
  尺寸或任务栏工作区计算，窗口缩小时不改变倍率，超宽屏和竖屏均使用短边。
  无法获取有效显示器信息时只跟随系统 DPI。这是舒适度启发式，不保证物理尺寸一致。
- 所有自定义模态弹窗共用鼠标遮挡层和焦点范围。打开后底层不响应悬停、点击、
  右键、滚轮或拖拽；已有面板、聊天高度、滚动条及频道拖拽取消。戳一下通知延后展示。
- 弹窗显示后焦点进入内部，保留主动聚焦的表单输入；Tab / Shift+Tab 留在弹窗内。
  Escape 优先关闭内部下拉菜单或浮层，再处理外层弹窗；关闭和取消沿用未应用确认及
  提交中保护，关闭恢复主界面焦点，不穿透关闭点击。
- 验收覆盖窄窗口、最大字号和深浅主题，表单能滚动且应用/取消可达；Windows 实机
  另验 100% / 125% / 150% / 200% 系统缩放及跨屏移动。macOS 验收不代替 Windows DPI 验收。

## 紧凑操作通知（2026-11-06）

- 普通操作错误在内容区上方居中显示，宽度随内容收缩、上限 360px，受当前可用宽度限制。
- 使用中性主题背景、细边框和轻阴影，正文正常文字色，仅 16px 状态图标点色，不用整条红底横幅。关闭按钮固定 24px。
- 短权限提示为“仅管理员可进入”；长错误完整换行，正文高度上限 144px，可滚动查看，文字给滚动条留出间距。
- 不抢焦点、不自动消失、不新增计时器。关闭清除当前错误和个人收听设置错误；弹窗打开时仅隐藏，退出遮罩仍覆盖通知。
- 原生预览检查深浅主题、800×900/760×600、短提示、长 URL 滚动和关闭；不影响连接、权限、语音及后台刷新。

## 身份与频道权限（2026-11-06）

- Owner 唯一，只能由已登录账号消费 24 小时的一次性初始化认领码绑定，不提供网页转让或创建第二位 Owner。
- 登录只显示账号密码及注册。账号自动续登凭据由应用内部保管；移除管理员令牌的发行、兑换、撤销、存储与旧后台 Token 登录，已有账号及角色保留。
- 仅 Owner 可见“管理员”页面，按 UID、账号和昵称选取已有注册账号授予或撤销 admin，不删除账号。管理员账号与普通账号共用同一用户体系，游客指未登录状态。
- 普通频道允许任何已连接成员进入；管理员频道仅 Owner/admin 能主动进入。默认频道始终普通，服务端和编辑页面都禁止把它设为管理员频道。
- 频道列表中普通频道使用 `#`，管理员频道使用 14px 细线盾牌；两者占位和对齐一致，沿用次要文字及选中颜色，不增加底色或动画。该标识表示访问权限，与频道创建来源无关。
- Owner/admin 已在目标管理员频道时，可以拖拽普通用户或游客进入。临时准入仅属于该连接的该频道，离开、断线或切换账号 UID 后失效；不保存、不跨重连恢复。邀请者随后离开不会驱逐已被拉入者。
- 成员拖拽与频道排序使用不同的数据类型。提交时重新核对连接、成员实例、角色和目标，过期拖拽不操作复用 ID 的新成员。失败显示成员操作错误，不伪报成功。
- Admin 可踢普通用户或游客，不能踢其他管理员或 Owner；Owner 可踢管理员。右键“踢出服务器”需确认，取消不发请求，等待防重复提交；服务端确认后连接终止且不自动重连。对方仍可手动连接，账号恢复凭据保留。
- 仅 Owner 可新增/解除封禁，任何角色不能封禁 Owner。封禁与踢出是独立权限，后台依据服务端能力显示按钮。
- 撤销 admin 或收紧频道权限后，失去准入的成员退回普通默认频道，推进语音 epoch 和聊天代次，保留麦克风/收听意图，不重开设备。未提交的编辑可取消，已提交操作不能靠关闭窗口撤销；旧连接回执不影响新会话。
- 权限检查只发生于控制操作和资源变更；语音热路径不查数据库/Casbin，不增加空闲计时器。验收覆盖注册/登录、角色变化、主动与临时加入、离开/重连、陈旧目标、确认/取消、封禁和踢出边界。

## 侧栏收纳与提示音（2026-11-06）

- 服务器栏右边界可拖动，窄区吸附为 52px 图标栏，反向拖动可展开；保留原收纳按钮及折叠后的名称提示。仅松手后保存收纳状态，不逐次鼠标移动写盘，不改变服务器选择、连接、发言或聊天草稿。
- 右侧用户/频道详情栏从左边界向右拖动，进入窄区后吸附关闭；松手关闭详情，重新点击用户/频道即可展开。拖动期间设最小可读宽度和关闭/恢复回差，未达到关闭区松手恢复标准宽度，不增加持久布局字段。
- Escape、失焦、打开弹窗或退出取消未完成拖动；窗口外松手完成当前预览。默认及最小窗口需检查边界命中、收纳后恢复、拖动方向逆转与重复操作。拖动仅响应鼠标事件，不启用动画或空闲定时器。
- 设置 → 提示音增加“戳一下”试听，复用已有 `poke` 声音与草稿音量；试听不发送网络戳一下，收听静音时禁用。总开关包含连接、成员和戳一下通知，仍经“应用”生效，取消保留原设置。
- 管理后台角色和封禁表格保留原生单元格布局，按钮在单元格内部排列；多行昵称/身份和空操作列的行分隔线保持对齐。

## 重置偏好与戳一下（2026-11-03）

- 设置 → 诊断 → 本地配置提供“重置偏好设置”。二次确认只恢复设备、声音、提示音、外观和快捷键的默认草稿；点击“应用”才按原有音频确认及串行保存流程生效。取消重置不修改草稿，取消设置恢复原已应用值；保存失败明确提示本次有效但未保存。保护状态下不覆盖损坏或未来版本文件。
- 保留服务器书签、设备身份、账号凭据及侧栏布局。重置不重连、不退出账号，也不改变当前静音/收听状态；默认自动开麦偏好只作用于下一次主动连接。
- 其他在线用户的右键菜单提供“戳一下”，跨已观察到的频道可用。按会话和成员实例校验，不能戳自己、离线用户或复用 ID 的新实例。等待期间禁止重复提交，退出或换服务器后的迟到回执不改变新页面。
- 接收方显示带发送者昵称的可关闭提醒，不抢焦点、不自动消失；最多保留 8 条，超过时淘汰最旧提醒。同一连接内切频道或进入后台仍保留，换连接/退出则清除。声音沿用提示音开关与收听静音偏好，关闭声音仍显示提醒；过期提示不补播声音。
- 服务端每发送者最多 5 秒一次、每接收者最多 2 秒一次，拒绝无效目标与队列拥塞。不持久化到聊天、不离线补发；成功仅表示进入接收方控制队列，不表示对方已阅读。不增加周期任务、逐包日志或后台动画。
- 客户端/服务端需配套升级：客户端在签名 Hello 中声明接收能力，服务端通过 State 声明发送能力。旧客户端不接收未知提醒，新客户端不在旧服务端上显示入口；由于签名字段扩展，新客户端连接旧服务端可能被拒绝。
- 验收覆盖确认/取消/应用失败、只读配置保护、当前静音保留、离线/过期目标、限频和满队列、跨频道/后台及旧会话隔离；macOS 检查最小窗口与深浅主题，Windows 原生和多人体验待维护者验收。
- 验证：Go internal/CLI/deploy、`make lint`、server race 通过；Rust client 60 项单元及 31 项 Go 互通测试、desktop 73 项单元及 1 项兼容测试、client Clippy 通过。macOS release 构建/签名通过，760×600 深浅主题提醒与关闭已检查；隔离配置实际验证取消重置、取消设置回滚及 Apply 保存默认值。tsukiyo 已备份后部署配套服务端，版本与管理 HTTP 健康检查通过。

## 桌面呈现与异步操作（2026-11-03）

- 深浅主题使用具名的表面、正文、次要文字、边框、发言、警告、错误和遮罩颜色。所有颜色角色有两套明确值；正文和状态文字在常用表面上达到 4.5:1 对比度，不再通过暗色 RGB 猜测浅色值。
- 连接页面、状态颜色、标签和操作文案从当前 Core 工作区与资源状态派生。查看其他书签不继承当前会话的频道或错误；资源失败显示“重试加载”，不会称为重新连接。资源后台刷新继续静默执行。
- 启动查询、历史消息、更新检查和能力查询不全局禁用无关操作。连接、设备/语音变更和书签写入仍阻止实际冲突；聊天、账号、频道管理按各自请求防重复。频道切换还会等待已提交的发送命令进入确定状态。
- 语音/PTT 与工作区命令结果属于提交时的会话；发送还属于当前频道和聊天代次。麦克风试听的每次启动/停止有操作序号。旧结果与未知请求 ID 不更新新会话或新试听状态。已关闭的书签/频道表单失败不覆盖新表单。取消、退出和设置应用确认沿用原有生命周期。
- 处理器选项及悬停说明只来自 Core 能力。音频不可用时禁用菜单；已保存但缺失的后端保留并标为不可用，不能静默改选。Core 仅在编译了 native audio 时存在，`voice` 表示编译能力，不表示设备已经打开或获准。
- 调试构建可通过 `resona-desktop --showcase` 独立查看离线、预览、连接准备、连接中、资源失败、恢复失败、语音失败和能力缺失的状态，以及发言/静音指示。预览使用生产主题、派生状态和组件，明确标记本地调试数据；不构造 Core、不读取配置/钥匙串、不打开网络或音频设备。正式 release 不包含此入口。
- 验收：默认 800×900 与最小 760×600 下检查深浅主题、发言指示、失败动作与菜单。回归运行真实 Audio Engine/处理链的设备替身测试，覆盖离线设置、切频道/身份、热切换处理链失败、设备打开取消与退出。Windows 原生运行及多人语音仍需维护者验收，不能由 macOS 检查代替。
- 2026-11-03 验证：Go internal/CLI/deploy 测试与 `make lint` 通过；Rust client 59 项单元和 30 项 Go/Rust 互操作测试通过，desktop 70 项单元和 1 项兼容测试通过。macOS release 与独立 debug 预览构建、签名检查通过，预览已检查两种尺寸、深浅主题、失败/能力缺失状态与退出。tsukiyo 已保留私有配置并备份数据后部署验收构建，版本检查及管理 HTTP 健康检查通过；Windows 实机和多人通话验收待完成。

## 服务器管理后台验收

- 单服务器后台使用注册账号密码，关联持久 UID；连接密码不能登录后台，网页不能创建或替换 owner。
- owner 可委派 admin；admin 管理频道与普通成员，不能管理 owner 或其他 admin。角色与封禁使用持久 UID，移动/踢出核对成员 ID 和会话实例。
- 每次修改核验当前权限与 KV 修订；过期表单保留草稿并提示重新核对，不静默覆盖。频道默认入口不能删除，非空频道删除在服务端最终判断。
- 保存和危险操作防重复提交，只在服务端确认后报成功；超时显示结果未确认，不自动重发。取消草稿与取消已提交请求不能混为撤销。
- 一条资源事件订阅更新页面；断线保留列表但禁用修改，重连取完整快照。登录过期或撤权清空数据并回登录，退出取消请求与订阅，迟到结果不能恢复旧会话。
- 页面采用紧凑侧栏、表格和设置表单；启动参数标明只读。后台访问、持久化和事件推送不得进入逐帧语音热路径。
- 窄窗口始终可退出登录；保存等待中可以取消等待或退出，说明操作可能已在服务端完成。设置草稿切栏目保留，冲突展示完整最新设置。浏览器后退恢复时重新验证会话和订阅，恢复前禁止修改。

Resona 是面向游戏连麦的语音应用。启动时不自动连接服务器或采集麦克风；本地预览和真实网络状态明确区分。

## Rust 客户端迁移验收

迁移首步为 UI 无关的传输/协议基础，产品继续使用现有 Go Core，直到替代行为
覆盖完整。Noise 握手不发布应用连接成功；真实本地 server 验收须包含 signed
Hello、合法 Welcome 和持久 UID 校验。等待有界、可取消，拒绝与取消不显示成功。
取消读取保留部分控制帧；已提交写取消/超时关闭传输；退出回收 worker。
后续接入 GPUI 前，须继续满足现有静音重连、聊天保存确认/历史、资源失败不影响
健康语音、设置确认、身份/偏好保护和有界退出。不能直接继承旧子进程强制回收
作为进程内原生调用的保证。回环不代表设备、Windows、WAN 或性能验收完成。
审查与阶段划分见 ADR-0032。

## 服务器与连接

- 添加/编辑表单仅含名称、地址、昵称、服务器公钥；没有传输选择或证书输入。
- 公钥可选，填写时必须是 64 位十六进制。有密码服务器可只填地址、连接密码；无密码服务器必须通过可信邀请提供公钥。格式错误保留输入；取消或 Escape 不保存。
- 旧书签保留显示、编辑、删除能力，但不能连接；提示用户使用原生服务器地址及公钥重新添加。
- 删除书签先清理其保存密码及本地登录凭据，再持久化列表；清理失败保留书签，在当前
  删除弹窗直接显示原因并允许重试。部分凭据可能已经清除，不承诺失败后仍全部保留。
  等待显示“正在删除”并阻止重复提交；关闭只停止等待展示，不撤销已提交操作。重新
  打开同一书签仍显示处理中，迟到响应不能关闭其他弹窗；核心异常须在弹窗内可见。
  成功以凭据清理及书签保存完成为准，重启后仍不存在。macOS 只定位目标钥匙串条目
  引用进行删除，不为删除读取密码、不放宽 ACL；未找到条目视为已清理，其他错误保留。
- 读取目标凭据前验证书签。凭据准备失败保留原会话；目标地址或公钥改变后不借用旧密码。
- 连接可取消。失败不能显示成功、保存错误密码或恢复迟到会话。主动连接成功后自动收听，并按“连接后自动开启麦克风”偏好启用采集，偏好默认开启。关闭偏好则静音进入；普通切频道保留手动静音/停用/耳聋，自动重连仍静音。PTT 不继承按住状态。设置确认只保存下一次主动连接的偏好，不取消当前静音。

### 共享连接密码与信任

- 添加/编辑服务器显示名称、地址、昵称和密码，不再提供公钥输入框；已有书签的可信
  pin 仍在内部保留并严格校验。新书签按地址与密码建立 PAKE 认证，不自动固定发现的 key。
- 密码框每次打开为空且默认遮蔽，不读取系统中已有密码。填写新密码并保存会显式更新
  系统凭据库，不连接服务器；留空只保留同一目标的原密码。地址变化不复制旧密码或
  账号 Token。密码不进入书签 JSON、工作区和日志。关闭/重新打开清空表单秘密，回车
  等同保存；未提交的取消不修改数据，已提交的保存不会因关窗而撤销。
- 密码存储失败不保存书签；密码已更新但随后书签保存失败时明确提示部分完成，允许
  重试。新目标书签保存失败则清理刚写入的密码，清理失败也明确报错。元数据与系统
  凭据不承诺跨存储事务；留空保存无需系统密码读取权限。
- 密码错误等终止连接失败后，可直接编辑或删除该服务器书签，无须先退出。保存成功后
  清除旧失败会话和错误，重新连接使用更新后的书签；删除成功后不保留失效服务器选择。
  校验、凭据清理或保存失败保留书签并显示原因，取消未提交的表单不改变连接。
  正在连接、已连接及后台重连的当前书签仍须先取消或退出；其他书签可独立编辑、删除。
- 地址与密码连接沿用原位置等待/取消，内部发现公钥并完成绑定连接的 PAKE，不向用户展示中间握手阶段。不把自动发现的 key 写进书签，不改变已存凭据键。
- 无公钥且无密码在联网前说明需要连接密码或可信邀请。已填写公钥不匹配、密码错误立即失败，不重试认证、不回退公开模式、不发送昵称/身份/账号 Token，也不覆盖已保存密码。
- 退出、超时或取消关闭临时连接；恢复连接重新认证，沿用已有会话/意图代次检查。
- 后台“服务器设置 → 服务器连接密码”只由 owner 修改或随机重置。私有 KV 持久化 OPAQUE 认证记录及配套密钥，无 TTL；状态只显示是否启用和随机编辑修订，绝不读回密码或认证记录。记录损坏或丢失拒绝启动，不回退环境变量或公开服。
- 修改填写两次，至少 8 字节、最多 1024 字节、合法 UTF-8，不允许换行或 NUL，不静默 trim，不接受空值关闭密码。随机重置先确认，新密码仅本次响应显示，可复制；完成、关闭、登出、权限失效、离页与浏览器返回恢复都清除一次性内容。
- 改密成功后新连接使用新密码，原连麦不中断；旧认证尚未入服的连接拒绝加入。并发编辑返回冲突，用户核对后才可重提。取消等待/网络或持久化结果未知不自动重试；刷新核对修订，随机新密码未收到时需再次主动重置，不能找回明文。服务端回读协调不确定写入；回读也失败则暂停新连接认证，提示恢复存储并重启，不断开原连麦。取消不发布迟到的秘密展示。
- Owner claim 由显式命令生成，持久化成功后 stdout 展示一次，不落日志或默认明文文件。KV 只保存摘要和固定 24 小时到期时间；成功认领以一次 CAS 同时绑定 owner、消费认领码。到期或重放拒绝，丢失只能显式重置未认领码；已有 owner 不可借此覆盖。
- 自动开麦设备失败时尝试一次静音收听回退，保留连接并显示设备错误；回退失败仍允许用户重试或退出。代数、操作序号、取消与设备期限继续隔离迟到完成。验收覆盖偏好开/关、重复进入回执、权限失败、切频道、取消和重连。
- 所有者认领和频道管理由服务端授权；普通成员不能靠构造 IPC 或控制帧越权。
- 离开服务器后清空本地频道、成员及聊天视图。成功换频道先清空旧视图，再恢复目标频道仍被服务端保留的近期消息；等待、失败且仍在原频道时保留记录。旧频道或旧会话迟到消息与发送结果不能重新填入当前聊天。自动重连等待期间保留未确认消息，主动取消后清空。

### 频道与身份切换期间的语音

同一连接内换频道或游客注册/登录成功，保留设备选择、3A、增益、发言方式、
麦克风静音及收听状态，不读取另一套账号音频默认值。身份或频道代次变化时
丢弃旧范围 PCM，保留已打开设备与发送侧 AEC/ANS，只清空接收侧解码、抖动、
AGC 与发言状态。播放回调按生产者边界跳过旧 PCM，不重置并发环形缓冲游标。
普通成员/聊天状态刷新不重建设备；真正断开、设备选择/采集意图变化仍释放或
重建设备，迟到设备打开不能恢复已取消的范围。
PTT 按住状态不跨代次继承，须重新按下。退出使所有未完成设备打开失效。

设置使用“身份”栏目；权限面板与身份页统一显示游客、成员、管理员、所有者。
注册提交期间防重复，失败保留原身份和语音；只有服务端确认后成为账号成员。
注册使用填写的昵称，未填写则沿用游客当前昵称；之后昵称以当前服务器账号为准。
设备、音量和处理器偏好只保存在本机，不上传为账号配置，也不在注册后重置。
关闭身份页不撤销已提交的注册；迟到结果不得重开面板或修改其他服务器的视图。

### macOS 密码授权

密码状态查询不弹系统授权；显式进入才读取目标密码，单次读取不自动重试，成功后
仅在 core 进程内缓存。拒绝授权保留原会话并回到手动输入路径，不要求点击“始终允许”。
系统授权窗口已打开时由 macOS 控制，应用不能保证立即取消该原生调用；退出仍保留
现有有界回收。无自动读取全部密码、条目删除或 ACL 放宽。

本地打包在唯一有效证书可用时自动采用稳定身份，多个身份须显式选择；无证书的
ad-hoc 开发包明确警告其跨构建授权限制。CI 未显式配置身份时不自动探测证书。
验收用同一证书签名的两个不同构建，首次进入仅点“允许”，验证后续重启/重建的
提示次数。旧 ad-hoc 条目可能同时存在 ACL 与 partition 不匹配，不宣称签名修复能
将其首次迁移提示强制合并成一次；如需迁移，只对用户指定条目重新保存后复测。

## 自动重连与诊断

- 已连接会话意外断线进入“正在重连”，可取消；首次连接临时失败最多三次尝试，明确认证拒绝立即停止，失败后提供显式重试与退出。
- 无每秒倒计时刷新；等待中禁止发送、频道管理和试听，切换服务器会取消旧重连。编辑当前目标须先取消。
- 恢复原频道后继续收听，保留设备、音量、增益、发言模式和收听静音；麦克风保持静音，用户确认后开启，PTT 重新按下。原频道不可用时明确提示并保持静音。
- 重连等待暂存当前文字但隐藏文字面板；恢复连接后用当前频道保留历史重建视图，不自动重发未确认消息。草稿不是已发送消息，仍按书签与频道隔离。取消、退出和迟到完成不能重新连接或开启麦克风。
- 设置的“诊断”页提供“导出诊断”；等待时禁用重复点击，通话与关闭窗口不受阻。成功关闭文件后定位文件，失败回显且允许重试，不定位半成品、不自动上传。
- 默认仅保留最多 512 条、最近 15 分钟的内存事件，音频热路径只计数。启动、闲置、退出均不写诊断文件；进程退出会丢失未导出的记录。空窗口也可导出合法文件。
- 导出使用独立快照，记录端不等待快照锁；导出中退出取消后续操作并清理未完成文件，迟到回复不能在退出时打开文件管理器。已经完成的导出文件保留给用户。
- 验收覆盖首次失败、退避取消、连接中取消、恢复频道时再次断线/取消、认证失败停止、服务重启身份保持及静音恢复。

## 音频处理

2026-10-21：麦克风 ANS 增加 RNNoise（标准模型），与 SpeexDSP、现有 WebRTC NS
独立选择，不改变已保存偏好和默认值。各算法菜单项及当前选项在悬停时显示 Core
提供的简短说明；不承诺消除所有背景交谈或保持所有音色。RNNoise 不显示传统降噪
等级。选择只更新草稿，切换清除不兼容参数；确认后沿用异步应用、对应操作回执和
帧边界替换。应用中禁用重复确认、编辑、取消及关闭设置，应用退出仍可用；失败、
超时、会话变化保留草稿，不保存未确认选择。取消或确认放弃恢复基线。库缺失时
已保存选项明确显示不可用，不静默切换其他算法；离线保存不打开音频设备。
能力探测会短暂加载/卸载原生库核验 ABI，不创建 RNNoise 模型状态；未选用时不
保留库句柄，不新增空闲定时器或处理线程。此条更新早期“选用前完全不加载”描述。

支持持续、按键、音量阈值三种发言方式；模式变化不解除静音。PTT 释放、断开、停用及退出清除发送状态。

发言指示在频道成员昵称左侧与底部本人昵称旁共用固定 16px 槽位：空闲显示 8px
灰色圆点，实际发言显示 12px 蓝色圆点并提亮昵称，长昵称截断，不挤压指示或按钮。
麦克风静音、暂停收听、个人本机静音分别显示对应斜线图标；暂停收听时仍可发送，
本人或远端实际发言优先显示活动圆点，不能把对方暂停收听解释为无法发送；
本机静音的悬停说明明确为“已在本机静音”。本人活动来自成功发送的有效语音，
其他成员活动来自实际播放，不能表示其他人一定听见本人；沿用音频核心约 300ms
释放延迟，不新增动画、轮询或定时器。两处复用当前频道/会话与实际音频状态，
切频道、断开、停用、配置等待及退出不保留蓝色；预览不模拟真实网络发言。
验收覆盖 PTT、语音检测、持续发送、静音/恢复、失败、深浅主题与最小窗口。

全局收听增益默认 +6 dB（约 2 倍振幅），个人默认 0 dB；两者相加（线性增益相乘），仅作用于本机收听，麦克风与提示音独立。两层均为 -20 至 +18 dB、1 dB 步进，保留独立静音，调整不解除静音。工具栏可调整全局，个人可用右键或详情，详情整数输入回车确认、越界截断，非法输入保留实际增益。失焦取消未提交输入。全局恢复默认 +6 dB，个人恢复原始 0 dB。全局跨重启保存，在线失败不保存未应用值；个人仍按会话与成员实例隔离。总和过载时使用无额外缓冲的帧峰值限幅，不能恢复录入时已经失真的声音。验收覆盖增益叠加、上下限、静音保持、失败与迟到回复、偏好恢复和窄窗排版；实际听感及 Windows 游戏性能独立验收。

2026-10-30 通话控制：移除“启用/停用语音”按钮。麦克风按钮只开关发送，耳机按钮
只开关收听；停止收听不自动静音麦克风，不取消 PTT 发言资格，底栏明确显示收听已关闭
而麦克风仍可发言。本人发言仍显示蓝点，对方关闭收听不妨碍显示其实际发言。
两者关闭时停止设备；任一恢复时按需打开相应设备。连接后自动开麦偏好与重连静音不变。
未连接时禁用按钮；更新、试听、切频道或退出期间阻止冲突操作，成功后按实际状态回显，
失败保留错误提示。设备未就绪时按钮可直接重试，点击麦克风开启发送，点击耳机恢复收听，
不覆盖另一方向的意图。取消、断线和退出仍隔离迟到设备操作。
验收覆盖四种输入/输出组合、PTT、设备失败重试与状态回显；真实设备与 Windows 测试独立记录。

在线麦克风试听复用设备，只把本人输入混入本机输出，不发送给其他成员；停止、关闭设置或切频道恢复之前意图，断线/退出取消。后台按键、设备热插拔、声学回声与 Windows 游戏负载必须实机验收。

频道音质为省流 20、游戏 32、高清 48 kbps 或自定义 16–64 kbps，Opus 单声道 48kHz/20ms。改码率不重建设备。子频道、音乐立体声和自动带宽适应不在当前能力中。

### 频道顺序

服务端按持久数组顺序展示频道，默认频道是独立的不可变 ID，不由显示位置决定。
当前服务器已连接、完整资源可用且获得排序授权时，可按住频道行上下拖动；频道行
上下半区显示对应插入线，成员行不是排序目标。拖动不进入频道，Escape、无效区域
释放、离服、切换展示服务器、权限丢失和退出均取消未提交拖动。右键上移/下移复用
同一命令，首末边界禁用；自身和原位不提交。列表可用滚轮滚动，不增加闲置任务。

最后一个频道及其成员下方的空白区域提供独立右键“新建频道”入口；短列表填满剩余
空间，长列表滚动到底后保留至少 40px 的空白，不压缩频道或成员行，也不覆盖它们
原有的右键菜单。仅当前服务器的已连接会话、资源可用且有频道管理权限时可操作，
已有频道操作等待中禁止重复提交。打开表单和提交时重新核验权限与会话；取消不
提交，失败保留草稿与错误，断开、切换服务器或权限撤销不得操作旧会话。新建仍
通过现有异步命令，按服务端持久顺序追加到末尾，不先在本地插入或额外发送排序。

释放后至多发出一次异步命令，标题原有状态位置显示保存中，等待期间禁止重复排序
及频道管理，语音和退出保持可用。列表仅接受服务端资源快照，不做乐观改序。成功
不改变默认频道、当前频道、成员、详情、草稿、静音或 PTT，也不产生加入/离开音。
明确拒绝显示原因，维持当前权威顺序；超时/断线提示排序结果尚未确认，不自动重发
或回滚，继续接受服务端权威更新。旧会话回执不能更新列表或显示迟到错误；退出
不等待无限期确认。并发排序按当前有效 ID 锚点执行，目标消失明确拒绝。

## 成员提示

- 仅对已建立会话、本人当前频道其他成员的真实加入/离开生成提示。
- 初次同步、范围扩大/缩小、前台恢复列表、自己换频道不产生批量成员音。
- 每个有序状态在核心中处理，快速加入后离开保留两个事件；通知队列最多 64 条。
- GUI 保留事件 ID 去重、过期过滤、同类声音限频和用户开关。后台收到轻量通知，不重新应用资源列表。
- 退出或新会话不播放旧会话迟到通知。

## 资源与聊天

前台实时事件更新，连续五分钟没有收到有效资源数据才兜底同步：成功同步或当前关注范围内递增 revision 的 Watch 推送都会重新计时；聊天、语音包、重复状态及旧范围数据不续期。后台合并普通呈现状态，保留语音、权限、会话及必要命令，停止兜底计时；切换会话或关注范围丢弃旧计时。资源状态与连接状态独立：首次同步、可用、静默刷新、同步失败。已有数据时，恢复前台或兜底刷新异步进行，保留已有内容，不显示短暂提示，不禁用普通交互。每轮最多三次尝试（初次加两次重试），每次上限 2 秒，退避 300/600 毫秒；明确权限或协议拒绝立即失败。

首次资源同步留在服务器连接入口，频道树、详情和文字区待必要资源就绪后一次呈现，不展示部分范围的列表；底层网络和语音仍异步运行。失败保留内部缓存但隐藏受阻内容，在原处显示“无法加载服务器信息”和具体原因，提供重试和退出服务器；资源重试只重新同步，不重复连接或读取密码。失败后不因切应用、资源事件或五分钟兜底自行重复请求。语音工具栏始终可用，资源回复超时/取消不关闭健康语音连接；阻塞控制写失败则按真实传输故障处理。关注更新合并至最新范围，同时只有一个进行中的请求；Rust 核心让同一会话已提交的请求安全完成再应用最新范围，避免取消半帧控制写。退出取消请求与退避，旧会话/旧关注代次事件不得覆盖当前状态。后台失败不激活窗口。没有资源提示计时器或新增常驻探活。会话、命令等待、对象和权限检查保留，服务端校验最终操作。当前实现以窗口是否活跃区分前后台，不是系统遮挡检测。

服务器页只保留中央一处连接入口，不叠加顶部状态卡、遮罩、重试次数或重复的连接/取消按钮。点击后原位置固定尺寸按钮立即显示“正在连接…”，覆盖音频偏好准备、保存密码读取、内部重试和必要资源等待；等待期间禁重复，提供取消。连接成功且必要资源就绪才呈现频道，聊天历史独立加载。准备阶段取消使请求代次失效，迟到的偏好、凭据和连接回执不能重新发起连接或打开密码框；核心连接开始后取消沿用断开清理。切换查看其他书签不冒用当前服务器的等待或错误。验收覆盖最小窗口、深浅主题、错误换行、准备/握手/资源等待的取消，以及成功后一次进入频道。

首次连接临时错误内部最多三次尝试，每次建立连接上限 8 秒，退避 300/600 毫秒；整轮最多 30 秒，可取消。密码、公钥、身份、协议等明确拒绝立即失败。最终失败在原服务器页显示“连接失败”和具体原因，提供重新连接、编辑服务器与重新输入密码；提交密码后收起密码框，由 Core 接管重试，不逐次访问钥匙串。断线恢复的前三次尝试也使用同一连接入口，退避约 1/2/4 秒加抖动；仍失败则在原处明确连接中断及后台恢复，保留取消和手动重新连接，不反复展示每轮状态。底层继续每次约 25–30 秒的慢速恢复，退出取消整个 worker。恢复原频道失败明确提示，恢复后麦克风保持静音，不重发聊天或管理命令。真实连接断开与资源同步失败不混为一个状态。

频道和紧凑成员行占主要工作区，成员资料保留在右侧；单击查看、双击进入频道。底部为“频道文字”，初始 210px，可拖动整个面板，范围 160px 至窗口高度 55% 且不超过 480px；折叠为 36px，不挤掉底部通话工具栏。

频道区始终展开、自适应剩余宽度，不提供频道栏收起按钮。旧偏好中的频道收起值忽略，不覆盖其他设置；左侧服务器栏收起与底部文字折叠保留且独立。前台查看当前服务器时观察全部频道，后台仍收敛订阅，不以服务器栏收起代替频道观察范围。

文字面板从当前展示服务器内已加入的有效频道派生。未连接、连接等待/失败、尚未加入频道、重连等待或查看另一服务器书签时，不挂载面板、标题、输入框或拖动边界，也不保留占位。返回仍在通话的服务器恢复本次频道文字；导航收纳和查看其他频道详情不改变文字归属。切频道等待保留原频道面板并禁用发送，失败/取消且仍在原频道时保留；主动断开后隐藏并清空。隐藏面板停止拖动，独立错误通知和通话工具栏仍可用。本地预览仅在预览上下文存在有效频道时展示。验收覆盖这些状态及退出，不能把选中频道当成已加入频道。

文字提供有界近期历史：默认每频道 200 条、全服聊天流 64 MiB、最长 7 天，任一限制达到淘汰最旧消息，服务端可调整。重启服务器仍保留范围内的消息，不承诺永久存档。进入/重新进入后，首批历史沿用会话有界缓存积累，就绪后一次投影到界面，不逐条展开历史；必要频道资源和语音不等待历史。加载未完成时不显示“暂无频道消息”，就绪前禁止发送；仅超过 300ms 才显示“正在加载频道消息…”。成功显示原始时间和稳定顺序，不把历史计为新消息，随后实时消息即时更新。失败立即明确显示“频道消息加载失败”及重试，不展示不完整的首批内容；语音、频道切换和退出仍可用，重试不阻塞核心主循环。退出和切频道取消旧订阅，迟到历史不得跨范围出现。

客户端显示至多 500 条、正文/昵称/错误合计至多 512 KiB，实时新消息超出预算时淘汰最旧记录并保留正在发送的消息。加载更早历史若超出预算，则保留已显示内容并提示本地浏览上限，不继续自动取旧页。历史与实时按序号去重；本人的实时回声合并待发记录，不能因临时成员 ID 复用漏掉其他作者。折叠不清空消息，上翻时实时新消息不抢位置，初次历史完成滚到末尾；没有跨频道未读或历史搜索。切换等待保留旧频道文字并禁用发送，失败仍留在旧频道则保留。

首次/重新进入频道最多加载最新 20 条，单页另限 48 KiB，长消息可能少于 20 条。
向上滚动或点击“加载更早消息”才取上一页；每页用最早消息序号作为排他游标，
过期、序号间隙和不足一页不会造成重复或无限加载。列表保留阅读行和行内偏移，
不因初始内容不足视口而自动拉完历史。加载中禁用重复请求，失败在文字区重试，
仍可发送、收听、切频道和退出。滚轮与拖动滚动条均支持；切频道/重新订阅后
通过会话、频道 epoch、订阅 generation、请求 ID 和游标拒绝迟到结果。

初始页与实时订阅使用共同的流序号边界，实时从边界后一条开始，交接不漏消息。
每成员只有一个临时实时 consumer，内部每批至多 4 条/48 KiB；翻页不重建它。
切频道或退出取消旧订阅，不周期轮询历史，也没有永久用户消费游标。

发送成功须获得服务端存储确认；明确存储拒绝可重试，结果未知保留提示、不自动重发。手动重试继续使用原消息 ID，服务端去重窗口最多两分钟，窗口外仍可能重复。聊天回复超时不关闭健康语音；实际控制流写失败按连接故障处理。首次连接/重连就绪前收到的历史增量必须有界积累，不能被后续状态快照覆盖。验收覆盖保留限制、重启恢复、历史到实时顺序、本人去重、切频道、失败重试、取消及退出，见 ADR-0031。

文字行使用时间、昵称与正文，不使用头像气泡。整行右键打开“复制”菜单，复制完整正文，保留换行和链接；不保留悬停复制按钮或槽位。菜单可取消，复制及取消均不改变草稿、发送状态或滚动位置，也不触发发送、重试或链接。历史、本人、失败及结果未知消息均支持。可打开 HTTP(S) 链接；发送失败与结果未知保留状态和重试入口。草稿按书签与频道隔离；Enter 发送、Shift+Enter 换行。输入区固定 64px，发送箭头固定 28×28px、位于框内右下角；正文留出按钮空间。连接/权限/设备错误仍使用独立通知，不依赖文字区展开。

消息在客户端按本地日期分组：当前已加载的首条消息及日期变化处，上方显示短横线，
中央为 YYYY-MM-DD。每条消息按 HH:mm:ss、昵称、冒号、正文排列，时间列固定 52px，
间隔 4px，禁止时间换行；昵称和冒号紧贴，长昵称省略时仍保留冒号。不再在行尾
显示日期。鼠标悬停消息行时保留适配主题的
淡色背景。分隔线在消息右键及悬停区域外；日期、正文及操作右侧统一预留 20px，
避开覆盖式滚动条。最小/默认/宽窗口及深浅主题下时间完整可读，长正文仍可换行。

日期和时分秒使用同一本地时区转换结果，历史、实时及待发送/失败消息沿用原始时间；
非法或空时间显示“时间未知”，相应日期组显示“日期未知”，不伪造时间。
日期分隔线归属其下方消息，不改变消息索引；翻页、淘汰首条或修正时间时，更新
受影响的行高缓存，并补偿可见消息内日期线的高度差，保持正文滚动锚点；如果原
锚点位于消失的日期线内则定位到消息顶部。跟随最新消息仍保持底部。复制只含原始
正文，重试、频道切换、断开及退出不变，不新增后台计时器或网络请求。

## 设置确认

设置侧栏标题在栏目内额外左缩进 12px，与下方导航图标的起点协调；导航位置与可用宽度不变。

GPUI Kit 迁移验收（2026-10-30）：使用默认后端及统一初始化/窗口 Root，保留
800×900 默认窗口、760×600 最小窗口与当前主题。离线打开设置不连接服务器或
打开设备；草稿、主题预览、应用等待/失败及取消恢复行为不变。菜单、emoji、
提示和右键层不能穿透触发服务器连接；中文候选确认不发送消息，Enter 发送、
Shift+Enter 换行，复制保留正文。分页日期线与滚动锚点不变。失焦或最小化继续
停止展示电平重绘与资源兜底同步，恢复后显示最新状态，不增加空闲任务。
原生关窗及 Cmd/Ctrl+Q 均等待现有有界清理，不重复启动退出；macOS 激活/失焦
不可死锁，Windows 显式深浅主题不被系统标题栏更新覆盖。实际验收范围记录于
verification.md，框架升级本身不作为性能改善证据。

本地偏好加载失败（损坏、格式不支持或无法读取）时使用恢复配置：自动开麦关闭、
AEC/ANS/AGC 全关，并显示持续的应用提示。正常首次安装默认值不改变。恢复模式下
设置和即时控制仍可作用于本次运行，取消仍恢复本次运行基线；应用后明确“不保存”。
所有保存入口及退出都保护原文件；无 Core 时诊断页仍提供“定位配置目录”，修复后重启。
书签文件异常继续阻止 Core 启动，不静默丢弃书签。未加载状态不能显示真实网络成功。

旧文件读取只做内存迁移，首次保存先创建同目录 `.pre-v1.bak` 原始备份。
备份或保存失败保留原文件，已应用的设置显示“本次有效，未保存”；只有落盘成功
才显示保存成功。保存期间发现文件异常同样保留原文件并提示保护状态。偏好格式
版本独立于应用版本，未来版本及 v1 未知字段不能被当前应用覆盖。详见 ADR-0029。

设备、声音、提示音共用设置草稿。设备页提供输入/输出设备和本地麦克风试听，声音页提供发言方式、快捷键、AEC、ANS、每成员接收 AGC 与闪避。页签切换保留草稿；“取消”丢弃，关闭/Escape 对未应用修改确认放弃或继续编辑。“应用”等待核心实际配置完成后才保存，失败保留草稿并允许重试；离线只准备偏好，不打开设备。纯提示音和快捷键偏好不重建设备。正在应用时防止重复提交。测试中禁止切换页签，停止后恢复；连接中和设备不可用时明确禁用试听，不能把本地试听当成网络收音。

增益只在通话工具栏提供全局即时控制，不重复放在设置草稿。麦克风输入增益暂不展示，但保留原值、Core 命令与迁移能力，避免隐藏控件时重置用户调整；接收 AGC 不替代发送侧输入增益，系统应用音量控制也不覆盖 macOS。侧栏图标使用等宽槽位、文字左起点统一，默认及最小窗口均保持选中、禁用与键盘焦点可辨。

“外观”提供跟随系统、深色、浅色三个选项，旧配置及首次安装默认跟随系统。选择后立即预览，应用才保存；取消、关闭确认放弃或 Escape 确认放弃时恢复已应用外观。仅改外观无需等待 Core，不触发语音设备配置；同时更改语音设置而应用失败时保留草稿与预览，供重试或取消。跟随系统在运行中响应系统外观变化，手动主题保持不变；重启恢复已应用主题。浅色模式的背景、文字、边框、输入、菜单、弹窗及滚动条须保持可读，界面品牌图标为白底黑线并以细边区分背景。

设置侧栏还提供“诊断”和“关于”。诊断页展示配置保护状态、定位配置目录，以及显式导出内存诊断并定位文件；应用内音频能力在启动时自动读取，不要求用户理解或探测 Core。能力未就绪时不能选择未知处理器；读取失败显示原因并提示重启，缺失的已选后端保留并标记不可用。导出期间防止重复提交，失败允许重试；关闭设置保留草稿，不改变通话、设备或连接。退出不自动导出，迟到结果不打开文件管理器。关于页显示应用版本、平台、提交与构建时间，不把未知字段伪装为正式版本。

应用品牌图标随有效主题统一为浅色白底黑线、深色黑底白线；应用内标志与 macOS Dock、Windows 窗口图标同步预览、应用、取消和系统主题变化。Finder、exe、快捷方式等静态位置固定白色版，不在运行时改写签名资源。Windows 固定任务栏项受系统缓存影响，以实机结果为准。两套运行时资源缓存复用，无定时刷新；验收覆盖启动、重复切换、取消恢复及重新打开。

Windows 原生标题栏随有效主题切换浅色/深色，包括已保存主题的启动、设置预览、应用、
取消恢复和跟随系统变化。手动选择浅色/深色时，系统主题变化不能覆盖标题栏偏好；
保留原生拖动、最小化、最大化及关闭按钮，不新增轮询或改变语音状态。

设置页的标题和关闭按钮固定在正文滚动区域外，滚动条与关闭按钮保持间距；侧边导航和应用/取消也固定。默认及 760×600 最小窗口下验证滚动、页面切换、关闭确认、主题预览/应用/失败/取消及系统外观变化；改变尺寸或诊断操作不清空草稿。

设置弹窗和下拉菜单占据输入焦点时，单击、双击、菜单选择、空白处点击及取消都不能选中、连接服务器或进入频道，也不能触发下层开麦、发送或音量操作；设置内选项仍可点击和滚动。后台已有连接事件仍按会话处理，关闭覆盖层仍能退出。默认与最小窗口都需验证菜单与底层服务器行重叠的坐标。

麦克风试听使用已应用配置，有未应用修改时先应用或取消后再试听；关闭设置停止试听。提示音预览使用草稿音量。通话工具栏与个人收听控制继续即时生效，草稿仅合并用户修改的字段，不能覆盖期间发生的即时音量和导航偏好。退出应用不保存未确认草稿。

## 3A 处理设置

设置草稿包含三个正交模块：`preprocess` 固定为麦克风 AEC、ANS，`postprocess` 固定为每位成员独立的收听 AGC。每项独立选择关闭、SpeexDSP 或已随 Core 打包的 WebRTC APM 后端；WebRTC 分别为 AEC3、NS、AGC2。Core 的能力响应按阶段/处理项提供可用后端 ID、名称与简短说明；GUI 不维护这些选项清单，等待或读取失败时禁用菜单。标签旁使用与文字协调的 12px 信息图标，悬停显示处理器取舍。原生库缺失时不展示 WebRTC 选项，已有选择明确标为当前不可用，不能偷偷回退到另一个算法。

发送链作用于麦克风 PCM、输入增益和编码前；接收 AGC 位于 Opus 单声道语音解码后、个人音量和混音前。音乐、通知音和本地麦克风试听不走接收链。新安装默认 SpeexDSP AEC、SpeexDSP ANS（中等）、WebRTC AGC2；已有配置及显式关闭项保持不变，旧版降噪、回声和接收 AGC 偏好在读取时转换一次。上一版已保存的发送 AGC、接收 ANS 被舍弃，其余设置和三个有效模块保留，明确关闭的处理项不因旧字段重新启用。验收覆盖首次安装、旧版关闭、可选库缺失、迟到能力响应、应用失败/取消/退出和多人每成员听感；Windows 游戏负载仍需实机验证。

AEC 的残余抑制和尾长仅属于 SpeexDSP；WebRTC AEC3 无这两项。Speex 尾长可在 40–500ms 以 20ms 调整。Speex 输入 ANS 可选低/中/高，WebRTC NS 还可选极高；Speex 接收 AGC 可调整目标和最大增益，WebRTC AGC2 可调整目标余量和最大增益。切换后端清除不兼容的旧参数，关闭时清空所有参数；未设置参数使用对应后端缺省值，Core 有界验证。仅改变收听 AGC 不得重建设备、解除静音或重置发送 AEC。接收处理器及增益历史按成员实例隔离，短暂停顿保留，退房、断线或关闭时回收；最多 64 项、空闲最多 30 秒。静音、无声或低于活动电平的 PCM、丢包补偿不参与学习；电平门不是人声识别。没有每用户线程或新增诊断定时器。

选择只更新草稿。应用返回“已受理”后保持等待；Core 异步准备候选处理器，编码 worker 在帧边界独占替换，混音 worker 切换接收配置，应用完成状态带操作序号和连接代次。GUI 仅在对应操作完成且配置回显一致后保存；立即查询与至多 20 秒的仅应用期间兜底查询可补丢失事件，重复或迟到事件不能把旧结果认作成功。超过期限显示“尚未确认”并保留草稿。离线应用只保存下次启动配置，不打开设备。非法后端、构造失败保留旧链和草稿；断线、退出或取消设置不保存未确认内容。接收新成员首次使用时仍需按需创建处理器，分配失败回显错误并保持基本播放；不承诺多人首次加入无分配。真人听感、Windows 多人及游戏负载仍需实机验收。

WebRTC 可选后端的验收覆盖：启用前无原生库加载和额外闲置循环；选择与参数确认后处理 48 kHz 单声道 20ms 帧，AEC3 使用本应用输出回调的参考；连续切换、失败、超时、取消和退出仍遵循上述草稿/旧链生命周期；每位成员 AGC2 历史互不共享，手动与全局音量保持叠加。缺失或版本不符的动态库不得伪装为已应用。macOS/Windows 安装包包含原生库与依赖许可，签名与跨平台多人听感仍需分别验证。

## 关闭（窗口与生命周期）

macOS 顶栏与应用暗色背景一体，保留原生关闭、最小化、全屏按钮，以及拖动和系统双击行为。设置、同步与退出遮罩只覆盖内容区，不能挡住标题栏；最小窗口仍应完整容纳设置面板。关闭按钮继续执行以下有界清理流程。

桌面默认使用 800×900 的竖向窗口，最小为 760×600；可用屏幕较小时以系统可用区域为准。主区与底栏始终铺满窗口，聊天输入跟随主区宽度，保持 64px 高、28px 发送按钮，不随窄窗缩成局部小块。设置面板宽度受窗口约束，内部页面和音量表自适应。验收覆盖默认、最小、恢复宽窗、侧栏收纳、文字区折叠/拖高与设置页切换；改变尺寸不能清空草稿或切换连接。

关闭入口阻止新操作、结束采集和连接，再按设置播放断开反馈。清理与声音等待有上限，核心失联不能阻止最终退出。Windows 注销和真实设备资源释放仍需平台验收。

本轮产品审查覆盖动作、等待、成功、失败、取消和退出。桌面测试通过不等于 Windows 实机或 GPU 性能验收。
## 设置中的更新入口（2026-10-15）

更新仅保留“设置 → 关于”入口，不在主界面或标题栏放置按钮、提示点或独立工具行。macOS 保留原有空的一体标题栏，其他平台保留原生系统标题栏。没有常驻动画、启动联网检查或周期轮询；启动只查询本地 Core 状态。检查结果、失败和取消持续保留至下一次操作，不新增 toast、横幅或自动弹窗。

进入关于页只读取本地状态；用户点击“检查更新”才启动一次检查。检查中禁止重复检查，仍可显式取消。切换栏目或关闭设置不取消检查，重新打开看到同一任务；显式取消和普通退出取消检查，不影响通话和用户配置。检查更新不应用或保存设置草稿。

一次检查总期限为 10 秒。仅主动取消或退出取消属于 cancelled，显示“已取消检查更新”；超时、网络、HTTP 和清单验证失败属于 failed，显示“检查更新失败：{安全原因}”，可手动重试。超时明确提示连接超时；HTTP 404 提示未发布清单，403/429/其他响应显示状态码，DNS、TLS 和连接失败分别说明原因，不直接显示底层请求地址或原始网络错误。退出时不另弹失败提示。

Core 读取包元数据中的 portable/installer，GUI 只消费能力和已验证的发布页 URL。ZIP/.app 均为 portable，有新版时关于页仅提供“打开发布页”，请求系统浏览器打开对应版本页面，不显示或执行下载、定位包和安装，不退出应用、不写更新缓存。浏览器实际是否成功打开不显示为安装成功；Core 返回地址失败留在关于页可重试。缺失/异常元数据、未知类型、开发版本、缺少更新配置均明确显示不可用原因。

验收覆盖深浅主题、760×600、错误状态与焦点、关闭再打开、取消重试及有界退出。便携版拒绝下载和安装命令。状态更新不得进入语音热路径，不能引入闲置计时器。交互参考 DBX 已评估提交及文件见 updates.md。

### Windows 安装与更新（2026-11-09）

同一 tag 提供独立 Velopack 安装器与便携 ZIP。安装版仍只有“设置 → 关于”入口：主动检查、下载（进度与取消）、已验证后“重启并更新”，再明确确认会结束当前通话。关闭设置保留任务和状态，“稍后”保留本次已下载更新；普通退出不安装。取消不得被下载完成的迟到回执覆盖为 ready，校验/网络/磁盘失败留在当前页面可重试。

确认重启重新校验包，将待安装文件的所有权移出 core，再停止发言/PTT、试听、收听与连接；实际 core close 完成才启动等待当前 PID 的 helper，随后退出，避免 helper 的六十秒等待期限被设备清理耗尽。窗口更新退出不用七秒强制退出兜底；系统应用级退出最多等待六十秒交接，超时或系统强制结束可能中止更新，不允许边替换边继续通话。启动 helper 失败恢复当前版本运行时并保持断开，在关于页显示失败；可重试或重新连接，不显示安装成功。替换失败保留旧安装的可恢复路径，由 Velopack 交互处理，不保证新版逻辑故障自动回滚。

本机验证：Rust client 33 项单元与 29 项 Go 互通测试、desktop 79 项单元与 1 项兼容测试通过；更新包路径/身份/签名及发布完整性测试通过，macOS 原生打包和签名检查通过。流程审查覆盖下载取消、退出交接及失败恢复。Windows Setup、卸载和跨两个正式版本的实际 OTA 尚待原生验收，不能由上述检查代替；Authenticode 本期暂缓，安装器明确标记 unsigned。

偏好、书签、身份、凭据位于安装目录之外，更新不重置它们。安装包使用独立 schema 2 签名清单；便携旧清单保持 schema 1。Authenticode 按用户决定延后，本期产物明确标注 unsigned，不能把 OTA 清单签名解释为 Windows 发布者签名。Windows 两版本安装/更新、退出、权限和配置保留需要实机验收；测试范围详见 updates.md。

# 2026-09-18 音频设置生效反馈

本地音质工具保留三个发送降噪对比频道，新增独立 AGC 频道：三个成员共用完全相同的合成人声片段，仅改变整段 RMS 为 -34/-26/-18 dBFS，轮流说话，不叠加，间隔 0.5 秒，避免音色、内容和语速干扰响度比较。试听时个人音量归 0 dB、全局音量固定、每次确认 AGC 后听两轮，麦克风保持静音。合成底噪样本的主观反馈（Speex 降噪较好、Speex AGC 抬噪明显）不直接等同于真人环境结论。

macOS Dock 图标单独保留约 10% 透明边距，浅深主题运行时与打包 ICNS 使用相同轮廓；应用内部与 Windows 图标不缩小。产品审查通过，Dock 实际显示尺寸仍需用户验收。

点击确认后等待 Core 操作回执，活动语音中的 3A 配置无需重启应用或重新连接。仅非音频改动显示“已保存”；已连接且活动的语音显示“已应用到当前语音”；已连接但语音未就绪时提示恢复麦克风或收听后自动应用；离线提示连接后自动应用，两者都明确无需重启应用。确认不会自动启用语音或解除麦克风静音。

应用中禁止重复确认、取消和关闭；麦克风试听期间保持音频配置不可应用。Core 错误、会话变化、超时或配置不匹配保留草稿并显示对应失败，不展示成功。产品流程审查通过，40 项桌面测试通过；新提示尚未进行实窗验收。

## Rust 核心迁移验收（2026-10-21）

用户操作继续通过有界异步指令和状态事件完成，UI 不等待网络、设备或系统密码授权。
旧身份、书签、偏好和凭据格式直接继承；密码只在本次认证成功后保存一次，失败、
取消或旧代次完成不得保存。排队连接被断开作废；PTT 松开作废排队的旧按下。
背压时合并最新展示字段，各字段公平送达，不能让音量表更新饿死语音状态。

连接、资源、聊天、设置分别保留原有等待、成功和失败语义；未知送达消息不自动重发，
退出和换频道清除旧范围。在线本地试听暂停网络发送，停止成功才恢复原配置；恢复失败
明确显示且可重试。断线/换频道立即停止旧范围采集，重连后麦克风静音。
普通关窗等待清理完成事件立即退出；系统授权/设备驱动停滞不得无期限阻止退出。
调试预览明确标记为本地预览，无真实连接或语音，正式包不提供入口。

Rust 单元和 Go 服务器互通回归覆盖以上核心路径；设备、Windows 长听、游戏焦点、
权限弹窗及总进程性能仍需实际平台验收，不能用回环结果替代。

## 音频状态与重绘（2026-10-22）

音频事件按已接受的类型化状态判断是否重绘，旧连接代次或旧操作不能替换当前状态。
普通通话的输入电平不在主界面显示，因此只更新模型；麦克风试听电平仅在前台设备页
触发重绘，沿用音频层约 100ms 的状态发布间隔。UI 不再额外丢弃电平事件，不增加
定时器，最终静音电平也立即显示。后台继续保存最新状态，重新激活窗口时重绘。

静音、发言者、忙碌、错误、操作回执等变化仍即时显示；设置确认、取消、PTT 松开及
退出继续执行，即使本次事件不需要重绘。每批最多处理 64 个事件，避免持续积压占住
输入处理。Windows 验收需分别覆盖主界面、设备试听、切换前后台和最小化，并在相同
release 工作负载下采集 GPU；单元测试通过不代表实际 GPU 降幅已验证。
## Server-local account acceptance (2026-10-22)

- Guest connections remain available without registration. The account settings
  page identifies the current server and separates device proof from account UID.
- User settings use a responsive, at most 400px wide form with consistent vertical
  spacing. The page opens at Login; a muted Register link sits to the left of the
  primary login button. Register replaces the same form and title, adding a
  nickname between username and password, with Register and log in as the primary
  action and Back to login as its secondary action. Login uses account passwords only.
  Registration includes an editable nickname, separate from the immutable ASCII
  username. Logged-in profile and security actions stay compact; account actions
  have their own confirmation buttons, without the unrelated preference footer.
  Closing with other pages' pending preference edits still exposes the discard
  confirmation, so User settings cannot trap the close/Escape flow.
- Registration validates username, nickname and password before sending, retaining
  inputs on local validation failure. Passwords require 8–256 UTF-8 bytes; this is
  an explicit input error, not an invalid-login error. Successful registration
  enters the logged-in profile without logging out or authenticating twice.
- Audio settings retain vertical spacing inside the scroll content. Activation
  choices wrap at narrow widths; automatic microphone activation is separated
  from these choices. Processing groups use consistent gaps and dividers; all
  controls still edit the draft until Apply, and Cancel restores the baseline.
- Chinese nicknames and UTF-8 chat, including skin-tone, ZWJ and flag emoji,
  preserve their exact text in live delivery, recent history and copying. Native
  input methods and system character panels remain supported. A fixed emoji
  button sits left of Send and opens a local, theme-aware selection panel.
  Selecting an emoji replaces the selection or inserts at the cursor, focuses
  the composer and never sends automatically. Escape and outside clicks dismiss
  the panel. Channel/session changes, chat collapse and settings close it;
  disabled chat and active IME composition cannot insert emoji. Existing
  nickname and UTF-8 byte bounds remain enforced.
- Enter submits the visible account action; Enter during IME composition only
  confirms composition. Registration's nickname Enter submits registration,
  whereas logged-in nickname Enter saves the profile. Inputs and mode switching
  are disabled while submitting; closing the page or application remains usable.
- Password/token login and registration use masked secrets, bounded waiting,
  duplicate-submit protection and explicit server-confirmed success. Account
  actions do not apply unrelated audio/appearance drafts. No passwords or tokens
  appear in workspace snapshots, diagnostics or ordinary config.
- Invalid credentials preserve the current usable connection and show a safe
  error. Leaving settings does not pretend a committed login was rolled back;
  authoritative state continues to reconcile. Disconnect/exit cancels pending
  work and stale completions cannot alter another connection.
- Logout revokes the current account token, clears local account state and
  disconnects. Reconnecting explicitly starts as guest. Password changes revoke
  old tokens; successful current-session rotation is server-confirmed.
- Ordinary disconnect and application exit retain account login for the same
  bookmark, destination and pinned server key. On the next connection, restore
  the account using its rotating 30-day token from macOS Keychain or Windows
  Credential Manager. Registration, password/token login, password changes and
  network recovery save confirmed rotations; passwords are never remembered as
  account credentials. A successful authentication renews the existing 30-day
  validity. There is no additional refresh timer or second token protocol.
- A denied credential read fails explicitly before admission; it never silently
  substitutes a guest identity or repeatedly opens authorization on network
  retries. A failed write preserves the server-confirmed login and warns that
  persistence failed. An expired/revoked saved token stops recovery, clears that
  saved token and requires an explicit reconnect before guest/manual login.
- Credential operations run in one bounded, ordered worker, outside the voice
  and control loops. Unanswered OS authorization must not block server switching,
  unrelated commands or application exit. Shutdown allows a bounded 250ms flush;
  an unfinished/denied OS operation cannot guarantee persistence. Bookmark
  deletion/destination changes follow session retirement and clear saved account
  tokens; forgetting only the server password preserves the account login.
- Transient network recovery retains account identity and restores the channel
  with the microphone muted. Revoked credentials and replacement are terminal,
  visible failures, never silent guest fallback or replacement/reconnect loops.
- A valid new account admission atomically removes an older same-UID connection
  from voice/control routing. Invalid or banned login never displaces it. The
  older device explicitly reports that the account connected elsewhere.
- Previous device-bound owner/admin roles are intentionally discarded. Devices
  enter as guests and register/login explicitly in Settings -> User. Nicknames
  of logged-in accounts synchronize with the server; guest edits stay local.
  Backups cover SQLite and KV together.
- Web administration accepts account passwords; reusable web tokens and
  administrator invitations are retired. Only Owner directly assigns admin.
  Successful account authentication does not grant management permission by
  itself; Casbin remains authoritative. Web sessions do not displace voice.

## Local macOS installation acceptance (2026-11-10)

- User quits Resona normally, then opens the local development PKG in macOS
  Installer. Packaging never quits or installs over the running client itself.
- Installation offers Applications or the current user's Applications directory.
  Standard Installer owns progress, cancellation and permission errors; cancelling
  before installation preserves the existing app. Failure must not claim success.
- The package retains `dev.resona.client` and the same application signing
  identity. The installer requests launch after installation; a successful launch
  does not imply a successful server connection. Existing configuration, bookmarks
  and credentials stay in their existing user storage.
- User acceptance covers first/repeat install, choosing destination, cancellation,
  launch, microphone access and retained identity/preferences. App removal alone
  retains user data. Package extraction/signature checks do not replace these
  native tests. Local development PKGs are unsigned and not notarized; macOS OTA
  and public installer trust remain deferred.

Build evidence: the local arm64 PKG was generated and expanded; bundle, helper,
native-library signatures and unchanged designated requirement passed. Desktop
tests passed (79 unit + 1 integration), as did the four client update tests.
The maintainer subsequently accepted the local macOS installation and Windows
installer experience. Intel installation and Windows OTA between two formal
versions remain separate acceptance items. CI adds both macOS architectures on
trusted main/tag builds; PKGs remain unsigned and not notarized.

## Light appearance refinement (2026-11-10)

- Following maintainer feedback on 2026-11-11, Light averages the original and
  gray palettes: canvas `#f0f2f4`, panels/sidebar/toolbar `#f8f9f9`, raised surfaces
  `#f3f5f7`, and dialogs/menus `#fbfbfc`. Component and custom palettes agree;
  small inverse button text stays white. Dark is unchanged.
- Appearance preview, Apply, Cancel and discard keep their existing semantics.
  Apply persists the selected preference without restarting audio; failure keeps
  the draft editable. Cancel/confirmed discard restores applied colors, titlebar
  and icon. Closing the app does not silently persist a preview.
- System mode follows OS appearance, including after restart. Explicit Light
  remains light when the OS changes. No new rendering loop or decorative effect.
- Product review checked the semantic palette and component backgrounds. The
  existing contrast test passed at least 4.5:1 for text/status labels on canvas,
  panels and raised surfaces; all 79 desktop unit tests and one integration test
  passed. The release app's offline main view and device settings were inspected
  on macOS; opening/cancelling settings preserved the current appearance.
- Native Windows acceptance should include settings, menus, notices, focus
  outlines and selected/disabled controls at 800x900 and 760x600. macOS inspection
  does not establish Windows acceptance.
