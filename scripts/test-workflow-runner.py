"""Contract tests for CLI node adapters; no external command or connection is made."""
import base64
import json
import pathlib
import subprocess
import tempfile
import unittest
from unittest.mock import patch

RUNNER = pathlib.Path(__file__).resolve().parents[1] / "internal/web/ui/assets/workflow-runner.py"

class RunnerContracts(unittest.TestCase):
    def run_node(self, kind, config):
        calls = []
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "result.json"
            config = {"directory": str(pathlib.Path(directory) / "checkout"), **config}
            spec = {"kind": kind, "inputs": {}, "config": config, "output": str(output)}
            source = RUNNER.read_text(encoding="utf-8").replace("__SPEC__", base64.b64encode(json.dumps(spec).encode()).decode())
            def execute(argv, **kwargs):
                calls.append(argv)
                stdout = json.dumps([{"Id": "sha256:test", "RepoDigests": ["registry/repo@sha256:test"]}]) if "inspect" in argv else "actual-commit"
                return subprocess.CompletedProcess(argv, 0, stdout=stdout)
            with patch("subprocess.run", side_effect=execute):
                exec(compile(source, str(RUNNER), "exec"), {"__name__": "__main__"})
            return calls, json.loads(output.read_text(encoding="utf-8"))

    def test_git_preserves_transport_and_explicit_verification_choice(self):
        for url in ["http://git.example/repo", "https://git.example/repo", "ssh://git.example/repo"]:
            for skip in [False, True]:
                with self.subTest(url=url, skip=skip):
                    calls, result = self.run_node("git", {"repository": url, "branch": "dev", "skipTLSVerify": skip})
                    self.assertIn(url, calls[0])
                    self.assertEqual("http.sslVerify=false" in calls[0], skip)
                    self.assertEqual(result["sourceCommit"], "actual-commit")

    def test_kubernetes_plain_tls_and_skip_are_not_rewritten(self):
        for server, skip in [("http://cluster.example", False), ("https://cluster.example", False), ("https://cluster.example", True)]:
            calls, _ = self.run_node("k3s", {"server": server, "skipTLSVerify": skip, "kubeconfig": "test-config", "manifest": "manifest.yaml"})
            self.assertIn(server, calls[0])
            self.assertEqual("--insecure-skip-tls-verify=true" in calls[0], skip)
            self.assertIn("test-config", calls[0])

    def test_docker_connection_and_credential_references_are_preserved(self):
        for context in ["plain-registry", "verified-tls", "explicit-insecure-registry"]:
            calls, result = self.run_node("image_push", {"image": "registry/repo:version", "dockerContext": context, "dockerConfig": "credentials-directory"})
            self.assertEqual(calls[0][:5], ["docker", "--context", context, "--config", "credentials-directory"])
            self.assertEqual(result["digest"], "registry/repo@sha256:test")

if __name__ == "__main__":
    unittest.main()
