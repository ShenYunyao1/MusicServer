import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('upgrade', Path(__file__).with_name('schema-upgrade-process.py'))
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


class MigrationHistoryTest(unittest.TestCase):
    def test_preserves_exact_old_history_and_one_new_edge(self):
        before = {'schema_migrations': [(7, 8, 'original timestamp')], 'users': [('retained',)]}
        after = {**before, 'schema_migrations': before['schema_migrations'] + [(8, 9, 'new timestamp')]}
        upgrade.assert_preserved_rows(before, after, 8, 9)
        for history in (
            [(7, 8, 'rewritten'), (8, 9, 'new timestamp')],
            [(8, 9, 'new timestamp')],
            [(7, 8, 'original timestamp'), (7, 9, 'new timestamp')],
            [(7, 8, 'original timestamp'), (8, 9, '')],
        ):
            with self.assertRaises(AssertionError):
                upgrade.assert_preserved_rows(before, {**after, 'schema_migrations': history}, 8, 9)

    def test_same_schema_does_not_add_history(self):
        before = {'schema_migrations': [(7, 8, 'original timestamp')]}
        upgrade.assert_preserved_rows(before, before, 8, 8)
        with self.assertRaises(AssertionError):
            upgrade.assert_preserved_rows(before, {'schema_migrations': before['schema_migrations'] + [(8, 9, 'new')]}, 8, 8)


if __name__ == '__main__':
    unittest.main()
