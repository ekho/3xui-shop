import importlib.util
import os
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name('configure_docker_cache.py')


class DockerCacheTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(SCRIPT.exists(), 'CI Docker cache configurator is missing')
        spec = importlib.util.spec_from_file_location('docker_cache', SCRIPT)
        self.cache = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.cache)

    def test_preserves_daemon_settings_and_existing_mirrors(self):
        original = {'debug': False, 'features': {'containerd-snapshotter': True},
                    'registry-mirrors': ['https://example.test']}
        result = self.cache.add_cache(original)
        self.assertEqual(result, {'debug': False, 'features': {'containerd-snapshotter': True},
                                 'registry-mirrors': ['https://mirror.gcr.io', 'https://example.test']})
        self.assertEqual(original['registry-mirrors'], ['https://example.test'])
        self.assertEqual(self.cache.add_cache(result), result)

    def test_rejects_invalid_daemon_settings(self):
        for config in ([], None, {'registry-mirrors': 'https://example.test'},
                       {'registry-mirrors': [None]}):
            with self.subTest(config=config), self.assertRaises(ValueError):
                self.cache.add_cache(config)

    def test_refuses_local_and_self_hosted_daemons_before_any_command(self):
        for platform, environment in [('darwin', {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted'}),
                                      ('linux', {}),
                                      ('linux', {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'self-hosted'})]:
            with self.subTest(platform=platform, environment=environment), \
                 patch.object(self.cache.sys, 'platform', platform), \
                 patch.dict(os.environ, environment, clear=True), \
                 patch.object(self.cache.subprocess, 'run') as command:
                with self.assertRaises(RuntimeError):
                    self.cache.main()
                command.assert_not_called()

    def test_refuses_restart_when_job_containers_exist(self):
        with patch.object(self.cache.sys, 'platform', 'linux'), \
             patch.dict(os.environ, {'GITHUB_ACTIONS': 'true', 'RUNNER_ENVIRONMENT': 'github-hosted'}, clear=True), \
             patch.object(self.cache.subprocess, 'run', return_value=SimpleNamespace(stdout='existing-container\n')):
            with self.assertRaises(RuntimeError):
                self.cache.main()


if __name__ == '__main__':
    unittest.main()
