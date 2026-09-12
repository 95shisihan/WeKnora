import json
from pathlib import Path
import tempfile
import unittest
import datasource_pb2 as pb
from server import Datasource, config_root

class Context:
    def abort(self, code, message):
        raise ValueError(message)

class PluginTests(unittest.TestCase):
    def test_full_changed_unchanged_and_deletion(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "alpha.txt").write_text("alpha v1")
            (root / "beta.txt").write_text("beta")
            config = json.dumps({"settings": {"root": directory}}).encode()
            plugin = Datasource()
            def fetch(cursor=b"", full=False):
                events = list(plugin.Fetch(pb.FetchRequest(config_json=config, cursor_json=cursor,
                                                            full=full), Context()))
                return [x.item for x in events if x.HasField("item")], events[-1].final_cursor_json
            first, cursor = fetch()
            self.assertEqual({x.external_id for x in first}, {"alpha.txt", "beta.txt"})
            unchanged, _ = fetch(cursor)
            self.assertEqual(unchanged, [])
            (root / "alpha.txt").write_text("alpha v2")
            changed, cursor = fetch(cursor)
            self.assertEqual([x.external_id for x in changed], ["alpha.txt"])
            (root / "beta.txt").unlink()
            deleted, cursor = fetch(cursor)
            self.assertEqual([(x.external_id, x.is_deleted) for x in deleted], [("beta.txt", True)])
            full, _ = fetch(cursor, True)
            self.assertEqual(len(full), 1)

    def test_relative_root_rejected(self):
        with self.assertRaises(ValueError):
            config_root(b'{"settings":{"root":"relative"}}')

    def test_identity(self):
        info = Datasource().GetInfo(pb.GetInfoRequest(), Context())
        self.assertEqual(info.id, "dev.example.independent-directory")
        self.assertEqual(info.connector_type, "independent_directory")

if __name__ == "__main__":
    unittest.main()
