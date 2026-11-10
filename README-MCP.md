# 终端 MCP 与 OAuth

该功能让 Codex 使用现有 RTTY 账号访问设备。服务默认关闭；启用后，每位用户的 MCP 权限也默认关闭。网页登录 Cookie、设备注册 Token 都不能作为 MCP 凭据。

## 管理员怎样配置用户权限

1. 启用下述服务配置，使用可被 Codex 和浏览器正常验证的 HTTPS 地址。
2. 登录控制台并选择租户，在“成员与权限”中选择成员，点击“设置 MCP 权限”。
3. 勾选需要开放的权限并保存。运维成员还需要现有的设备或分组授权。
4. 用户从 Codex 发起登录，在浏览器中选择租户、勾选权限并同意授权。
5. 用户可在工作台左侧的“MCP 授权”页面查看、撤销自己的授权。

平台管理员自身的权限在“租户与账号 → 平台账号 → MCP 权限”设置，同样先选择租户。

| 权限 | 能力 | 额外限制 |
| --- | --- | --- |
| devices:read | 查看有权访问的设备列表 | 按当前租户及设备权限过滤 |
| diagnostics:read | 固定模板的状态和日志查询 | 需要设备操作权限、配置的非 root 系统账号 |
| terminal:interactive | 打开、读写、关闭交互终端 | 需要设备操作权限，能够修改设备 |

查看者只能获得设备列表权限。诊断和交互终端均不会绕过成员角色或设备授权。最终权限取管理员的 MCP 设置、用户此次同意的权限及当前设备授权的交集。

修改 MCP 权限会撤销该用户在当前租户的全部旧 MCP 授权，关闭对应终端，要求重新登录授权。撤销设备授权、停用设备/账号/租户、移除成员或修改密码，继续沿用现有的会话撤销机制。

## 启用服务

在现有的租户模式、TLS 配置上增加：

    --mcp-url https://rtty.example.com/mcp
    --mcp-client-id ja-rtty-codex
    --mcp-redirect-uri http://127.0.0.1/callback
    --mcp-diagnostic-user rtty-diagnostic

同名配置项也可写入原有配置文件。只有 mcp-url 必须设置才能启用；client-id 和 redirect-uri 的默认值如上。诊断账号默认留空，此时诊断执行关闭。

地址必须为规范的 HTTPS URL，路径固定为 /mcp，不能包含密码、查询参数或片段。它同时决定资源标识、授权服务的公开地址与允许的 Host/Origin。通过反向代理访问时需保留这个公开 Host；控制台、授权接口及 MCP 使用同一公开 HTTPS 源地址。

证书应由系统信任的 CA 签发，或事先正确安装组织内部 CA。不要通过关闭证书校验接入。部署前备份身份库；本版本将身份库迁移到 schema 4，现有 CI 发布元数据已同步更新。

## Codex 连接

使用支持预注册 OAuth 客户端的 Codex：

    codex mcp add ja-rtty \
      --url https://rtty.example.com/mcp \
      --oauth-client-id ja-rtty-codex \
      --oauth-resource https://rtty.example.com/mcp

    codex mcp login ja-rtty \
      --scopes devices:read,diagnostics:read,terminal:interactive

浏览器跳转到 RTTY 的授权页。未登录时先登录原有账号，再返回此次授权请求。选择租户后，只能同意管理员已开放的权限；交互终端默认不勾选。完成后返回本机 Codex。

这里使用预注册的公共客户端，不分配客户端密码，不开放任意动态注册。默认回调允许 Codex 使用 127.0.0.1 的临时端口，其他回调地址必须由服务端明确配置。授权响应包含 issuer，客户端需保留 state 和 PKCE 校验。

Codex 的官方 MCP 配置说明：[连接 MCP 服务](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)。

## 工具与执行边界

| 工具 | 主要参数 | 行为 |
| --- | --- | --- |
| list_devices | after、limit、query、online_only | 分页查询当前账号可见设备；返回内部 device_id，不返回凭据 |
| run_diagnostic | device_id、check、unit | check 仅允许 identity、uptime、disk、memory、processes、service、logs |
| open_terminal | device_id、cols、rows | 打开交互终端，返回 session_id |
| read_terminal | session_id、cursor、max_bytes、wait_ms | 使用上次返回的 next_cursor 继续读取 |
| write_terminal | session_id、input | 发送终端输入 |
| close_terminal | session_id | 关闭该授权创建的终端 |

诊断复用设备的结构化命令协议，按固定的程序与参数执行，不启动 Shell，不接受调用者提供的命令、账号或文件路径。service 和 logs 必须提供合法的 .service 单元名称；日志最多请求最近 100 条，标准输出与错误输出分别限制为 16 KiB。进程查询不返回完整命令行参数。

在设备上单独创建并限制诊断账号的系统权限，按需授予指定日志的读取能力；不需要给它 sudo 权限。服务在执行诊断前调用固定的 id -u，拒绝 UID 0，包括其他名称的 UID-0 账号。目标未安装某个诊断程序时会返回设备的执行错误。

交互终端使用设备客户端已配置的系统登录方式：客户端配置自动登录账号时使用该账号，否则出现系统登录提示。MCP 不提供传入登录密码的参数；不要通过工具参数输入密码或其他秘密。按最小权限配置设备的自动登录账号。

服务端无需新增客户端协议：结构化诊断协议已核对 rtty-go v1.2.2 源码，交互终端沿用原有 PTY 协议。其他版本和 C 客户端需在目标设备上验收其命令、账号切换行为，不能仅凭服务端测试断言全部兼容。

设备现有命令协议没有取消消息。断开、撤权或请求取消会停止等待并禁止返回结果；已经交给设备的诊断可能继续执行至设备自身的超时（已核对的 rtty-go 为 30 秒），并不能保证远端立即杀死进程。

## 安全、生命周期与验证

- 授权码配合 S256 PKCE，90 秒有效且仅使用一次。令牌只接受 Authorization Bearer 请求头，不接受 Cookie 或 URL 参数。
- access token 有效期 10 分钟；refresh token 会轮换，整份授权最长 24 小时。重用已轮换的 refresh token 会撤销整份授权。凭据在身份库中仅保存摘要。
- 令牌绑定配置的 MCP 资源、客户端、用户与租户。每个请求及工具执行阶段都会检查当前授权和权限。
- 终端按授权隔离，同一用户的另一份 OAuth 授权也不能接管旧 session_id。每份授权最多 4 个终端，全服务最多 128 个；5 分钟无读写则关闭，最长不超过打开时的 access token 有效期。
- 每个终端最多保留 128 KiB 输出，溢出时返回 truncated。单次读取最多 32 KiB，等待最多 10 秒，输入最多 16 KiB。固定诊断并发上限为全服务 8 个。
- 审计记录用户、租户、设备、工具动作和执行状态/输入字节数，不记录终端输入、输出或 OAuth 凭据。这不是完整的终端命令录像。诊断/终端结果本身可能含敏感内容，仍应按设备权限管理，并视为不可信数据。
- 当前实现沿用身份库的单实例模式；不支持多个实例同时写同一个身份库。

新增自动测试覆盖 OAuth、PKCE、一次性授权码、并发兑换、令牌轮换/重用撤销、摘要存储、资源绑定、过期与重启、租户/角色/设备隔离、终端会话归属与撤权、非 root 诊断及事务失败回滚。工具调用使用官方 MCP Go SDK 客户端；设备侧使用真实协议的模拟对端。仍需在实际 HTTPS 地址、实际 Codex 和目标设备上完成部署验收。
