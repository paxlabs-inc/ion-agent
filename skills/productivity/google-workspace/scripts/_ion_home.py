"""Resolve ION_HOME for standalone skill scripts.

Skill scripts may run outside the Ion process (e.g. system Python,
nix env, CI) where ``ion_constants`` is not importable.  This module
provides the same ``get_ion_home()`` and ``display_ion_home()``
contracts as ``ion_constants`` without requiring it on ``sys.path``.

When ``ion_constants`` IS available it is used directly so that any
future enhancements (profile resolution, Docker detection, etc.) are
picked up automatically.  The fallback path replicates the core logic
from ``ion_constants.py`` using only the stdlib.

All scripts under ``google-workspace/scripts/`` should import from here
instead of duplicating the ``ION_HOME = Path(os.getenv(...))`` pattern.
"""

from __future__ import annotations

import os
from pathlib import Path

try:
    from ion_constants import display_ion_home as display_ion_home
    from ion_constants import get_ion_home as get_ion_home
except (ModuleNotFoundError, ImportError):

    def get_ion_home() -> Path:
        """Return the Ion home directory (default: ~/.ion).

        Mirrors ``ion_constants.get_ion_home()``."""
        val = os.environ.get("ION_HOME", "").strip()
        return Path(val) if val else Path.home() / ".ion"

    def display_ion_home() -> str:
        """Return a user-friendly ``~/``-shortened display string.

        Mirrors ``ion_constants.display_ion_home()``."""
        home = get_ion_home()
        try:
            return "~/" + str(home.relative_to(Path.home()))
        except ValueError:
            return str(home)
