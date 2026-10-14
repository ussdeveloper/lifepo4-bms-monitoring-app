"""Make the command line tool importable from the tests.

The tests import ``humsienk_cli`` directly, which is the script in this
directory. Adding the directory to ``sys.path`` keeps
``python -m pytest cli/tests -q`` working from the repository root.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
