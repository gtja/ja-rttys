# install-rtty 安装约定

适用 Linux systemd，ARM64 / x86_64。使用经校验的离线包，不自动登记设备。管理员在面板先登记设备，安装时提供同一个编号与独立 Token。保留已有其他 rtty；只升级本工具管理的 rtty-tenant-poc 服务。证书校验默认开启。

验收失败方式（实现前）：未知架构、缺失依赖、无 root、非法设备编号/端口/用户名、缺失或校验不符的二进制/CA、空或非法 Token、已有同名非托管服务、安装后启动失败。前置失败不得改动当前服务；升级失败恢复原文件和原启用/运行状态。服务 active 不代表平台注册成功，最终用 API 在线状态及真实终端/文件往返确认。

使用方法：解压完整安装包后，运行 sudo ./install-rtty --id nvidia-174 --description 'NVIDIA 174'。按隐藏提示粘贴面板签发的设备Token；自动化可改用 --token-file /secure/token（权限600）。脚本与二进制、CA、SHA256SUMS需放在一起。当前默认服务端172.21.30.204:19512。服务名rtty-tenant-poc。Go客户端description短选项为-d，兼容用户所述旧客户端-D的描述语义。
