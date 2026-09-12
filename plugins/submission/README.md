# WeKnora 课题一插件提交目录

提交人：刘健（95shisihan）。本目录是邮件大附件受限时的替代交付方式，随主仓库提交，不是主仓运行时自动安装目录。

主仓最终版本 Tag 为 `rhino-2026-final-1`；本目录是 Tag 之后补充的交付材料，当前可访问地址：
`https://github.com/95shisihan/WeKnora/tree/main/plugins/submission`

目录包含六个独立插件的源码、README、Manifest、构建脚本、依赖文件，以及各插件已有的 Windows 安装包（位于对应的 `artifacts/`）。上传到插件管理时，请选择某一个 `artifacts/*.zip`，不要上传整个 `plugins/submission` 目录。

## 插件目录

- `WeKnora-LocalDirectory-Plugin`：本地目录数据源，主验收插件。
- `WeKnora-Proprietary-Model-Plugin`：专有模型协议参考插件。
- `WeKnora-Feishu-Plugin`：飞书插件。
- `WeKnora-TencentDocs-Plugin`：腾讯文档插件。
- `WeKnora-ControlledHTTP-Plugin`：受控 HTTP 示例插件。
- `WeKnora-Feishu-NoNetwork-Plugin`：禁网对照插件。

源码构建入口和运行边界见每个插件的 README。构建不读取主仓源码；本目录中的安装包是已存在的 Windows 构建制品，本次提交只做文件归档，没有重新构建或重新执行在线业务验收。
