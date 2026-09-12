# 独立插件提交附件说明

本次代码成果不发布独立插件远程仓库。邮件中随附独立插件压缩包，压缩包内保留插件源码、构建脚本、依赖锁定文件、Manifest、构建产物和 README。

## 建议附件结构

```text
WeKnora-LocalDirectory-Plugin-windows-x64.zip
└── WeKnora-LocalDirectory-Plugin/
    ├── README.md
    ├── requirements.txt
    ├── requirements-lock.txt
    ├── build_windows.py
    ├── plugin.yaml
    └── dist/
```

邮件正文写明附件名称、构建入口和验收版本。主仓库中的框架、SDK、模板和测试证据仍以最终 Tag 与完整 Commit SHA 为准。
