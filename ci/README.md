# Jenkins CI/CD 初版

流水线读取仓库根目录的 [Jenkinsfile](../Jenkinsfile)。参考已有的
`ja-alarm-mqtt-kafka-bridge` 和 `defenseAI` Jenkins 实现，复用源码检出、凭据 ID、
105 构建节点及版本追溯方式。本仓库保留单进程 SQLite、systemd 部署方式。

默认每次构建只验证和出包，`PUBLISH_IMAGE=false`、`DEPLOY_DEV=false`。
2026-10-26 已创建 [ja-rttys 多分支任务](http://172.21.30.114:3214/job/ja-rttys/)，
修复分支的 [首轮构建 #1](http://172.21.30.114:3214/job/ja-rttys/job/codex%252Fsecurity-scan-20261024/1/) 成功。
dev 开发环境目标为 **172.21.30.105**，与 204 上的租户功能测试实例分开。
105 已初始化独立的 `ja-rttys-dev` systemd 服务，使用专用非 root 账号和独立数据库；
控制台为 `https://172.21.30.105:18446`，设备 TLS 端口为 `19512`。
本地 Docker 和 Jenkins 实际节点验证结果见 [验证记录](verification.md)。

## 流程

1. 在 `SCM_AGENT_LABEL`（默认 `built-in`）检出 Jenkins 选定的精确提交。
   用 `git archive` 生成源码快照及 SHA256，再传给 105，避免 105 直连 GitHub 的不稳定问题。
2. 在 `BUILD_AGENT_LABEL`（默认既有的 `ja-pipeline-builder-105`）解包并核对快照。
   节点需要 Bash、Git/检出节点、Python 3.8+、Docker、Buildx 及 BuildKit；宿主机不需要安装 Go/Node。
   先检查内网基础镜像及工具版本，节点已有镜像时复用本地缓存。
3. 运行交付和回滚测试，然后在临时构建镜像内完成 `npm ci`、前端构建、完整 npm 审计、
   `go mod verify`、Go 回归、`go vet`、`govulncheck` 及真实 HTTPS/TLS/WebSocket、OAuth/MCP 本地端到端验证。
   仅排除需要外部客户端的手工压力用例 `TestRttysStress`。任一检查失败均阻止出包及部署。
   每次构建传入独立 `CI_RUN_ID`，重新执行审计与回归。
4. 从同一提交交叉编译 Linux amd64、arm64，无需 QEMU；检查镜像架构、二进制 ELF 架构及源码 revision。
   出包包含二进制、示例配置、systemd unit、操作说明、manifest 和 SHA256。
   各架构 `image.json` 记录源码、二进制 SHA、镜像 ID；仅推送成功后记录 digest 和 `pushed=true`。
5. 如显式启用 `PUBLISH_IMAGE`，使用临时 `DOCKER_CONFIG` 登录、推送，并在结束时删除登录信息。
6. 如人工在 `dev` 显式启用 `DEPLOY_DEV`，使用对应 amd64 包升级 105 上独立的开发实例。

| 分支 | 检查与双架构出包 | 显式启用镜像推送时 | 部署 |
| --- | --- | --- | --- |
| dev | 是 | `registry.jingan.com:32008/ja/ja-rttys` | 仅人工启用后升级 105 开发实例 |
| master | 是 | `registry.jingan.com:32008/ja/ja-rttys` | 禁用 |
| test/release | 是 | `registry.cn-hangzhou.aliyuncs.com/jingansi/ja-rttys` | 禁用 |
| 其他分支/PR | 是 | 禁用 | 禁用 |

版本包含服务版本、UTC 时间、分支、源码 SHA 前 12 位和 Jenkins 构建号。
arm64 镜像 tag 追加 `-arm`。这两个架构分别交付，不发布多架构 manifest 或 `latest`。
GitHub Actions 上游示例仍保存在 `docs/upstream-workflows`，未启用。

本机单独构建前端需 Node 22.13+。Vue I18n 及构建插件已更新至修复版本；
minimatch 的 7–10 系列因 [GHSA-g3hv-75wg-m249](https://github.com/advisories/GHSA-g3hv-75wg-m249)
没有对应修复版本，使用定向 override 到 11.0.5，保留 ESLint 依赖的 3.x 系列。

## 内网基础镜像

日常构建直接使用以下镜像，配置集中在 [base-images.sh](base-images.sh)。
Dockerfile 也使用相同默认值；`NODE_BUILD_IMAGE`、`GO_BUILD_IMAGE`、`RUNTIME_IMAGE`
可覆盖构建参数，`BASE_IMAGE_REGISTRY` 可统一替换仓库前缀。

| 用途 | 镜像 |
| --- | --- |
| 前端 | `registry.jingan.com:32008/ja/node:22-bookworm-slim` |
| Go 编译和验证 | `registry.jingan.com:32008/ja/ja-rttys-build:go1.27.3-vuln1.9.0-v1` |
| 最终运行镜像 | `registry.jingan.com:32008/ja/alpine:3.22` |
| 构建工具镜像的原始 Go 基础镜像 | `registry.jingan.com:32008/ja/golang:1.27.3-bookworm` |

基础镜像维护与日常 CI 分开。需要更新版本时，在有 Docker Hub 访问能力、
且已经登录内网仓库的 105 上，于 `/data1/wanghaoyang/ja-rttys` 执行：

```sh
bash ci/mirror-base-images.sh
# 105 无法直连 Docker Hub 时，可显式使用该机已有的镜像代理：
UPSTREAM_IMAGE_PREFIX=docker.m.daocloud.io/library bash ci/mirror-base-images.sh
```

该脚本从同一上游 digest 拉取每种镜像的 amd64、arm64 版本，分别 tag、推送到 `ja/`，
再合成双架构 manifest。随后用 [Dockerfile.build-base](Dockerfile.build-base)
生成两种架构的 Go 工具镜像；它包含 Go 1.27.3、Python 3、OpenSSL 和预装的
govulncheck v1.9.0。交叉编译 govulncheck，arm64 基础镜像构建无需 QEMU。
基础工具版本更新时应使用新 tag，并同步修改 `base-images.sh` 和 Dockerfile 的默认值。

日常 Jenkins 不执行上述镜像维护脚本，不重复运行 apt 安装或下载 govulncheck。
`npm ci` 与 Go 模块仍按项目锁文件解析，npm 下载、Go 模块和编译缓存通过 BuildKit 复用；
依赖发生变化时仍需访问 npm/Go 软件源，安全审计仍需访问漏洞数据库。
`ci/check-base-images.sh` 会验证构建节点上的工具可运行；双架构应用镜像仍在同一节点交叉编译。

105 的该仓库使用 HTTP。流水线通过 `ci/ensure-builder.sh` 使用本项目专用的
`ja-rttys-ci` Buildx 构建器及 `ci/buildkitd.toml`，只为该仓库指定 HTTP。
构建器使用内网的 `ja/buildkit:v0.30.0`，缓存保存在它自己的 Docker volume 中，上限约 20GB。
不会修改 Docker daemon、重启 Docker 或切换其他项目的默认构建器；可用
`RTTYS_BUILDER`、`RTTYS_BUILDKIT_IMAGE` 覆盖构建器名称和镜像。
脚本使用 `--load` 将最终镜像导入本机 Docker，以保留镜像校验和二进制提取步骤。
Buildx 元数据默认位于 `$HOME/.config/ja-rttys/buildx`，与镜像推送时临时使用的
`DOCKER_CONFIG` 分开，登录和清理发布凭据不会丢失构建器配置或缓存。

## Jenkins 接入

导入 [jenkins-multibranch.xml](jenkins-multibranch.xml)，任务名称建议 `ja-rttys`。
配置按每 5 分钟扫描，初始包含 `master dev test release codex/security-scan-20261024`；
只有存在 Jenkinsfile 的分支会创建子任务。默认参数会执行验证和出包。
上述任务已导入现有 114 Jenkins；安全修复和流水线现已合入基于最新 master 建立的 `dev`，
master 分支未改动。推送 dev 后由多分支扫描创建对应构建任务。其他 Jenkins 安装可使用此 XML 接入。

复用历史工程中的凭据 ID，不复制任何凭据值：

| 凭据 ID | 类型 | 用途 |
| --- | --- | --- |
| `github-pat` | Username/password | GitHub Branch Source / HTTPS 检出 |
| `registry-jingan-com` | Username/password | master/dev 私仓推送 |
| `public-aliyun-registry` | Username/password | test/release 公仓推送 |
| `ssh-105` | Username/password | 可选的独立 dev 实例升级；账号需有该主机的 root 权限 |

需要 Pipeline、Git/GitHub Branch Source、Credentials Binding；启用部署时构建节点还需 OpenSSH 和 sshpass。
105 的 SSH 公钥由运维预置在构建用户的 `~/.ssh/known_hosts`，使用严格主机校验；不读取 204 凭据。
仓库和凭据的可写权限应限制在可信维护者；不应让不可信 PR 修改后读取发布凭据。
初版不配置邮件/钉钉发送，结果通过 Jenkins 日志和归档查看。

## 105 dev 升级与回退

部署入口是 [deploy-dev.sh](deploy-dev.sh)，固定通过 `ssh-105` 连接 105，调用
[scripts/ci-deploy.py](../scripts/ci-deploy.py)。仅 `dev` 分支允许人工启用 `DEPLOY_DEV`，
master/test/release/PR 不部署。原 `DEPLOY_TEST` 参数已移除，流水线不再连接 204。

该入口仅更新已经初始化的 systemd 实例，不安装服务、不覆盖 unit/TLS/用户配置、不初始化新数据库。
[deploy-dev.example.json](deploy-dev.example.json) 给出独立开发实例的建议布局：
服务 `ja-rttys-dev`，目录 `/opt/ja-rttys-dev`，数据库 `/opt/ja-rttys-dev/data/identity.db`。
105 已按该布局初始化，实际部署配置位于 `/etc/ja-rttys-dev/ci-deploy.json`。
其他新环境须先核对实际路径并初始化，将配置放在对应主机的该路径，
root 所有、0600；备份目录 root 所有、0700。开发数据库与账号独立初始化。
`console_url` 的证书必须由 `tls_ca` 信任并匹配主机名，不使用跳过 TLS 校验的方式。
105 使用独立开发 CA，公钥证书在 `/etc/ja-rttys-dev/ca.pem`；浏览器及设备客户端需信任该 CA。
初始管理员为 `admin`，密码已轮换，保存在 105 的 `/etc/ja-rttys-dev/initial-admin.json`（root、0600）；
bootstrap 文件已删除。管理员凭据及 TLS 私钥不进入仓库或 Jenkins 归档。

部署前校验包 SHA256、内层二进制 SHA256、源码提交、架构和 schema；
核对 systemd MainPID 运行的是配置中的原二进制，并实际打开了指定数据库。
目标目录使用 `flock` 锁，避免不同分支同时升级同一实例。
停止单实例后备份旧二进制和 SQLite，再原子替换二进制并启动。
验证 `/proc/<MainPID>/exe` 的 SHA256、HTTPS 登录页面及未登录 `/api/me` 的 401 边界。

验收失败会恢复旧二进制并重新验证，流水线仍报告失败；新产生的数据库写入不会被自动覆盖。
仅支持数据库与包声明相同 schema 的升级，当前包为 v4；端到端门禁会核对真实数据库版本与包声明。
v3 首次升级到包含 OAuth/MCP 的 v4 时，同 schema 部署门禁会拒绝自动升级。需安排维护窗口，停止单实例并备份数据库、旧二进制，用新程序完成并验收迁移后再启用 v4 的常规发布；不要跳过 schema 检查。
若新程序意外修改 schema，保持服务停止并报告回滚失败，
由运维使用保存的数据库和旧二进制人工恢复，避免旧程序读写不兼容结构。
备份和结果回执保留在目标机，数据库、密码和私钥不进入 Jenkins 归档。

升级期间终端/WebSocket 会断开；本次 Cookie 调整需要用户重新登录。
上述验收仅覆盖进程、版本、TLS 和未登录边界，设备业务及浏览器验收、线上安全复扫仍需另行执行。

## 本地验证

```sh
for script in ci-package.sh scripts/ci-verify.sh ci/*.sh; do bash -n "$script"; done
python3 -m unittest discover -s tests/ci -p 'test_*.py' -v
# Linux Docker 节点；不会推送或部署
SOURCE_COMMIT="$(git rev-parse HEAD)" CI_RUN_ID="local-$(date +%s)" bash scripts/ci-verify.sh
SOURCE_COMMIT="$(git rev-parse HEAD)" PACKAGE_VERSION=local-check TARGET_ARCH=amd64 bash ci-package.sh
```

构建与测试证据在 `out/`；Git 和 Docker 上下文均排除该目录。
源代码审计与业务回归的已有结果见 [扫描整改说明](../README-TENANT.md#security-scan-remediation-2026-10-24)。
Jenkins 服务端语法验证、105 实际节点检查和双架构镜像构建已通过；
105 的 SSH 凭据、构建节点主机公钥校验和独立实例初始化已完成。
dev #2 实际推送双架构镜像成功，升级因旧脚本写死 schema v2 而被拦截，原服务恢复通过。
修复后的流水线以真实 E2E 数据库版本核对包元数据；部署只接受数据库与安装包 schema 相同的升级。
