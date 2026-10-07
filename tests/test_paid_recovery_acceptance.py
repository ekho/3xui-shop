"""Local acceptance boundaries; never contacts a real payment provider."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from deploy.acceptance import local


class PaidRecoveryAcceptanceTests(unittest.TestCase):
    def test_prepare_resumed_state_creates_private_fixture_secret(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            env = state / 'public.env'
            env.write_text('LOCAL_STATE_DIR=' + directory + '\n')
            with patch.multiple(local, STATE=state, ENV=env, PROFILE='native'):
                local.prepare()
                secret = state / 'yoomoney-notification-secret'
                self.assertTrue(secret.is_file(), 'resuming a prepared stack must provision its new fixture')
                original = secret.read_bytes()
                self.assertGreaterEqual(len(original), 32)
                self.assertEqual(secret.stat().st_mode & 0o777, 0o600)
                local.prepare()
                self.assertEqual(secret.read_bytes(), original, 'resume must not rotate a receipt signing key')

    def test_fixture_signature_matches_existing_provider_contract(self):
        fields = {'amount': '98.00', 'codepro': 'false', 'currency': '643',
                  'datetime': '2013-12-26T08:28:34Z', 'label': 'ML23045',
                  'notification_type': 'p2p-incoming', 'operation_id': '441361714955017004',
                  'sender': '41000000000', 'sha1_hash': 'ac13833bd6ba9eff1fa9e4bed76f3d6ebb57f6c0',
                  'unaccepted': 'false', 'withdraw_amount': '100.00'}
        self.assertEqual(local.yoomoney_signature(fields, 'secret123'),
                         'a452af731650e2c5b39abcdc7c28dd27db7b3b654c2230ad2c386e64afb98605')

    def test_native_fixture_is_opt_in_and_file_backed(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            env = state / 'public.env'
            with patch.multiple(local, STATE=state, ENV=env, PROFILE='native'):
                local.prepare()
                args = ['docker', 'compose', '--project-name', 'paid-recovery-fixture', '--env-file', str(env)]
                for name in ('acceptance', 'local', 'native'):
                    args += ['-f', 'deploy/acceptance/compose.' + name + '.yml']
                args += ['config', '--format', 'json']
                for enabled in ('false', 'true'):
                    with self.subTest(enabled=enabled):
                        process_env = {**os.environ, 'LOCAL_YOOMONEY_FIXTURE_ENABLED': enabled}
                        result = subprocess.run(args, cwd=local.ROOT, env=process_env, capture_output=True, text=True, timeout=30)
                        self.assertEqual(result.returncode, 0, 'owned Compose config must resolve')
                        config = json.loads(result.stdout)
                        for service in ('backend', 'migrate'):
                            settings = config['services'][service]['environment']
                            self.assertEqual(settings.get('SHOP_PAYMENT_YOOMONEY_ENABLED'), enabled)
                            self.assertEqual(settings.get('YOOMONEY_WALLET_ID'), '410000000000000')
                            self.assertEqual(settings.get('YOOMONEY_NOTIFICATION_SECRET_FILE'), '/run/secrets/local_yoomoney_notification')
                            self.assertIn('local_yoomoney_notification', [s['source'] for s in config['services'][service]['secrets']])
                        self.assertEqual(config['secrets']['local_yoomoney_notification']['file'], str(state / 'yoomoney-notification-secret'))


if __name__ == '__main__':
    unittest.main()
