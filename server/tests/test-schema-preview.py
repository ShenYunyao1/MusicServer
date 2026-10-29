import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("schema_preview", Path(__file__).with_name("build-schema-preview.py"))
preview = importlib.util.module_from_spec(spec)
spec.loader.exec_module(preview)


class PreviewTests(unittest.TestCase):
    def test_only_schema_constant_changes(self):
        source = 'package version\nconst (\n Version = "0.20.2"\n Schema  = 7\n)\n'
        self.assertEqual(preview.preview_source(source), source.replace("Schema  = 7", "Schema  = 8"))

    def test_activated_source_keeps_schema_eight(self):
        source = 'Version = "0.21.0"\nSchema = 8\n'
        self.assertEqual(preview.preview_source(source), source)
        self.assertEqual(preview.preview_source(source, '0.22.0'), source.replace('0.21.0', '0.22.0'))

    def test_unknown_or_ambiguous_source_is_refused(self):
        for source in ("Schema = 9\n", "Schema = 7\nSchema = 7\n", "Schema = 17\n", "Schema = 7\nSchema = 8\n"):
            with self.subTest(source=source), self.assertRaises(ValueError):
                preview.preview_source(source)

    def test_explicit_test_version_is_validated(self):
        source='Version = "0.20.2"\nSchema = 7\n'
        self.assertIn('Version = "0.21.0"',preview.preview_source(source,"0.21.0"))
        for version in ('../x','0.21.0-preview','01.2.3','1.2.3\n'):
            with self.assertRaises(ValueError): preview.preview_source(source,version)


if __name__ == "__main__":
    unittest.main()
