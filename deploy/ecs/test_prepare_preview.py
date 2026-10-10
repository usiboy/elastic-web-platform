import os
import stat
import tempfile
import unittest
from pathlib import Path

from prepare_preview import private_file


class PreviewPreparationTest(unittest.TestCase):
    def test_private_file_is_exclusive_and_owner_only(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'credential'
            private_file(path, 'fixture-only\n')
            self.assertEqual(path.read_text(), 'fixture-only\n')
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            with self.assertRaises(FileExistsError):
                private_file(path, 'replacement')
            self.assertEqual(path.read_text(), 'fixture-only\n')


if __name__ == '__main__':
    unittest.main()
