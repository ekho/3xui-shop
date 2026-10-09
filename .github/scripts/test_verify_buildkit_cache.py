import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name('verify_buildkit_cache.py')


def trace(url='https://mirror.gcr.io/v2/library/node/manifests/sha256:abc?ns=docker.io&token=fake-secret',
          host='mirror.gcr.io', method='HEAD', status=200, operation='remotes.docker.resolver.HTTPRequest'):
    return {'data': [{'spans': [{'operationName': operation, 'tags': [
        {'key': 'url.full', 'value': url}, {'key': 'server.address', 'value': host},
        {'key': 'http.request.method', 'value': method}, {'key': 'http.response.status_code', 'value': status},
    ]}]}]}


class BuildKitCacheTests(unittest.TestCase):
    def setUp(self):
        self.assertTrue(SCRIPT.exists(), 'BuildKit cache verifier is missing')
        spec = importlib.util.spec_from_file_location('buildkit_cache', SCRIPT)
        self.cache = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.cache)

    def test_accepts_only_successful_https_mirror_manifest_resolution(self):
        self.assertEqual(self.cache.mirror_manifest_requests(trace()), 1)
        self.assertEqual(self.cache.mirror_manifest_requests(trace(url='https://mirror.gcr.io:443/v2/library/node/manifests/v1')), 1)
        invalid = [
            {'url': 'http://mirror.gcr.io/v2/library/node/manifests/v1'},
            {'url': 'https://mirror.gcr.io.evil.test/v2/library/node/manifests/v1'},
            {'url': 'https://user:fake-secret@mirror.gcr.io/v2/library/node/manifests/v1'},
            {'url': 'https://mirror.gcr.io:444/v2/library/node/manifests/v1'},
            {'url': 'https://mirror.gcr.io:fake-secret/v2/library/node/manifests/v1'},
            {'url': 'https://mirror.gcr.io/v2/library/node/blobs/sha256:abc'},
            {'host': 'registry-1.docker.io'}, {'method': 'GET'}, {'status': 401},
            {'operation': 'unrelated.HTTPRequest'},
        ]
        for values in invalid:
            with self.subTest(values=values):
                self.assertEqual(self.cache.mirror_manifest_requests(trace(**values)), 0)

    def test_absent_http_fields_are_not_evidence(self):
        self.assertEqual(self.cache.mirror_manifest_requests({'data': []}), 0)
        self.assertEqual(self.cache.mirror_manifest_requests({'data': [{'spans': []}]}), 0)
        value = trace()
        value['data'][0]['spans'][0]['tags'] = []
        self.assertEqual(self.cache.mirror_manifest_requests(value), 0)

    def test_searches_completed_records_without_exposing_trace(self):
        outputs = [
            '[registry."docker.io"]\nmirrors = ["mirror.gcr.io"]\n',
            'warmrecord\ncoldrecord\n',
            json.dumps({'data': []}), json.dumps(trace()),
        ]
        stdout = io.StringIO()
        with patch.dict(os.environ, {'BUILDX_BUILDER': 'ci-test'}), \
             patch.object(self.cache.subprocess, 'run', side_effect=[SimpleNamespace(stdout=value) for value in outputs]) as command, \
             contextlib.redirect_stdout(stdout):
            self.cache.main()
        self.assertNotIn('fake-secret', stdout.getvalue())
        self.assertNotIn('url.full', stdout.getvalue())
        self.assertNotIn('https://', stdout.getvalue())
        self.assertIn('manifest requests verified: 1', stdout.getvalue())
        self.assertEqual(command.call_args_list[1].args[0], ['docker', 'buildx', 'history', 'ls', '--builder', 'ci-test', '--filter', 'status=completed', '--format', '{{.Ref}}'])
        self.assertEqual(command.call_args_list[3].args[0], ['docker', 'buildx', 'history', 'trace', '--builder', 'ci-test', 'coldrecord'])
        for call in command.call_args_list:
            self.assertTrue(call.kwargs['check'])
            self.assertTrue(call.kwargs['capture_output'])

    def test_rejects_missing_evidence_and_invalid_record_identifier(self):
        for records in ('', 'coldrecord\n', '--unexpected-option\n'):
            with self.subTest(records=records), patch.dict(os.environ, {'BUILDX_BUILDER': 'ci-test'}), \
                 patch.object(self.cache.subprocess, 'run', side_effect=[
                     SimpleNamespace(stdout='[registry."docker.io"]\nmirrors = ["mirror.gcr.io"]\n'),
                     SimpleNamespace(stdout=records), SimpleNamespace(stdout=json.dumps({'data': []})),
                 ]), contextlib.redirect_stdout(io.StringIO()):
                with self.assertRaises(RuntimeError):
                    self.cache.main()

    def test_rejects_invalid_mirror_config_before_trace(self):
        with patch.dict(os.environ, {'BUILDX_BUILDER': 'ci-test'}), \
             patch.object(self.cache.subprocess, 'run', return_value=SimpleNamespace(stdout='[registry."docker.io"]\nmirrors = []\n')) as command:
            with self.assertRaises(RuntimeError):
                self.cache.main()
            self.assertEqual(command.call_count, 1)

    def test_failed_producer_cannot_pass_or_expose_output(self):
        error = subprocess.CalledProcessError(1, ['docker', 'buildx', 'history', 'trace'], output=json.dumps(trace()), stderr='fake-secret')
        with patch.dict(os.environ, {'BUILDX_BUILDER': 'ci-test'}), \
             patch.object(self.cache.subprocess, 'run', side_effect=[
                 SimpleNamespace(stdout='[registry."docker.io"]\nmirrors = ["mirror.gcr.io"]\n'),
                 SimpleNamespace(stdout='coldrecord\n'), error,
             ]), contextlib.redirect_stdout(io.StringIO()) as stdout:
            with self.assertRaises(subprocess.CalledProcessError) as raised:
                self.cache.main()
            self.assertNotIn('fake-secret', stdout.getvalue() + str(raised.exception))


if __name__ == '__main__':
    unittest.main()
