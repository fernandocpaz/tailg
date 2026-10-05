import unittest

from release_version import next_version, parse_version


class ReleaseVersionTests(unittest.TestCase):
    def test_versions(self):
        cases = [
            ([], 'v0.1.2', 'v0.1.2'),
            (['v0.1.2'], 'v0.1.2', 'v0.1.3'),
            (['v0.1.9', 'v0.1.10'], 'v0.1.2', 'v0.1.11'),
            (['v0.1.10'], 'v0.2.0', 'v0.2.0'),
            (['v0.1.2', 'v0.2.0-rc.1', 'latest'], 'v0.1.2', 'v0.1.3'),
            (['v0.1.2', 'v1.0.0'], 'v0.1.2', 'v1.0.1'),
            (['v0.1.2'], ' v0.1.2\n', 'v0.1.3'),
        ]
        for tags, requested, expected in cases:
            with self.subTest(tags=tags, requested=requested):
                self.assertEqual(next_version(tags, requested), expected)

    def test_invalid_marker_fails(self):
        for value in ['', 'latest', 'v01.2.3', 'v1.2', 'v1.2.3-rc.1']:
            with self.subTest(value=value), self.assertRaises(ValueError):
                next_version(['v0.1.2'], value)

    def test_stable_versions_only(self):
        self.assertIsNone(parse_version('v1.2.3+build'))
        self.assertEqual(parse_version('v10.20.30'), (10, 20, 30))


if __name__ == '__main__':
    unittest.main()
