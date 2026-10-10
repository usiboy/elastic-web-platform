import stat
import tarfile
import tempfile
import unittest
from pathlib import Path

from package_static import package


class TrustedStaticArchiveTest(unittest.TestCase):
    def test_identical_ci_bytes_produce_private_deterministic_archive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "dist"
            (root / "assets").mkdir(parents=True)
            (root / "index.html").write_text("<h1>fixture</h1>")
            (root / "assets" / "index.js").write_text("console.log('fixture')")
            first = Path(directory) / "first.tar.gz"
            second = Path(directory) / "second.tar.gz"
            package(root, first)
            package(root, second)
            self.assertEqual(first.read_bytes(), second.read_bytes())
            self.assertEqual(stat.S_IMODE(first.stat().st_mode), 0o600)
            with tarfile.open(first, "r:gz") as archive:
                self.assertEqual(archive.getnames(), ["assets/index.js", "index.html"])
            with self.assertRaises(FileExistsError):
                package(root, first)

    def test_symlink_is_not_packaged(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "dist"
            root.mkdir()
            (root / "index.html").write_text("fixture")
            (root / "secret.key").symlink_to(Path(directory) / "secret.key")
            with self.assertRaisesRegex(ValueError, "非常规"):
                package(root, Path(directory) / "rejected.tar.gz")
            self.assertFalse((Path(directory) / "rejected.tar.gz").exists())


if __name__ == "__main__":
    unittest.main()
