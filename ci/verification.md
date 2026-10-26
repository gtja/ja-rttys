# 初版 CI/CD 验证记录

## 2026-10-24 本地验证

日期：2026-10-24。安全修复基线为 `c9b16430c33964a6bd9c61718cc3cbf8ce3be815`，
本记录验证该基线加本次 CI/CD 工作区修改。所有服务验证均为本机隔离测试。

| 检查 | 实际结果 |
| --- | --- |
| Shell/Python/任务 XML | Bash 语法、Python 编译、Jenkins 多分支 XML 解析通过 |
| Jenkinsfile | Groovy 2.4.21 语法解析通过；Jenkins 服务端 Declarative 模型校验未执行 |
| 交付回归 | 8 项通过，覆盖包校验、ELF 架构、拒绝 tar 符号链接、成功备份升级、失败恢复二进制且保留新写入、schema 变化阻止不安全回退 |
| Docker verify target | 实际构建通过；Go 1.27.3 / Node 22 / Debian bookworm，Linux arm64 |
| Go 回归 | 279 个测试通过（含子测试），3 个测试包通过；排除手工 `TestRttysStress` |
| 静态与依赖检查 | `go mod verify`、`go vet` 通过；`govulncheck` 为 0 可达/导入包漏洞，1 个未使用模块级告警；npm 审计全部为 0 |
| 前端 | `npm ci` 和 Vite 生产构建通过 |
| 本地 E2E | 真实 HTTPS/TLS/WebSocket 通过；HTTP/socket 退出码均为 0，第二写入实例被拒绝，重启持久化通过 |
| 可归档摘要 | `RTTY_RUN_SUMMARY` 实际写出可解析的独立 JSON；未将密码、私钥或测试数据库复制到 `out/verify` |
| 部署健康检查 | 使用实际本地 rttys 服务验证 TLS CA、登录页和未登录 `/api/me` 的 401，通过 |
| Docker runtime target | amd64、arm64 两种镜像实际构建通过；image architecture、revision、二进制 ELF 和包 SHA256 均通过 |
| 镜像运行 | 两种镜像均以 UID/GID 65532、无网络、只读根文件系统、无 capabilities 运行 `--version` 成功；amd64 使用本机 Docker 的仿真支持 |

本地证据在被 Git 忽略的 `out/verify/`、`out/release/`、`out/local-check/`。
测试镜像为 `registry.jingan.com:32008/ja/ja-rttys:local-ci-c9b1643` 及 `-arm`，
仅在本机 Docker 中，两个 `image.json` 均为 `pushed=false`；不是已发布版本。

114 的历史 Jenkins 入口 `http://172.21.30.114:3214` 本机直连超时，经代理返回 502。
因此本次没有创建/修改 Jenkins 任务，没有验证 105 实际执行节点、registry 登录推送或 systemd 远端升级。
待连接恢复后，按 [接入说明](README.md) 导入任务并先保持两个发布/部署参数关闭，
核实 Jenkins 服务端模型校验和首轮验证/出包，再启用所需交付阶段。

没有部署到 204、105 或其他业务环境；实机业务验收、浏览器验证和线上安全复扫未执行。

## 2026-10-26 Jenkins 接入和首轮实跑

114 Jenkins 已恢复直连，实际版本为 2.533。使用现有凭据导入本仓库
`ci/jenkins-multibranch.xml`，创建独立的 [ja-rttys 多分支任务](http://172.21.30.114:3214/job/ja-rttys/)。
没有修改 Jenkins 全局配置、插件、节点或其他任务。

[codex/security-scan-20261024 #1](http://172.21.30.114:3214/job/ja-rttys/job/codex%252Fsecurity-scan-20261024/1/)
由 Branch indexing 自动触发，最终为 **SUCCESS**，耗时 239 秒。
源码提交为 `2b96075f0a86e4e5ab3ec0a3260bb7da1f39bfee`；
先在 114 检出，再将带 SHA256 的源码快照传到既有 `ja-pipeline-builder-105` 节点执行。
此结果验证的是修复分支，不代表已经合并到 master/dev。

| 检查 | 实际结果 |
| --- | --- |
| Jenkins 服务端模型校验 | `/pipeline-model-converter/validate` 返回 `Jenkinsfile successfully validated.` |
| 105 执行环境 | Linux amd64，Docker Engine 24.0.9、Python 3.9.25；构建容器内使用 Go 1.27.3 / Node 22 |
| 交付/回退回归 | 8 项通过 |
| Go 回归 | 279 个测试通过（含子测试），3 个测试包通过；排除手工 `TestRttysStress` |
| 静态与依赖检查 | `go mod verify`、`go vet` 通过；`govulncheck` 为 0 可达/导入包漏洞，仍有 1 个未使用模块级告警；npm 审计全部为 0 |
| 前端与 E2E | 前端生产构建通过；真实 HTTPS/TLS/WebSocket 通过，HTTP/socket 退出码 0，第二写入实例被拒绝，重启持久化通过 |
| 双架构构建 | amd64、arm64 runtime 镜像及二进制安装包均成功，源码 revision 和 ELF 架构校验通过 |
| 归档复核 | 实际下载 17 项归档，独立复核源码快照和两个包的 SHA256、包内二进制 SHA256、manifest、源码提交及架构，均一致 |
| 镜像发布/远端部署 | 两个 `image.json` 均为 `pushed=false`；204 部署阶段按条件跳过 |

首轮版本为 `5.6.0-tenant.2.261026022839-codex-security-scan-2026-2b96075f0a86-1`。
安装包 SHA256：

| 架构 | SHA256 |
| --- | --- |
| amd64 | `179f35e9b7ec3f5f2555098dbad5df82b9535120d755a2786182b3b100f060f0` |
| arm64 | `5b0b5870fc7da548c0d62e810027adb8ff08c89f7605462b098cb0b6b9364596` |

日志和安装包保存在该构建的 [Jenkins 归档](http://172.21.30.114:3214/job/ja-rttys/job/codex%252Fsecurity-scan-20261024/1/artifact/)。
本地复核副本在被 Git 忽略的 `out/jenkins/1/`，包含 `artifact-verification.json`。
发布和部署默认仍然关闭；registry 凭据可用性、镜像推送、204 配置/SSH 凭据及远端升级回退未实测。
未部署到任何业务环境，设备业务、浏览器验收和线上安全复扫仍未执行。

## 2026-10-26 dev 环境目标修正

用户明确 dev 开发环境应为 **172.21.30.105**。初版将扫描目标及已有租户功能测试实例
204 沿用为 dev 部署目标，这一映射错误已纠正；前节中的 204 跳过记录仅描述旧版首轮验证。

- `DEPLOY_DEV` 替代 `DEPLOY_TEST`，默认关闭；仅人工触发的 dev 构建允许升级 105。
- 部署入口固定使用现有 `ssh-105`，通过构建节点 OpenSSH/sshpass 和已有 known_hosts 严格校验主机。
  新配置路径为 105 的 `/etc/ja-rttys-dev/ci-deploy.json`；缺少服务或配置时拒绝升级。
- 通过临时只读 Jenkins 检查，实际 SSH 到 105 `server-1`，架构 x86_64、UID 0。
  `systemctl` 未发现名称含 rtty 的 unit，`docker ps -a` 未发现名称/镜像含 rtty 的容器；
  检查的独立部署目录和配置文件不存在，18446/18447/19512/5912/5913 端口未监听。
- 因此 105 当前没有已确认运行的 rttys 开发实例；示例布局尚待初始化。
  此次只修正流水线环境映射，没有迁移 204 数据、初始化 105 服务或进行任何远端升级。

只读检查证据保存在本地忽略目录 `out/jenkins/dev105-preflight/`。

## 2026-10-26 合入 dev 前验证

dev 基于 master `56caad1a98eaf6c594236af68b0bf8d3a62e75e8` 建立，合入安全修复分支
`641be874a1c480e0954bda6e14932f75acb8cfbf`。保留 master 新增的设备生命周期、
客户端安装和文件下载窗口控制；文件名过滤接入新的下载路径，前端消息校验同时允许 `fileError`。

合并工作区的 Linux arm64 Docker verify target 已实际通过：279 个 Go 测试、
`go vet`、前端生产构建、npm 审计（全部 0）、`govulncheck`（0 可达/导入包漏洞，
1 个未使用模块级告警）及真实 HTTPS/TLS/WebSocket E2E。
HTTP/socket 退出码为 0，第二写入实例拒绝、重启持久化和设备注册元数据持久化均通过。
8 项交付/回退回归、Bash 语法及 Git 空白检查通过。

本地证据在 `out/verify/`。本节记录提交前的合并工作区检查；推送后精确提交的
Jenkins 结果和双架构安装包以 dev 子任务归档为准。发布和 105 部署均保持默认关闭。

## 2026-10-26 105 初始化及首轮发布部署

按用户的“推送部署”指令，在 105 创建独立的 `ja-rttys-dev` systemd 实例，
使用专用非 root 账号、`/opt/ja-rttys-dev/data/identity.db` 和独立开发 CA。
初始化使用 dev #1 已验证的 amd64 安装包，源码为 `0be2f614f8773c20d851d1771b352d80629b9bdd`。
配置和管理员凭据保存在 105 的 `/etc/ja-rttys-dev`；密码已轮换，bootstrap 文件已删除。
首次启动的目录权限问题和 CA 扩展问题已修正，未跳过 TLS 校验。
严格 TLS、真实登录/退出、未登录 API 401、数据库权限、租户重启持久化及 30 秒进程稳定性验收通过。

[dev #2](http://172.21.30.114:3214/job/ja-rttys/job/dev/2/) 以
`PUBLISH_IMAGE=true`、`DEPLOY_DEV=true` 人工触发，测试及双架构镜像推送成功。
升级在替换二进制前被旧版 schema v2 限制拦截，原服务恢复通过；该构建状态为 FAILURE。
根因是最新 master 的设备生命周期迁移已使用 schema v3，CI 包元数据和部署脚本仍写死 v2。

本次修复将包声明改为 v3，部署按安装包声明核对现有数据库并保持跨 schema 回退保护；
真实 E2E 在进程停止后读取数据库版本，CI 将其与包声明比较，版本漂移会在出包前失败。
交付回归改用当前 v3，并继续验证拒绝 v2 → v3、阻止 v4 数据库回退和失败后保留新写入。
后续精确提交的发布部署结果以 dev 子任务归档为准。

初始化与外部验收证据位于本地忽略目录 `out/deployment/dev105/`，不含密码或私钥。
