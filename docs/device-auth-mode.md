# 通过服务端配置切换设备注册鉴权

默认 `token` 模式要求有效 Token，以 Token 确定设备和租户，不要求客户端 `-I` 与登记编号一致。`-I` 仍需符合客户端协议的非空与长度限制。`compat` 模式对配置的一个接收租户按设备编号接入，忽略设备携带的 Token，因此空、无效、旧 Token 都可使用。**兼容模式启用期间，重置 Token 不能阻止该租户的设备凭编号接入**；停用或软删除设备仍会拒绝连接。

平台账号登录、租户权限和文件/终端权限不变。客户端传入的 group 不决定租户。其他租户不接收兼容设备：此模式下该实例的设备入口统一按配置的接收租户处理，包括携带其他租户有效 Token 的设备；需要同时服务不同策略时应运行独立实例或保持 token 模式。

```yaml
device-auth-mode: compat
device-compat-tenant: 接收租户的内部ID
device-compat-auto-enroll: false
device-compat-plaintext: false
```

`device-compat-auto-enroll: false` 仅接收平台注册过的设备编号。显式设为 `true` 后，未知旧设备编号自动登记到接收租户，不授权 Web 代理目标、不继承客户端 Token。自动登记包含审计，租户记录总量达到1000时不再自动登记。软删除留下的编号不会复活。

如果旧脚本完全没有 TLS 参数，可另外设 `device-compat-plaintext: true`，设备监听端口改用明文 TCP；网页监听端口继续 HTTPS。此配置不会在同一设备端口同时混用 TLS/明文。token 模式下启用该项会拒绝启动。原版协议至少需为服务端支持的 v3。

## 使用方式

保留现有数据库、证书、监听端口参数，在服务的 ExecStart 上追加 `--conf /etc/rttys/device-auth.conf`。复制 `deploy/device-auth-token.conf` 或 `deploy/device-auth-compat.conf` 为该文件，兼容模式先填写一个现有且启用的租户内部ID，然后 daemon-reload 并重启服务。

配置变更需要重启生效，旧设备连接会断开再重连。恢复鉴权时换回 token 模板并重启。已有设备原 Token 保留；兼容模式自动登记的设备需要管理员通过重置 Token 签发可用凭据后配置客户端。

TLS 默认保留，免 Token 不会自动关闭证书校验。兼容模式下知道设备编号的人可能冒充设备，公开入口应在使用期间限制可达范围。这是配置可选能力，本次未开启公网兼容模式。

## 验证

```sh
go build -o /tmp/rttys-auth-mode ./cmd/rttys
RTTY_BINARY=/tmp/rttys-auth-mode python3 scripts/device-auth-mode-e2e.py
```

真实本地 HTTPS/TLS/TCP 链路、隔离数据库：验证安全默认、空/错误Token接入、接收租户绑定、显式自动登记、平台登录仍鉴权、停用/软删除仍拒绝、旧明文客户端、恢复安全模式后旧规则生效。脚本输出证据目录，失败与成功均保留检查结果。未以此宣称公网或173实机验收。
