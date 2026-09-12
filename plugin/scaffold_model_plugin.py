"""Create a self-contained Go model plugin with vendored public protocol/SDK.

Run: python plugin/scaffold_model_plugin.py E:/plugins/my-model --module example.org/my-model
The destination must not exist. No main-repository dependency or replace directive
is written; all imports are rewritten to the new module.
"""
import argparse
from pathlib import Path
import re
import shutil


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("destination", type=Path)
    parser.add_argument("--module", default="example.org/weknora-model-plugin")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._/-]*", args.module) or ".." in args.module or "//" in args.module:
        parser.error("module must be a plain Go import path")
    root = Path(__file__).resolve().parent.parent
    dest = args.destination.resolve()
    if dest.exists():
        parser.error("destination already exists; refusing to overwrite")
    mapping = {
        "plugin/templates/model-provider-go/main.go": "main.go",
        "plugin/templates/model-provider-go/plugin.yaml": "plugin.yaml",
        "plugin/templates/model-provider-go/README.md": "README.md",
        "plugin/MODEL-INFERENCE.md": "MODEL-INFERENCE.md",
        "plugin/proto/model_provider.proto": "proto/model_provider.proto",
        "plugin/proto/model_provider.pb.go": "proto/model_provider.pb.go",
        "plugin/proto/model_provider_grpc.pb.go": "proto/model_provider_grpc.pb.go",
        "plugin/sdk/model/model.go": "sdk/model/model.go",
        "plugin/sdk/model/server.go": "sdk/model/server.go",
        "plugin/sdk/transport/stdio.go": "sdk/transport/stdio.go",
        "plugin/sdk/transport/services.go": "sdk/transport/services.go",
        "LICENSE": "LICENSE",
    }
    replacements = {
        "github.com/Tencent/WeKnora/plugin/proto": f"{args.module}/proto",
        "github.com/Tencent/WeKnora/plugin/sdk": f"{args.module}/sdk",
    }
    # Verify all inputs before creating any output.
    for source in mapping:
        if not (root / source).is_file():
            parser.error(f"missing template input: {source}")
    dest.mkdir(parents=True)
    for source, target in mapping.items():
        output = dest / target
        output.parent.mkdir(parents=True, exist_ok=True)
        text = (root / source).read_text(encoding="utf-8")
        # Generated protobuf descriptors contain length-prefixed binary strings.
        # They have no repository imports; rewriting their embedded go_package
        # text would corrupt the descriptor. Only rewrite source imports/options.
        if not target.endswith(".pb.go"):
            for old, new in replacements.items():
                text = text.replace(old, new)
        if target == "README.md":
            text = text.replace("../../../plugin/MODEL-INFERENCE.md", "MODEL-INFERENCE.md")
        output.write_text(text, encoding="utf-8")
    (dest / "go.mod").write_text(f"module {args.module}\n\ngo 1.26.0\n\nrequire (\n\tgoogle.golang.org/grpc v1.81.0\n\tgoogle.golang.org/protobuf v1.36.11\n\tgolang.org/x/net v0.56.0 // indirect\n\tgolang.org/x/sys v0.46.0 // indirect\n\tgolang.org/x/text v0.38.0 // indirect\n\tgoogle.golang.org/genproto/googleapis/rpc v0.0.0-20260427160629-7cedc36a6bc4 // indirect\n)\n", encoding="utf-8")
    # Reuse verified dependency checksums; go mod tidy prunes unused entries.
    shutil.copyfile(root / "go.sum", dest / "go.sum")
    (dest / ".gitignore").write_text("bin/\n*.zip\n", encoding="utf-8")
    print(f"Created standalone plugin: {dest}")
    print("Build from that directory: go build -mod=mod -o bin/model-plugin.exe .")
    print("Edit metadata IDs in main.go and plugin.yaml; implement your SDK Backend in the new repository.")


if __name__ == "__main__":
    main()
