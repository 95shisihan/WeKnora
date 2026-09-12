# 独立仓库本地目录插件（Windows）

独立 Git 仓库，不是 WeKnora 的子目录、子模块或 worktree。插件通过公开 datasource/v1 协议接入，构建不读取 WeKnora 主仓源码，不要求修改核心工厂或重新编译宿主。

## 构建

安装 Windows x64 Python 3.10+ 与 Git，在本仓库执行：

```powershell
python -m venv .venv
.venv/Scripts/python.exe -m pip install -r requirements.txt
.venv/Scripts/python.exe build_windows.py
```

构建脚本生成协议代码、运行单测、打包 EXE，输出 `dist/independent-directory-windows-x64-0.1.0.zip`、SHA-256 和 `evidence/build.json`。用户上传 ZIP 即可，不需要安装 Python。

## 安装与使用

1. 在 Windows WeKnora 0.7.x（含插件框架与原生 stdio 支持）以系统管理员身份进入插件管理。
2. 上传 ZIP 并启用“独立仓库本地目录插件”。
3. 新建测试知识库，配置当前可用的 Embedding 模型。
4. 添加“独立仓库本地目录”数据源，填写测试目录的绝对路径，测试连接并保存。
5. 手动同步，等待知识文档解析完成，使用混合检索查询测试文本。

插件声明禁止直接联网，使用 stdio gRPC 通信；宿主只向受限进程授予配置目录的只读权限。只测试文本文件，不声明支持任意格式。按完整目录同步，不提供资源范围过滤；文件变化由 SHA-256 识别，删除同步是否生效由宿主数据源设置决定。

## 来源与验收边界

初始代码改编自 WeKnora 的独立 Python 数据源模板；`datasource.proto` 和 `stdio_grpc.py` 随仓库保留，适用随附 MIT LICENSE。构建脚本无主仓相对路径、源码 import 或自动回拷逻辑。已有模板改编证明独立构建与接入，不等于“未参与开发者只凭文档”的人工盲测。

验收通过正式应用 API 创建专用知识库；不直接写数据库，不替代宿主解析和索引。后续完整记录见 `evidence/`。

## 本次验收结果

Windows 实测通过：初次同步新增 2 篇；无修改再同步为 0；只改 Alpha 后更新 1 篇，Beta 的 ID、哈希和更新时间不变。各轮混合检索、纯向量检索均召回测试口令。详见 [验收报告](evidence/REPORT.md)。

## API 复现流程

当前脚本针对本机 Lite 环境，调用正常的 `auth/auto-setup` 接口，令牌只保留在内存中。准备一个已有可用模型配置的“飞书受控联网验证”知识库作为模型配置来源；新建的验收知识库不会复制其文档。主仓应为干净状态，已构建宿主放在 `.tools/bin/weknora-lite-dev.exe`，独立插件 ID 尚未安装。

```powershell
.venv/Scripts/python.exe verify_live.py prepare --host E:/Tengxun_RAG/WeKnora
.venv/Scripts/python.exe verify_live.py initial
.venv/Scripts/python.exe verify_live.py wait
.venv/Scripts/python.exe verify_live.py unchanged
.venv/Scripts/python.exe verify_live.py wait
.venv/Scripts/python.exe verify_live.py changed
.venv/Scripts/python.exe verify_live.py wait
.venv/Scripts/python.exe verify_live.py finish
```

已经完成本次验收的机器请直接查看证据和知识库，不要再次执行 `prepare`；它会拒绝重复安装。`wait` 每 5 秒检查一次，最多等待 5 分钟，严格检查同步计数和检索正文。

复现相同构建依赖可使用 `requirements-lock.txt`；ZIP 哈希是本次构建标识，不承诺不同构建时间/环境产生逐字节一致的 PyInstaller 文件。

若测试目录由另一个 Windows 账户创建，应由目录所有者为运行 WeKnora 的用户授予该测试目录的管理权限，使宿主能添加和撤销插件只读 ACL。不要扩大整个磁盘或业务目录权限。本次仅为新建 `sample-data` 及其中两个测试文件处理了账户间权限。

## Go 实现

原主仓的 Go 本地目录插件已迁入 [go-plugin](go-plugin/README.md)，拥有独立 go.mod 和公开协议/SDK。根目录 Python 插件及原验收证据保持不变，两者插件 ID 不同。
