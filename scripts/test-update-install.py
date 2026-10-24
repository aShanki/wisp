#!/usr/bin/env python3
"""Exercise the real installer in private directories, without desktop services."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

REPO = Path(__file__).resolve().parents[1]
SCRATCH = REPO / "build" / "update-tests"
SCRATCH.mkdir(parents=True, exist_ok=True)


class InstallerTest(unittest.TestCase):
    def exercise(self, platform, changed, fail=False, launch_agent=False):
        with tempfile.TemporaryDirectory(dir=SCRATCH) as work:
            work = Path(work)
            commands = work / "commands"
            commands.mkdir()
            events = work / "events"
            stage = work / "stage"
            stage.mkdir()
            target = work / ("Wisp.app" if platform == "Darwin" else "bin")
            old_bin = target / "Contents/MacOS" if platform == "Darwin" else target
            new_bin = stage / "new/Wisp.app/Contents/MacOS" if platform == "Darwin" else stage / "new"
            old_bin.mkdir(parents=True)
            new_bin.mkdir(parents=True)
            for name in ("wisp", "wispd", "wispctl"):
                for folder, marker in ((old_bin, "old"), (new_bin, "new")):
                    file = folder / name
                    file.write_text(f"#!/bin/sh\n# {marker}\nexit 0\n")
                    file.chmod(0o755)
            if fail:
                if platform == "Darwin":
                    (stage / "new/Wisp.app").rename(stage / "missing")
                else:
                    (new_bin / "wispd").unlink()  # Fail after the UI was replaced.
            for name, body in {
                "uname": f"echo {platform}",
                "systemctl": 'echo systemctl "$@" >> "$EVENTS"',
                "open": 'echo open "$@" >> "$EVENTS"',
                "xattr": 'echo xattr "$@" >> "$EVENTS"',
                "launchctl": ('if [ "$1" = print ]; then echo "$APP/Contents/MacOS/wisp"; '
                              'else echo launchctl "$@" >> "$EVENTS"; fi') if launch_agent else "exit 1",
            }.items():
                file = commands / name
                file.write_text("#!/bin/sh\n" + body + "\n")
                file.chmod(0o755)
            env = dict(os.environ, PATH=str(commands) + ":/usr/bin:/bin", EVENTS=str(events), APP=str(target))
            result = subprocess.run(["/bin/sh", str(REPO / "scripts/install-update.sh"), str(stage),
                                     str(target), str(old_bin / "wisp"), "99999999", str(int(changed)), "0"],
                                    env=env, timeout=10, capture_output=True)
            self.assertEqual(result.returncode, 1 if fail else 0, result.stderr)
            for name in ("wisp", "wispd", "wispctl"):
                self.assertIn("# old" if fail else "# new", (old_bin / name).read_text())
            log = events.read_text() if events.exists() else ""
            if platform == "Linux":
                self.assertEqual("systemctl" in log, changed)
            elif launch_agent:
                self.assertIn("launchctl kickstart", log)
                self.assertNotIn("open ", log)
            else:
                self.assertIn("open ", log)
            self.assertEqual(stage.exists(), fail)

    def test_linux_replacement(self):
        self.exercise("Linux", True)

    def test_linux_unchanged_daemon(self):
        self.exercise("Linux", False)

    def test_linux_partial_rollback(self):
        self.exercise("Linux", True, fail=True)

    def test_mac_bundle_replacement(self):
        self.exercise("Darwin", True)

    def test_mac_launch_agent(self):
        self.exercise("Darwin", False, launch_agent=True)

    def test_mac_rollback(self):
        self.exercise("Darwin", True, fail=True)


if __name__ == "__main__":
    unittest.main()
