"""Keep the operational inventory attached to the actual configuration readers."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]


class DeploymentInventoryTests(unittest.TestCase):
    def test_used_runtime_settings_are_documented(self):
        inventory = next((ROOT / 'docs/runbooks').glob('*-product-configuration.md')).read_text()
        names = set()
        for relative in ('backend/internal/app/config.go', 'backend/cmd/server/main.go',
                         'backend/internal/modules/telegram/config.go', 'backend/internal/modules/telegram/support.go',
                         'backend/internal/modules/telegram/audit_mirror.go', 'backend/cmd/server/backup.go'):
            source = (ROOT / relative).read_text()
            names.update(re.findall(r'os\.(?:Getenv|LookupEnv)\("([A-Z_]+)"\)', source))
            names.update(name + '_FILE' for name in re.findall(r'(?:SecretFile|loadTokenFile)\("([A-Z_]+)"\)', source))
            names.update(re.findall(r'"([A-Z]+_[A-Z_]+)":\s*&', source))
        names.update(re.findall(r'\$\{([A-Z_]+)(?::[-+?][^}]*)?\}', (ROOT / 'web/scripts/runtime-config.sh').read_text()))
        missing = sorted(name for name in names if name not in inventory and name + '_FILE' not in inventory)
        self.assertEqual(missing, [], 'New configuration needs its deployment inventory entry')


if __name__ == '__main__':
    unittest.main()
