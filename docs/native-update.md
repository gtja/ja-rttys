# 原生程序交付与动态更新

## 构建和下载

Jenkins `ja-rttys/dev`、`ja-rttys/test` 在 105 专用执行器运行安全检查，
输出 Linux amd64、arm64 的静态 `rttys`、SHA256、manifest 和 tar.gz 安装包。
前端已嵌入可执行文件，服务器无需 Go、Node 或 Docker。
构建阶段复用私有仓库的 Go/Node 工具链；默认不构建、推送应用运行镜像。
如另有镜像交付需求，可显式设置 `PUBLISH_IMAGE=true`。

test 正式构建保留 `v大版本.小版本.修订号.YYMMDD` 版本标签，
同日后续构建递增修订号，失败构建的号码也不复用。
制品见 Jenkins 构建页面的 `out/release/{amd64,arm64}/`。
下载后先验证 `sha256sum -c <安装包>.sha256`，保留 manifest 的源提交号。

## 公网 rtty 的 CI/CD

固定目标为 `47.111.155.82` 的 `ja-rttys.service`，程序路径
`/opt/ja-rttys/rttys`。Jenkins 使用专用 `ssh-rtty` 凭证绑定，
密码只通过 Jenkins 环境传给 sshpass，不写入源码、命令参数或日志。
SSH 主机公钥通过已认证的 JumpServer 资产读取并固定在 `ci/rtty-known-hosts`。
密钥轮换时需重新核对指纹后更新文件，禁止关闭主机校验。

人工运行 dev/test 构建并选择 `RTTY_ACTION=check`：仅通过 SSH 读取服务、
程序 SHA、版本、数据库头和部署配置是否存在。不会打开 SQLite 连接、停止服务或写远程文件。
PR、非 dev/test 分支和自动触发构建均不能使用该凭证入口。
check 模式跳过工具链构建、安全扫描和出包，也不预留正式版本号。

部署首次配置由服务器管理员执行：

```sh
install -d -m 700 /etc/ja-rttys
install -o root -g root -m 600 ci/deploy-rtty.example.json /etc/ja-rttys/ci-deploy.json
```

必须核对 JSON 中服务名、原程序、数据库和 CA 文件，尤其是
`/opt/ja-rttys/ca.pem` 能否验证当前 HTTPS 证书；这里不重写 systemd 启动参数。
现有 device-auth.conf、Mender 配置、密钥及数据库沿用。
为避免误操作，`RTTY_ACTION` 默认 `skip`；仅人工 test 构建可选 `deploy`。
check 和 deploy 都在 105 发起连接。

部署流程：

1. 通过固定 SSH 主机公钥验证服务器；确认运行进程和数据库路径。
2. 在停服务前检查部署配置与数据库 schema，拒绝不匹配的包。
3. 校验包 SHA256、源提交、ELF 架构和内嵌程序 SHA256。
4. 获取独占部署锁，备份旧程序，停止服务，再核对 schema 并备份数据库。
5. 在同一文件系统写入新程序并原子替换，保留原所有者和权限，启动原服务。
6. 验证实际运行进程的 SHA256、HTTPS 登录页和匿名 `/api/me` 返回 401。
7. 失败时恢复旧程序并再次验收；保留新的业务写入，不用旧数据库覆盖新数据。

每次发布在 `backup_root/release-<commit>-*/` 保留旧程序、数据库快照、manifest 和结果回执，
Jenkins 同时归档 `out/rtty-preflight.json`、`out/rtty-deployment.json`。
不同 Jenkins 分支也由服务器端锁串行化。
同版本降级也必须经过 schema 校验；不要直接覆盖正在运行的文件。

这是自动化滚动到新版本的发布流程。当前单实例会短暂断开 WebSocket、设备和终端会话，
客户端重连后恢复；正在执行的交互命令不会被透明迁移。
设备连接和进程内会话不支持双实例同时共享 SQLite，因此不能直接宣称零停机热更新。

## 首次 schema 3 → 4 更新

2026-11-11 实查公网服务器数据库为 schema 3，新程序为 schema 4。
新程序包含旧设备兼容和 Mender 功能，同时会在首次启动时增加 OAuth/MCP 表。
自动部署故意拒绝跨 schema，必须先在维护窗口完成一次迁移。
普通部署不能通过修改 manifest 或强改 PRAGMA 绕过检查。

迁移准备：在隔离环境用合法备份验证账户、租户、设备标识、权限、旧客户端接入、OTA，
同时验证 schema 4 的 OAuth/MCP；库中新增表不等于线上业务已经验收。
本仓库 CI 用合成数据和真实 HTTPS/TLS/WebSocket 传输验证，不读取生产账号或密钥。

维护窗口建议流程：

1. 暂停新的控制台操作及设备接入，记录服务完整启动配置和当前程序 SHA。
2. 停止 `ja-rttys.service`，确认 MainPID=0 后对数据库作一致性备份，
   另存原程序、权限、证书和私有配置；不在锁定的活动库上直接复制单个 DB 文件。
3. 在隔离端口用备份库启动新程序，完成迁移与数据检查；不要让隔离实例连接真实 Mender。
4. 替换正式程序，沿用原启动参数，验证登录、设备接入及 Mender，再开放流量。
5. 若需恢复 schema 3，保持写入隔离，停止新进程后恢复同一检查点的原程序和库，
   且必须包含正确的 SQLite journal 状态。开放写入后不能无条件恢复旧库。
6. schema 4 验收后，后续同 schema 版本可直接使用 Jenkins `RTTY_ACTION=deploy`。

此流程不会自动为任何用户开启 MCP。OAuth/MCP 入口及用户 scope 仍需显式配置授权。
