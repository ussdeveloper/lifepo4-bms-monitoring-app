"""Copy the desktop interface into the device filesystem image.

The ESP32 serves the very same HTML, CSS and JavaScript as the desktop
application, so the two can never drift apart: data/ is rebuilt from ../../ui
before every build, and the little ESP32 only files (the access point setup
page) are kept in data-esp/ and copied over the result.
"""

import os
import shutil

Import("env")  # noqa: F821 - provided by PlatformIO

PROJECT = env["PROJECT_DIR"]  # noqa: F821
REPOSITORY = os.path.abspath(os.path.join(PROJECT, "..", ".."))
SOURCE = os.path.join(REPOSITORY, "ui")
EXTRA = os.path.join(PROJECT, "data-esp")
TARGET = os.path.join(PROJECT, "data")

# Everything the interface needs; anything else in ui/ is not served.
FILES = ("index.html", "app.js", "style.css")


def sync_ui(source, target, env):  # noqa: ARG001 - PlatformIO signature
    os.makedirs(TARGET, exist_ok=True)
    for name in FILES:
        origin = os.path.join(SOURCE, name)
        if not os.path.exists(origin):
            print("sync_ui: %s is missing" % origin)
            continue
        shutil.copyfile(origin, os.path.join(TARGET, name))
        print("sync_ui: %s -> data/%s" % (name, name))
    if os.path.isdir(EXTRA):
        for name in sorted(os.listdir(EXTRA)):
            shutil.copyfile(os.path.join(EXTRA, name), os.path.join(TARGET, name))
            print("sync_ui: data-esp/%s" % name)


sync_ui(None, None, env)
env.AddPreAction("buildfs", sync_ui)  # noqa: F821
env.AddPreAction("uploadfs", sync_ui)  # noqa: F821
