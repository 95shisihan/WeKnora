# 最小模型插件模板

本模板只提供协议、健康检查和启动入口。默认业务操作返回 UNIMPLEMENTED，开发者实现 SDK Backend 后才具备真实推理能力。

在宿主根目录运行 `python plugin/scaffold_model_plugin.py <新目录> --module example.org/my-model`，再进入生成目录运行 `go build -mod=mod -o bin/model-plugin.exe .`。此模板不含厂商适配实现。完整协议见 [模型文档](../../../plugin/MODEL-INFERENCE.md)。
