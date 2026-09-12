# 独立插件提交附件说明

提交人：刘健（95shisihan），课题一“扩展能力插件化框架”。

主仓通过 GitHub 链接及最终 Tag 提交；独立插件不发布远程仓库，通过邮件附件 `WeKnora-plugins-topic1-LiuJian-2026.zip` 提供源码、构建脚本、Manifest、README、已有依赖锁定文件和选定的 Windows 安装包。

## 附件结构

```text
README.md
MANIFEST.json
SHA256SUMS.txt
plugins/
  WeKnora-LocalDirectory-Plugin/
  WeKnora-Proprietary-Model-Plugin/
  WeKnora-Feishu-Plugin/
  WeKnora-TencentDocs-Plugin/
  WeKnora-ControlledHTTP-Plugin/
  WeKnora-Feishu-NoNetwork-Plugin/
```

`MANIFEST.json` 记录实际交付文件的 SHA-256、大小和插件源码基线信息。部分源码目录尚无 Git 提交，或有基线之后的修改，附件文件哈希是交付快照的准确标识，不能将已有 Git SHA 当成全部附件内容的版本。

解压后按各插件 README 构建。主验收是本地目录 Python 插件，其 Windows 安装包位于 `plugins/WeKnora-LocalDirectory-Plugin/dist/independent-directory-windows-x64-0.1.0.zip`。其他插件安装包位于对应 `dist/`，专有模型包位于该插件根目录。外层邮件 ZIP 包含源码，不能直接上传插件管理；应上传内部单个插件安装包。

附件不包含 Git 数据库、虚拟环境、构建缓存、临时日志、机器状态或未选定的旧版安装包。源目录中保留的历史验收记录仅证明记录当时的执行结果；本次打包核对文件完整性，不宣称重新执行全部在线业务验收。

主仓框架、SDK、模板及验收文档以 `rhino-2026-final-1` 与 `submission.yaml` 中的完整 Commit SHA 为准。`submission.yaml` 在打 Tag 后单独提交，并同时作为邮件附件提供。
