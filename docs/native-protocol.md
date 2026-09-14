# Resona 应用协议

唯一传输为 [Noise UDP](noise-protocol.md)，版本 `resona-noise-exp-5` / `RN05`。开发版客户端与服务端需匹配升级，不进行旧协议回退。

## 控制消息

可靠流上每帧为 `length:u32 big-endian | Protobuf Frame`。Frame 包含 kind、request 和 body；长度上限 65536 字节，body 按 kind 解码。命令 request 非零；事件 request 为零。普通命令由 Reply 表示服务端处理结果，不能把传输 ACK 当作业务成功。

Schema 在 `internal/nativewire/pb/control.proto`，用 `make generate` 生成 Go 文件，正常构建不需要 protoc。保留字段编号，删除字段时 reserve，不复用编号。多字节语音头用大端序，Protobuf 使用其标准编码。

| kind | 用途 |
| --- | --- |
| 1 / 2 | Hello / Welcome |
| 3 | 带 revision 的资源状态 |
| 4 / 5 / 6 | 移动频道 / 频道聊天 / 本人静音状态 |
| 7 / 8 | 命令结果 / 收到聊天 |
| 9 | 所有者认领 |
| 10 / 11 / 12 | 创建 / 编辑 / 删除频道 |
| 13 | 原子替换 List+Watch 关注范围 |

Hello 的 Ed25519 签名绑定 Noise 握手和登录参数，验证成功后才加入成员。服务器角色目前为 owner/member；owner 可管理频道。频道数量与并发成员上限均为 64，服务端限制命令与发言速率。

Watch 选择全部频道、全部成员，全部成员要求全部频道。当前频道成员、音质、本人身份和权限始终存在。初始快照在 Reply 前排入同一可靠流，随后递增 revision，避免 list/watch 间隙。当前是范围集合快照，不是逐对象 delta。Watch 等待回复超时不直接关闭健康语音连接；被取消的部分控制写仍关闭连接。

## 语音包

每个数据报一帧 Opus，20ms、48kHz、单声道。服务端不接受客户端声称的发送者身份或任意目标频道。

| 方向 | 字段 |
| --- | --- |
| 上行 7 字节 | end:u8、发送者 epoch:u32、sequence:u16 |
| 下行 13 字节 | end:u8、接收者 epoch:u32、sequence:u16、sender:u16、发送者 epoch:u32 |

随后为最多 1024 字节的 Opus。end 只能为 0/1；结束包正文为空，普通包必须非空。epoch 非零，用于拒绝切频道前的迟到包。加密层另有 5 字节认证头和 16 字节 tag，因此 Opus 之外为上行 28、下行 34 字节，不含 UDP/IP。

语音不可靠重传；发送环满时覆盖旧语音。可靠控制不会采用这一丢弃策略。原生成员提示由客户端比较每次已验证的当前频道成员快照生成，不新增语音字段。

## 当前限制

没有自动重连、端点迁移、拥塞估计或自适应码率。服务端使用逐跳加密，具有解密内容的能力。独立安全审查、网络损伤测试与长期设备测试仍待完成。
