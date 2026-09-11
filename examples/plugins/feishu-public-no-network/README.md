# 飞书公开链接（禁网验证）

这是原飞书公开链接数据源的独立诊断变体，沿用 `settings.public_urls` 表单，使用 Go stdio gRPC 接入现有 Windows 原生禁网架构。插件 ID 为 `dev.example.feishu-public-no-network`，不会替换原来的联网导入插件。

本插件仅用于测试连接，不实现知识库导入、正文解析或同步。测试连接会真实发送 HTTPS GET 并读取最多 8 KiB 响应；HTTP 200 表示可访问，不表示正文可导入。没有在代码里直接返回“禁止联网”，也没有通过宿主代发请求。

## 安装与验证

1. 在运行新架构的 Windows amd64 WeKnora 中进入“设置 → 插件管理”，上传 `feishu-public-no-network-windows-x64-0.1.0.zip` 并启用。
2. 预期插件健康检查正常。其配置是 `runtime.address: stdio://` 和 `permissions.network.outbound: false`；宿主通过 AppContainer 权限阻止 IP 网络，管道 RPC 不受影响。
3. 在知识库数据源中选择“飞书公开链接（禁网验证）”，填写原来可访问的公开链接，点击“测试连接”。
4. 预期失败，错误包含“飞书公开链接网络请求失败（本插件声明禁止联网）”及 Windows 网络错误（可能在 DNS 阶段报告 no such host，或报告 socket 权限拒绝）。失败后插件仍应健康。

仅“测试连接失败”不能独自证明禁网：也可能是链接不公开、DNS 故障或 HTTP 错误。自动化验收先验证同一链接在普通进程中可达，再验证正式 EXE 在宿主使用的 `windowsandbox.Start` 中无法访问，最后再次确认普通进程仍可达，同时验证前后的 gRPC Health。Windows 默认 DNS 接口可能先报告 no such host；这一结果是受限/非受限对照，不冒称已获得明确 Winsock 拒绝码。该测试不等同于页面点击验收，也不要求 WFP 审计订阅可用。

## 构建

在仓库根目录使用 PowerShell 和 Go 1.26：

```powershell
./examples/plugins/feishu-public-no-network/build.ps1
```

生成的 ZIP 位于 `artifacts/plugins/`，包含清单和独立 EXE，无需安装 Python 或 Docker。

要运行真实飞书/系统权限对照验收：

```powershell
./examples/plugins/feishu-public-no-network/build.ps1 -TestURL 'https://docs.feishu.cn/article/wiki/ACKgw651xiDd3WkiTT8cmDhBnDg'
```

验收终端本身必须允许联网，否则普通进程对照会失败；测试会创建并清理临时受限身份及其目录 ACL。公开链接访问状态会变化，验收不保存页面正文。旧 TCP Python 插件无法仅靠修改 `outbound` 变为 stdio 插件。
