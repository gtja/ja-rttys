# 在 RTTYS 制作 PSDK 版本

在“应用升级”点击“新建 PSDK 版本”，填写版本号、目标 Docker 镜像完整本地 ID（sha256:64位）、该镜像内 ja_UAV_PSDK 程序 SHA-256，选择对应的 uavModel 文件。文件必须是非空 JSON 对象，最大1MiB。

后端自动计算模型摘要、生成固定 ja-psdk-container 模块的 manifest、制作并签名 .mender、调用 Mender 制品上传接口。完成后自动选择该版本，用户仍需选择设备并确认发布；制作版本本身不触发升级。

当前仍要求目标镜像已经缓存到设备，不能把仓库标签或仓库分发摘要直接当成本地镜像 ID。设备必须安装相应 ja-psdk-container 更新模块，并信任本租户签名密钥的公钥。

## 服务端配置

在原有私有 mender.json 配置增加：

```json
{
  "artifact_tool": "/opt/rttys-tools/mender-artifact",
  "signing_keys": {
    "TENANT_INTERNAL_ID": "/etc/rttys/signing/tenant-key.pem"
  }
}
```

配置示例是新增字段，保留原 url、ca_file、username、password、artifact_prefixes、bindings。配置修改后重启生效。工具应是管理员安装的对应服务器架构二进制。签名私钥是绝对路径、服务账号拥有、0600或更严格，不能从浏览器上传密钥，也不能把服务器CA私钥拿来签应用。每租户可以指定独立签名私钥。配置或密钥未准备时新建入口不显示，已有版本发布仍可使用。

版本号仅允许字母、数字、点、下划线和短横线，长度1–80。同租户不可复用版本号。服务器自动使用既有租户前缀生成完整制品名，浏览器不能指定租户归属、模块类型、设备类型、工具路径、签名路径或任意安装脚本。

制作记录保存在 ota_versions，含版本、制品名、镜像和程序/模型摘要、创建时间及状态。导入过程中断或5xx会标为 unknown，不自动重试，管理员应核对Mender是否已有制品后再处理。服务重启将building标为interrupted；失败版本也保留，不盲目重复上传。当前不提供版本删除、未知状态自动对账或制品下载入口，记录列表显示最近50条。

全实例同时最多制作1个版本。请求总量2MiB，模型1MiB，制品4MiB。生成文件使用独立临时目录，结束清理；工具超时90秒。当前租户管理员可以创建，普通操作员/查看者拒绝。上传前重新核对当前成员权限；Mender凭据和签名路径不返回浏览器。

当前已有版本目录仍沿用原租户前缀隔离，新增制作记录并未实现任意已有Mender制品的归属迁移。版本创建审计与部署审计分开。

## 验证

```sh
go build -o /tmp/rttys-version-builder ./cmd/rttys
python3 scripts/ota-version-e2e.py \
  --server /tmp/rttys-version-builder \
  --tool /absolute/mender-artifact
```

该脚本启动真实本地rttys与真实mender-artifact工具，Mender上传端使用隔离HTTPS测试服务，并实际用公钥验证上传制品签名。检查匿名/操作员拒绝、摘要格式、空模型、真实制品生成、摘要、重复版本拒绝、列表回读、签名配置不泄漏、签名失败不上传。测试服务不等同真实Mender部署验收。

2026-10-29：前后端构建及已有后端测试通过，本地协议验收通过。网络恢复后已部署204，通过真实rttys接口制作签名制品、导入Mender、回读可发布列表和ready制作记录；重复版本返回409。首次工具目录缺少服务账号遍历权限导致制作失败，已修正且保留failed记录，未上传失败制品。浏览器页面点击尚未验收。没有触发167升级，未修改公网。
