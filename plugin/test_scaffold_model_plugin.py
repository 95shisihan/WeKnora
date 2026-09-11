"""Regression tests for standalone generation, including protobuf integrity."""
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class ScaffoldTests(unittest.TestCase):
    def test_preserves_descriptors_and_refuses_overwrite(self):
        root = Path(__file__).resolve().parent.parent
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary) / "independent"
            command = [sys.executable, str(root / "plugin/scaffold_model_plugin.py"), str(target), "--module", "example.org/independent"]
            subprocess.run(command, check=True, capture_output=True)
            for name in ("model_provider.pb.go", "model_provider_grpc.pb.go"):
                self.assertEqual((root / "plugin/proto" / name).read_text(encoding="utf-8"), (target / "proto" / name).read_text(encoding="utf-8"))
            self.assertIn('"example.org/independent/sdk/model"', (target / "main.go").read_text(encoding="utf-8"))
            module = (target / "go.mod").read_text(encoding="utf-8")
            self.assertNotIn("github.com/Tencent/WeKnora", module)
            self.assertNotIn("replace", module)
            marker = target / "user-work.txt"
            marker.write_text("preserve", encoding="utf-8")
            result = subprocess.run(command, capture_output=True)
            self.assertNotEqual(0, result.returncode)
            self.assertEqual("preserve", marker.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
