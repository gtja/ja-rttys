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
