import importlib.util
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('configuration_smoke', ROOT / 'deploy/acceptance/smoke.py')
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class SmokeConfigurationTests(unittest.TestCase):
    def test_parent_deployment_inputs_cannot_enable_external_channels(self):
        with tempfile.TemporaryDirectory() as directory:
            envfile = Path(directory) / 'owned.env'
            envfile.write_text('TELEGRAM_ENABLED=false\nBOT_TOKEN_FILE=\nSUPPORT_TELEGRAM_ENABLED=false\n'
                               'SUPPORT_BOT_TOKEN_FILE=\nAUDIT_MIRROR_ENABLED=false\nOPERATIONS_EMAIL_FILE=\n')
            parent = {'TELEGRAM_ENABLED': 'true', 'BOT_TOKEN_FILE': '/outside/token', 'SUPPORT_TELEGRAM_ENABLED': 'true',
                      'SUPPORT_BOT_TOKEN_FILE': '/outside/support', 'AUDIT_MIRROR_ENABLED': 'true',
                      'OPERATIONS_EMAIL_FILE': '/outside/recipient', 'SUPPORT_GROUP_ID': '-100000',
                      'COMPOSE_ENV_FILES': '/outside/env', 'COMPOSE_PROFILES': 'legacy', 'DOCKER_HOST': 'fixture-docker', 'PATH': '/fixture/bin'}
            args = ['docker', 'compose', '--env-file', str(envfile), 'config', '--quiet']
            with patch.dict(os.environ, parent, clear=True):
                env = smoke.compose_environment(args)
                for name in ('TELEGRAM_ENABLED', 'SUPPORT_TELEGRAM_ENABLED', 'AUDIT_MIRROR_ENABLED'):
                    self.assertEqual(env[name], 'false')
                for name in ('BOT_TOKEN_FILE', 'SUPPORT_BOT_TOKEN_FILE', 'OPERATIONS_EMAIL_FILE'):
                    self.assertEqual(env[name], '')
                for name in ('SUPPORT_GROUP_ID', 'COMPOSE_ENV_FILES', 'COMPOSE_PROFILES'):
                    self.assertNotIn(name, env)
                self.assertEqual(env['DOCKER_HOST'], parent['DOCKER_HOST'])
                self.assertEqual(env['PATH'], parent['PATH'])
                with patch.object(smoke.subprocess, 'run') as run:
                    run.return_value.returncode = 0
                    run.return_value.stdout = b''
                    smoke.command(args)
                    self.assertEqual(run.call_args.kwargs['env'], env)


if __name__ == '__main__':
    unittest.main()
