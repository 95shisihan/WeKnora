"""Build an uploadable Windows x64 ZIP; run with the plugin virtualenv Python."""
from pathlib import Path
import hashlib
import re
import subprocess
import sys
import zipfile
import shutil

ROOT = Path(__file__).resolve().parent


def main():
    if sys.platform != "win32":
        raise SystemExit("Run this build on Windows x64")
    output = ROOT / "dist"
    public = "--public" in sys.argv
    name = "feishu-public" if public else "feishu-wiki"
    # Vendored copies keep the template usable outside the main repository.
    sdk = ROOT.parents[1] / "sdk" / "python"
    if public and sdk.is_dir():
        for module in ("host_http.py", "stdio_grpc.py"):
            shutil.copyfile(sdk / module, ROOT / module)
    output.mkdir(exist_ok=True)
    subprocess.run([sys.executable, "-m", "grpc_tools.protoc", "-I.",
                    "--python_out=.", "--grpc_python_out=.", "datasource.proto"], cwd=ROOT, check=True)
    subprocess.run([sys.executable, "-m", "PyInstaller", "--noconfirm", "--onedir",
                    "--name", "weknora-" + name, "--distpath", str(output / "bin"),
                    "--workpath", str(output / "build"), "--specpath", str(output),
                    "public_server.py" if public else "server.py"], cwd=ROOT, check=True)
    manifest = (ROOT / ("plugin.public.yaml" if public else "plugin.yaml")).read_text(encoding="utf-8")
    if not public:
        manifest, count = re.subn(r"  runtime:\n.*?(?=  configSchema:)",
        f"  runtime:\n    type: grpc\n    command: [bin/weknora-{name}.exe]\n"
        f"    address: 127.0.0.1:{50080 if public else 50079}\n    startupTimeout: 60s\n", manifest, flags=re.S)
        if count != 1:
            raise SystemExit("Could not replace runtime in manifest")
    version = "0.2.0" if public else "0.1.0"
    archive = output / f"{name}-windows-x64-{version}.zip"
    distribution = output / "bin" / ("weknora-" + name)
    executable = distribution / f"weknora-{name}.exe"
    if executable.read_bytes()[:2] != b"MZ":
        raise SystemExit("Build did not produce a Windows executable")
    with zipfile.ZipFile(archive, "w", zipfile.ZIP_DEFLATED) as bundle:
        bundle.writestr("plugin.yaml", manifest)
        for file in sorted(distribution.rglob("*")):
            # grpc wheels include optional C++ source headers, not runtime assets.
            if file.is_file() and file.suffix.lower() not in (".cc", ".h"):
                bundle.write(file, "bin/" + file.relative_to(distribution).as_posix())
    if archive.stat().st_size > 64 * 1024 * 1024:
        raise SystemExit("ZIP exceeds host upload limit")
    checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_suffix(".zip.sha256").write_text(checksum + "  " + archive.name + "\n")
    print(f"Upload bundle: {archive}\nSHA256: {checksum}")


if __name__ == "__main__":
    main()
