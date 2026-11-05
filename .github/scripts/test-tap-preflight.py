#!/usr/bin/env python3
"""Offline tap-preflight regressions. No real gh, git, or network is used."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("tap-preflight.sh")
TOKEN = "synthetic-token-never-print-or-pass-in-argv"
STUB = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import subprocess
import sys

name = Path(sys.argv[0]).name
args = sys.argv[1:]
root = Path(os.environ["FIXTURE_ROOT"])
record = {"name": name, "args": args,
          "env": {key: os.environ.get(key) for key in
                  ("HOME", "GH_CONFIG_DIR", "GIT_CONFIG_GLOBAL",
                   "GIT_CONFIG_NOSYSTEM", "GIT_TERMINAL_PROMPT")},
          "has_token": bool(os.environ.get("GH_TOKEN"))}
with (root / "calls.jsonl").open("a") as output:
    output.write(json.dumps(record) + "\n")

def phase(command, arguments):
    if command == "gh":
        if arguments[:2] == ["auth", "setup-git"]:
            return "setup"
        if arguments[:1] == ["api"]:
            return "protection" if any("/branches/" in a for a in arguments) else "metadata"
    if command == "git":
        for candidate in ("check-ref-format", "clone", "push"):
            if candidate in arguments:
                return candidate
    raise SystemExit("Unexpected command")

if name == "timeout":
    if args[:2] != ["--kill-after=2s", "15s"]:
        raise SystemExit("Unbounded timeout")
    current = phase(args[2], args[3:])
    if os.environ.get("FAIL_AT") == "timeout:" + current:
        raise SystemExit(124)
    raise SystemExit(subprocess.run(args[2:], check=False).returncode)

current = phase(name, args)
if os.environ.get("FAIL_AT") == current:
    # Simulate even an unsafe client diagnostic; the helper must suppress it.
    print(os.environ["GH_TOKEN"], file=sys.stderr)
    raise SystemExit(1)
if name == "gh":
    if current == "metadata":
        print(os.environ.get("METADATA", "main\tmain"))
    elif current == "protection":
        print(os.environ.get("UNPROTECTED", "true"))
    else:
        Path(os.environ["GH_CONFIG_DIR"]).mkdir()
        Path(os.environ["GIT_CONFIG_GLOBAL"]).write_text("# isolated helper\n")
        (Path(os.environ["GH_CONFIG_DIR"]) / "config.yml").write_text("# isolated\n")
elif current == "clone":
    Path(args[-1]).mkdir()
    (Path(args[-1]) / "clone-marker").write_text("fixture\n")
elif current == "push" and "--dry-run" not in args:
    (root / "remote-ref-write").write_text("Unsafe push\n")
    raise SystemExit(1)
"""


class TapPreflightTests(unittest.TestCase):
    def run_case(self, *, ready=False, **overrides):
        with tempfile.TemporaryDirectory(prefix="tap-preflight-test-") as temporary:
            root = Path(temporary)
            bindir = root / "bin"
            bindir.mkdir()
            runner = root / "runner"
            runner.mkdir()
            original_home = os.environ.get("HOME")
            outside_git = root / "outside-gitconfig"
            outside_git.write_text("# preserve existing configuration\n")
            outside_gh = root / "outside-gh"
            outside_gh.mkdir()
            (outside_gh / "config.yml").write_text("# preserve existing gh configuration\n")
            for name in ("gh", "git", "timeout"):
                stub = bindir / name
                stub.write_text(STUB)
                stub.chmod(0o700)
            output = root / "output"
            env = {
                "PATH": os.pathsep.join((str(bindir), str(Path(sys.executable).parent), "/usr/bin", "/bin")),
                "GH_CONFIG_DIR": str(outside_gh),
                "GIT_CONFIG_GLOBAL": str(outside_git),
                "GITHUB_ACTIONS": "true", "RUNNER_TEMP": str(runner),
                "GITHUB_OUTPUT": str(output), "GH_TOKEN": TOKEN,
                "FIXTURE_ROOT": str(root), "PYTHONDONTWRITEBYTECODE": "1",
            }
            if original_home is not None:
                env["HOME"] = original_home
            env.update(overrides)
            result = subprocess.run(["/bin/bash", str(SCRIPT)], env=env,
                                    text=True, capture_output=True, timeout=10, check=False)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, "")
            self.assertEqual(result.stderr, "")
            self.assertEqual(output.read_text(), "ready=" + str(ready).lower() + "\n")
            calls_path = root / "calls.jsonl"
            calls = [json.loads(line) for line in calls_path.read_text().splitlines()] if calls_path.exists() else []
            self.assertNotIn(TOKEN, json.dumps(calls))
            self.assertFalse((root / "remote-ref-write").exists())
            self.assertEqual(list(runner.iterdir()), [], "private fixture was not cleaned")
            self.assertEqual(outside_git.read_text(), "# preserve existing configuration\n")
            self.assertEqual((outside_gh / "config.yml").read_text(), "# preserve existing gh configuration\n")
            self.assertEqual(sorted(p.name for p in outside_gh.iterdir()), ["config.yml"])
            for call in calls:
                self.assertEqual(call["env"]["HOME"], original_home)
                for key in ("GH_CONFIG_DIR", "GIT_CONFIG_GLOBAL"):
                    self.assertTrue(Path(call["env"][key]).is_relative_to(runner))
                self.assertEqual(call["env"]["GIT_CONFIG_NOSYSTEM"], "1")
                self.assertEqual(call["env"]["GIT_TERMINAL_PROMPT"], "0")
                self.assertTrue(call["has_token"])
                if call["name"] == "git" and "push" in call["args"]:
                    self.assertIn("--dry-run", call["args"])
                    self.assertIn("--no-verify", call["args"])
            timeouts = [c for c in calls if c["name"] == "timeout"]
            clients = [c for c in calls if c["name"] != "timeout"]
            self.assertLessEqual(len(timeouts), 6)
            self.assertLess(len(timeouts) * 17, 120)
            for call in timeouts:
                self.assertEqual(call["args"][:2], ["--kill-after=2s", "15s"])
                self.assertIn(call["args"][2], ("gh", "git"))
            expected_clients = [c["args"][2:] for c in timeouts]
            for call, expected in zip(clients, expected_clients):
                self.assertEqual([call["name"], *call["args"]], expected)
            self.assertEqual(len(clients), len(timeouts) - int(overrides.get("FAIL_AT", "").startswith("timeout:")))
            return calls

    def test_empty_token_selects_manual_without_clients(self):
        self.assertEqual(self.run_case(GH_TOKEN=""), [])

    def test_non_actions_execution_does_not_configure_git(self):
        self.assertEqual(self.run_case(GITHUB_ACTIONS="false"), [])

    def test_missing_runner_directory_selects_manual(self):
        self.assertEqual(self.run_case(RUNNER_TEMP=""), [])

    def test_valid_dry_run_uses_fixed_repository_and_default_branch(self):
        calls = self.run_case(ready=True)
        git_calls = [c["args"] for c in calls if c["name"] == "git"]
        clone = next(c for c in git_calls if "clone" in c)
        self.assertEqual(clone[:-1], ["clone", "--depth", "1", "--no-checkout",
                                     "--single-branch", "--branch", "main", "--",
                                     "https://github.com/alikatgh/homebrew-tap.git"])
        push = next(c for c in git_calls if "push" in c)
        self.assertEqual(push[2:], ["-c", "core.hooksPath=/dev/null", "push", "--dry-run",
                                   "--no-verify", "origin", "HEAD:refs/heads/main"])
        setup = [c["args"] for c in calls if c["name"] == "gh" and "setup-git" in c["args"]]
        self.assertEqual(setup, [["auth", "setup-git", "--hostname", "github.com"]])

    def test_default_branch_is_encoded_for_api_and_preserved_for_git(self):
        calls = self.run_case(ready=True, METADATA="release/next\trelease%2Fnext")
        self.assertTrue(any("repos/alikatgh/homebrew-tap/branches/release%2Fnext" in c["args"] for c in calls))
        self.assertTrue(any("HEAD:refs/heads/release/next" in c["args"] for c in calls))

    def test_rejected_authentication_or_client_failure_selects_manual(self):
        for phase in ("metadata", "check-ref-format", "protection", "setup", "clone", "push"):
            with self.subTest(phase=phase):
                self.run_case(FAIL_AT=phase)

    def test_protected_or_unknown_branch_selects_manual_before_clone(self):
        for state in ("false", "null", "", "unknown", "true\nfalse"):
            with self.subTest(state=state):
                calls = self.run_case(UNPROTECTED=state)
                self.assertFalse(any("clone" in c["args"] for c in calls))

    def test_unknown_default_branch_selects_manual(self):
        for metadata in ("", "main", "main\tmain\textra", "main\tmain\nother\tother"):
            with self.subTest(metadata=metadata):
                calls = self.run_case(METADATA=metadata)
                self.assertEqual(len([c for c in calls if c["name"] == "gh"]), 1)

    def test_timeout_at_each_client_selects_manual_and_cleans_up(self):
        for phase in ("metadata", "check-ref-format", "protection", "setup", "clone", "push"):
            with self.subTest(phase=phase):
                self.run_case(FAIL_AT="timeout:" + phase)


if __name__ == "__main__":
    unittest.main()
