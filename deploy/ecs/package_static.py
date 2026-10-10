"""将已核验的静态 CI 产物打包为操作员可信 fixture 输入。只接受常规文件。"""

import argparse
import gzip
import hashlib
import os
import stat
import tarfile
import tempfile
from pathlib import Path


def package(root: Path, output: Path) -> None:
    root = root.resolve(strict=True)
    parent = output.parent.resolve(strict=True)
    if stat.S_IMODE(parent.stat().st_mode) & 0o077:
        raise ValueError("输出目录必须为私有目录（0700）")
    if output.exists() or output.is_symlink():
        raise FileExistsError(output)

    files = []
    for entry in sorted(root.rglob("*")):
        mode = entry.lstat().st_mode
        if stat.S_ISDIR(mode):
            continue
        if not stat.S_ISREG(mode):
            raise ValueError("静态目录中包含非常规文件")
        files.append(entry)
    if not files or len(files) > 256:
        raise ValueError("静态文件数量超出范围")
    total = 0
    for file in files:
        info = file.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_size > 4 << 20:
            raise ValueError("只允许大小受限的常规文件")
        total += info.st_size
    if total > 16 << 20 or not (root / "index.html").is_file():
        raise ValueError("缺少首页或静态字节超限")

    fd, name = tempfile.mkstemp(prefix=".fixture-", suffix=".tar.gz", dir=parent)
    try:
        with os.fdopen(fd, "wb") as destination:
            with gzip.GzipFile(fileobj=destination, mode="wb", filename="", mtime=0) as zipped:
                with tarfile.open(fileobj=zipped, mode="w") as archive:
                    for file in files:
                        entry = tarfile.TarInfo(file.relative_to(root).as_posix())
                        entry.size = file.stat().st_size
                        entry.mode = 0o644
                        entry.mtime = 0
                        with file.open("rb") as content:
                            archive.addfile(entry, content)
        os.link(name, output)
    finally:
        os.unlink(name)
    digest = hashlib.sha256(output.read_bytes()).hexdigest()
    print(f"archive={output} sha256={digest} files={len(files)}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    package(args.root, args.output)
