#!/usr/bin/env python3
"""Clean only allowlisted local outputs. Stop builds/apps first; never follows links."""
import os
from pathlib import Path
import shutil
import subprocess

ROOT = Path(__file__).resolve().parents[1]
OUTPUTS = ('dist', 'target', 'artifacts', '.cache/go-build')


def redirected(path):
    return path.is_symlink() or getattr(path, 'is_junction', lambda: False)()


def clean(root=ROOT, *, dry_run=False):
    root = Path(root).resolve()
    protected = [root / '.cache/signed-dev-data']
    for name in ('DATA_DIR', 'MANAGER_DATA_DIR'):
        if os.environ.get(name):
            protected.append(Path(os.environ[name]).expanduser().resolve())
    tracked = subprocess.check_output(
        ['git', '-C', str(root), 'ls-files', '-z'], text=True).split('\0')
    selected = []
    for relative in OUTPUTS:
        path = root / relative
        if any(path == data or path in data.parents or data in path.parents
               for data in protected):
            print(f'Preserving data path: {relative}')
            continue
        # Check ancestors too, including links to missing destinations.
        for component in (path, *path.parents):
            if component == root:
                break
            if redirected(component):
                raise ValueError(f'Refusing redirected output: {relative}')
        if not path.exists():
            continue
        if any(name == relative or name.startswith(relative + '/') for name in tracked):
            raise ValueError(f'Refusing tracked output: {relative}')
        # Fail before any deletion if a nested checkout is present.
        # Generated links are entries to unlink, never trees to inspect/follow.
        if path.is_dir():
            for directory, dirs, files in os.walk(path, followlinks=False):
                if '.git' in dirs or '.git' in files:
                    raise ValueError(f'Refusing nested repository: {relative}')
                dirs[:] = [name for name in dirs
                           if not redirected(Path(directory) / name)]
        selected.append(path)
    for path in selected:
        print(f'{"Would remove" if dry_run else "Removing"}: {path.relative_to(root)}')
        if not dry_run:
            if redirected(path):
                raise ValueError('Output changed during cleanup; stop concurrent writers')
            if path.is_dir():
                shutil.rmtree(path)
            else:
                path.unlink()


if __name__ == '__main__':
    clean(dry_run=os.environ.get('CLEAN_DRY_RUN') == '1')
