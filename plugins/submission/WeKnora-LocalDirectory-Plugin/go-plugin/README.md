# local-directory 独立插件

源码已从 WeKnora 主仓移出。此目录自带公开 Proto、生成代码、SDK、Go 模块及许可证，构建不读取宿主源码，不使用本地 replace。需要 Go 1.26；安装 ZIP 的用户不需要 Go。

```powershell
go test -mod=mod ./...
.\build.ps1
```

上传 `dist/local-directory-windows-x64-0.1.0.zip` 到管理员插件管理页。源码、Go 文件和构建脚本不进入 ZIP；宿主使用解包后的可执行文件和 Manifest。

原生隔离属于宿主职责，其集成测试位于 WeKnora `internal/plugin/`，通过环境变量读取本仓库编译出的制品。目录插件使用 `WEKNORA_NATIVE_DIRECTORY_EXE`；禁网对照使用 `WEKNORA_FEISHU_DENIED_EXE`；腾讯文档使用 `WEKNORA_TENCENT_DOCS_ZIP`；受控 HTTP 使用 `WEKNORA_CONTROLLED_HTTP_EXE`。这些是可选联调测试，不是独立构建依赖。

本地迁移基线：WeKnora `cb7a5b9f28753eefffc39328a5cc3133bd446f73`。独立仓库尚未发布远端。
