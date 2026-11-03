import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('upgrade', Path(__file__).with_name('schema-upgrade-process.py'))
upgrade = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upgrade)


class MigrationHistoryTest(unittest.TestCase):
    def test_only_valid_derived_updates_can_advance(self):
        before={'meta':[('sync_secret','retain'),('browse/community','3'),('browse/schema_version','5'),('browse/schema_digest','a'*64),('browse/epoch','retain-epoch')]}
        current=dict(before['meta']);current.update({'browse/community':'4','browse/schema_version':'6','browse/schema_digest':'b'*64})
        upgrade.assert_preserved_rows(before,{'meta':list(current.items())},8,9)
        for key,value in [('sync_secret','changed'),('browse/community','2'),('browse/community','invalid'),('browse/schema_digest','invalid'),('browse/epoch','changed')]:
            invalid={**current,key:value}
            with self.assertRaises(AssertionError):upgrade.assert_preserved_rows(before,{'meta':list(invalid.items())},8,9)
        del current['browse/community']
        with self.assertRaises(AssertionError):upgrade.assert_preserved_rows(before,{'meta':list(current.items())},8,9)

    def test_only_registered_derived_metadata_additions_are_allowed(self):
        before={'meta':[('sync_secret','must retain'),('revision','5')]}
        additions=[('browse/schema_version','2'),('browse/schema_digest','digest'),('browse/epoch','epoch'),('browse/library','0'),('browse/admin','0'),('browse/search_version','1'),('browse/account/00000000-0000-4000-8000-000000000001','1')]
        upgrade.assert_preserved_rows(before,{'meta':before['meta']+additions},8,8)
        for rows in ([('sync_secret','changed'),('revision','5')]+additions,before['meta']+[('unrecognized','addition')],before['meta'][1:]+additions):
            with self.assertRaises(AssertionError):upgrade.assert_preserved_rows(before,{'meta':rows},8,8)

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
