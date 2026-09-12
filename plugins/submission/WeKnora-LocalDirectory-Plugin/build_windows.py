"""Build exclusively from this repository and the active Python environment."""
import hashlib
import json
import sysconfig
from pathlib import Path
import subprocess
import sys
import zipfile

ROOT = Path(__file__).resolve().parent

def main():
    if sys.platform != "win32" or sysconfig.get_platform() != "win-amd64":
        raise SystemExit("Windows x64 is required")
    subprocess.run([sys.executable, "-m", "grpc_tools.protoc", "-I.", "--python_out=.",
                    "--grpc_python_out=.", "datasource.proto"], cwd=ROOT, check=True)
    subprocess.run([sys.executable, "-m", "unittest", "discover", "-v"], cwd=ROOT, check=True)
    name = "weknora-independent-directory"
    subprocess.run([sys.executable, "-m", "PyInstaller", "--noconfirm", "--clean", "--onedir",
                    "--name", name, "--distpath", str(ROOT / "dist/bin"),
                    "--workpath", str(ROOT / "build"), "--specpath", str(ROOT / "build"),
                    "server.py"], cwd=ROOT, check=True)
    bundle = ROOT / "dist/independent-directory-windows-x64-0.1.0.zip"
    distribution = ROOT / "dist/bin" / name
    with zipfile.ZipFile(bundle, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.write(ROOT / "plugin.yaml", "plugin.yaml")
        for file in sorted(distribution.rglob("*")):
            if file.is_file() and file.suffix.lower() not in (".cc", ".h"):
                archive.write(file, "bin/" + file.relative_to(distribution).as_posix())
    assert bundle.stat().st_size <= 64 * 1024 * 1024
    digest = hashlib.sha256(bundle.read_bytes()).hexdigest()
    bundle.with_suffix(".zip.sha256").write_text(digest + "  " + bundle.name + "\n")
    commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
    record = {"source_commit": commit, "python": sys.version.split()[0],
              "bundle": bundle.name, "sha256": digest, "bytes": bundle.stat().st_size,
              "build_uses_main_repository": False}
    (ROOT / "evidence").mkdir(exist_ok=True)
    (ROOT / "evidence/build.json").write_text(json.dumps(record, indent=2) + "\n")
    print(json.dumps(record), flush=True)

if __name__ == "__main__":
    main()
