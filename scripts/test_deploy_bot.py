"""Exercise the deployment shell script without a Docker daemon or server."""

import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts/deploy-bot.sh"
COMPOSE = ["compose", "--env-file", ".env", "--env-file", "deploy.env"]

FAKE_DOCKER = r'''
import json, os, pathlib, sys
path = pathlib.Path(os.environ["FAKE_DOCKER_STATE"])
state = json.loads(path.read_text())
args = sys.argv[1:]
state["calls"].append(args)
path.write_text(json.dumps(state))
compose = ["compose", "--env-file", ".env", "--env-file", "deploy.env"]
redis = state["redis"]
if args == compose + ["ps", "--all", "--quiet", "redis"]:
    if state.get("ps_error"):
        sys.exit(30)
    if redis.get("exists", True):
        print(redis["id"])
        if redis.get("multiple"):
            print("redis-extra")
elif len(args) == 6 and args[:4] == ["inspect", "--type", "container", "--format"]:
    if state.get("inspect_error") or args[5] != redis["id"]:
        sys.exit(31)
    template = args[4]
    values = {
        "{{.Id}}": redis["id"],
        "{{.Image}}": redis["image"],
        "{{.State.Running}}": redis["running"],
        "{{.State.Status}}": redis["status"],
        "{{.State.StartedAt}}": redis["started"],
        "{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}": redis["health"],
        "{{.RestartCount}}": str(redis["restart_count"]),
        "{{json .HostConfig.RestartPolicy}}": json.dumps(redis["restart_policy"], sort_keys=True),
        r'{{range .Mounts}}{{printf "\n"}}mount:{{json .}}{{end}}': "".join("\nmount:" + json.dumps(m, sort_keys=True) for m in redis["mounts"]),
        "{{json .NetworkSettings.Networks}}": json.dumps(redis["networks"], sort_keys=True),
    }
    for expression, value in values.items():
        template = template.replace(expression, value)
    assert "{{" not in template, "Unsupported inspect template"
    print(template)
elif args in (compose + ["pull", "bot"], compose + ["up", "-d", "--no-deps", "--no-build", "--pull", "never", "bot"]):
    action = args[len(compose)]
    redis.update(state.get("after_" + action, {}))
    path.write_text(json.dumps(state))
    sys.exit(state.get(action + "_exit", 0))
else:
    print("Unexpected or unsafe Docker command: " + repr(args), file=sys.stderr)
    sys.exit(99)
'''


class DeployBotTests(unittest.TestCase):
    def run_deploy(self, *, redis=None, mode=None, **options):
        state = {
            "calls": [],
            "redis": {
                "id": "redis-original",
                "image": "sha256:original",
                "running": "true",
                "status": "running",
                "started": "2026-09-28T15:13:19Z",
                "health": "healthy",
                "restart_count": 0,
                "restart_policy": {"Name": "unless-stopped"},
                "mounts": [{"Source": "/srv/giraffe/redis_data", "Destination": "/data"}],
                "networks": {"giraffe_app-network": {"NetworkID": "network-original"}},
            },
            **options,
        }
        state["redis"].update(redis or {})
        with tempfile.TemporaryDirectory(prefix="giraffe-deploy-test-") as directory:
            directory = Path(directory)
            state_path = directory / "state.json"
            state_path.write_text(json.dumps(state))
            fake = directory / "fake_docker.py"
            fake.write_text(FAKE_DOCKER)
            wrapper = directory / "docker"
            wrapper.write_text(
                "#!/bin/sh\nexec " + shlex.quote(sys.executable) + " "
                + shlex.quote(str(fake)) + ' "$@"\n'
            )
            wrapper.chmod(0o700)
            env = dict(os.environ, PATH=str(directory) + os.pathsep + os.environ["PATH"],
                       FAKE_DOCKER_STATE=str(state_path))
            # Workflows pipe this script to bash over SSH, so test the same stdin mode.
            command = ["bash", "-s", "--"] + ([mode] if mode else [])
            result = subprocess.run(command, input=SCRIPT.read_text(), cwd=directory,
                                    env=env, capture_output=True, text=True, timeout=10)
            calls = json.loads(state_path.read_text())["calls"]
        self.assertNotIn("Unexpected or unsafe Docker command", result.stderr)
        return result, calls

    @staticmethod
    def mutations(calls):
        return [args[len(COMPOSE):] for args in calls
                if args[:len(COMPOSE)] == COMPOSE and args[len(COMPOSE)] != "ps"]

    def test_deploy_only_pulls_and_starts_bot(self):
        result, calls = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.mutations(calls), [
            ["pull", "bot"],
            ["up", "-d", "--no-deps", "--no-build", "--pull", "never", "bot"],
        ])
        self.assertEqual(sum(args[0] == "inspect" for args in calls), 3)
        self.assertIn("Redis is healthy and unchanged", result.stdout)

    def test_preflight_is_read_only(self):
        result, calls = self.run_deploy(mode="--check-redis")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.mutations(calls), [])
        self.assertEqual(sum(args[0] == "inspect" for args in calls), 1)

    def test_unready_redis_refuses_preflight_and_deployment(self):
        for mode in (None, "--check-redis"):
            for redis in (
                {"exists": False}, {"multiple": True},
                {"running": "false", "status": "exited"},
                {"status": "paused"}, {"status": "restarting"},
                {"health": "starting"}, {"health": "unhealthy"}, {"health": "missing"},
            ):
                with self.subTest(mode=mode, redis=redis):
                    result, calls = self.run_deploy(redis=redis, mode=mode)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(self.mutations(calls), [])
                    self.assertRegex(result.stderr, "Redis|bootstrap")

    def test_inspection_failure_refuses_deployment(self):
        for option in ("ps_error", "inspect_error"):
            with self.subTest(option=option):
                result, calls = self.run_deploy(**{option: True})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.mutations(calls), [])

    def test_redis_changes_are_detected_before_and_after_up(self):
        for phase in ("pull", "up"):
            for change in (
                {"id": "redis-recreated"}, {"image": "sha256:changed"},
                {"started": "2026-10-30T20:00:00Z"}, {"restart_count": 1},
                {"running": "false", "status": "exited"},
                {"health": "unhealthy"}, {"exists": False},
                {"mounts": []}, {"networks": {}}, {"restart_policy": {"Name": "no"}},
            ):
                with self.subTest(phase=phase, change=change):
                    result, calls = self.run_deploy(**{"after_" + phase: change})
                    self.assertNotEqual(result.returncode, 0)
                    if phase == "pull":
                        self.assertEqual(self.mutations(calls), [["pull", "bot"]])
                    self.assertNotIn("deployment completed", result.stdout)
                    self.assertIn("Redis", result.stderr)

    def test_pull_failure_keeps_exit_code_and_checks_redis(self):
        result, calls = self.run_deploy(pull_exit=17)
        self.assertEqual(result.returncode, 17, result.stderr)
        self.assertEqual(self.mutations(calls), [["pull", "bot"]])
        self.assertEqual(sum(args[0] == "inspect" for args in calls), 2)

    def test_mount_order_does_not_trigger_false_alarm(self):
        mounts = [{"Source": "/data", "Destination": "/data"},
                  {"Source": "/redis.conf", "Destination": "/etc/redis.conf"}]
        result, _ = self.run_deploy(redis={"mounts": mounts}, after_pull={"mounts": mounts[::-1]})
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_up_failure_keeps_exit_code_and_checks_redis(self):
        result, calls = self.run_deploy(up_exit=18)
        self.assertEqual(result.returncode, 18, result.stderr)
        self.assertEqual(sum(args[0] == "inspect" for args in calls), 3)
        self.assertNotIn("deployment completed", result.stdout)

    def test_unknown_mode_does_nothing(self):
        result, calls = self.run_deploy(mode="bootstrap")
        self.assertEqual(result.returncode, 2)
        self.assertEqual(calls, [])

    def test_workflows_use_guarded_script_and_preflight_before_shipping(self):
        for filename in ("deploy.yml", "manual-deploy.yml"):
            with self.subTest(workflow=filename):
                workflow = (ROOT / ".github/workflows" / filename).read_text()
                self.assertIn("python3 -m unittest discover -s scripts -p 'test_deploy_bot.py' -v", workflow)
                self.assertIn("bash -s -- --check-redis' < scripts/deploy-bot.sh", workflow)
                self.assertEqual(workflow.count("< scripts/deploy-bot.sh"), 2)
                self.assertLess(workflow.index("--check-redis"), workflow.index("rsync "))
                self.assertLess(workflow.index("rsync "), workflow.rindex("< scripts/deploy-bot.sh"))
                self.assertNotRegex(workflow, r"\bdocker(?:-|\s+)compose\s")
                self.assertNotIn("--remove-orphans", workflow)
                self.assertNotRegex(workflow, r"\bdocker\s+(?:container\s+)?(?:stop|restart|rm)\b")


if __name__ == "__main__":
    unittest.main()
