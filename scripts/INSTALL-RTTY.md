# install-rtty 安装约定

适用 Linux systemd，ARM64 / x86_64。使用经校验的离线包，不自动登记设备。管理员在面板先登记设备，安装时提供客户端上报编号与平台签发的独立 Token。鉴权以 Token 确定设备，客户端 `-I` 可以与登记编号不同，也可在重连后变化；平台登记编号和权限保持不变。保留已有其他 rtty；只升级本工具管理的 rtty-tenant-poc 服务。证书校验默认开启。

验收失败方式（实现前）：未知架构、缺失依赖、无 root、非法设备编号/端口/用户名、缺失或校验不符的二进制/CA、空或非法 Token、已有同名非托管服务、安装后启动失败。前置失败不得改动当前服务；升级失败恢复原文件和原启用/运行状态。服务 active 不代表平台注册成功，最终用 API 在线状态及真实终端/文件往返确认。

使用方法：解压完整安装包后，运行 `sudo ./install-rtty`，交互输入设备名称，再按隐藏提示粘贴面板签发的设备 Token。名称支持中文，作为客户端注册描述上报；平台登记名称仍由平台管理。客户端编号自动取本机 machine-id 的前24位并加 `rtty-` 前缀，也可用 `--id` 指定；自动化可改用 --token-file /secure/token（权限600）。脚本与二进制、CA、SHA256SUMS需放在一起。当前默认服务端 `47.111.133.127:5912`（TLS）。服务名rtty-tenant-poc。Go客户端description短选项为-d；-D是后台运行选项，不能作为描述参数。


## 统一 CA 随包安装

项目证书源：`certs/trusted-ca.pem`。客户端包内为 `cert.pem`，包含公网与204的可信证书，不含私钥。安装时自动写入 `/etc/rtty/trusted-ca.pem`，客户端配置引用这个全局路径。设备编号与Token仍须在目标平台注册，不能跨独立平台自动互认。

公网新安装示例：

```sh
sudo ./install-rtty
# 非交互：sudo ./install-rtty --name "设备名称" --token-file /secure/token
# 204环境：sudo ./install-rtty --host 172.21.30.204 --port 19512
```

如果已有不同的全局CA文件，安装器会在修改服务前停止，先合并所需信任证书后再安装，不会盲目删除其他平台信任。失败回退会移除本次新建的全局CA文件，既有相同CA保持不动。

构建离线客户端包（输入已批准的两种架构客户端二进制）：

```sh
python3 scripts/build-client-package.py --bin-dir /path/to/bin --output out/ja-rtty-client-linux.tar.gz
```

输入文件名为 `rtty-linux-amd64`、`rtty-linux-arm64`。构建检查ELF架构和PEM格式，输出manifest、逐文件SHA256SUMS及压缩包摘要。

已由startRtty.sh托管的双链路设备（如173）不要用此单连接安装器覆盖服务；173已经切换到上述统一CA路径，无需重装。本次不修改在线服务。


## 统一文件传输命令

新版安装包完成安装后，所有 rttys 终端均使用 `rtty -S 文件` 下载，`rtty -R` 上传。
统一入口根据当前控制终端的 Go 文件传输套接字选择助手，旧终端使用保留的 C 程序。
已有盒子可解压新包后单独执行 `sudo bash ./install-rtty-command` 安装兼容入口，无需重新输入 Token 或重启客户端服务。
原始启动参数继续交给 `/usr/local/libexec/ja-rtty/rtty-legacy`。已有自定义 rtty 脚本或符号链接时安装器会停止，需先核对入口。
