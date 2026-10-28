import base64
import importlib.util
from pathlib import Path
import plistlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("configure_updates", Path(__file__).with_name("configure-updates.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class UpdateBundleTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.app = self.root / "magentic.app"
        self.plist = self.app / "Contents/Info.plist"
        self.plist.parent.mkdir(parents=True)
        with self.plist.open("wb") as target:
            plistlib.dump({"CFBundleIdentifier": "com.wails.magentic", "LSMinimumSystemVersion": "10.13.0"}, target)
        # Match the shipped Wails template, including its missing declaration.
        self.plist.write_bytes(self.plist.read_bytes().split(b"\n", 1)[1])
        self.sdk = self.root / "sdk"
        framework = self.sdk / "Sparkle.framework"
        (framework / "Versions/B/Resources").mkdir(parents=True)
        (self.sdk / "LICENSE").write_text("Sparkle license fixture")
        (framework / "Versions/B/Sparkle").write_bytes(b"test framework")
        (framework / "Versions/Current").symlink_to("B")
        (framework / "Sparkle").symlink_to("Versions/Current/Sparkle")
        (framework / "Resources").symlink_to("Versions/Current/Resources")
        with (framework / "Resources/Info.plist").open("wb") as target:
            plistlib.dump({"LSMinimumSystemVersion": "12.0"}, target)
        self.key = base64.b64encode(bytes(range(32))).decode()

    def read(self):
        with self.plist.open("rb") as source:
            return plistlib.load(source, fmt=plistlib.FMT_XML)

    def test_release_preserves_framework_links_and_requires_signatures(self):
        module.configure(self.app, self.sdk, self.key, version="1.2.3")
        info = self.read()
        self.assertEqual(info["CFBundleVersion"], "1.2.3")
        self.assertEqual(info["CFBundleShortVersionString"], "1.2.3")
        self.assertEqual(info["SUPublicEDKey"], self.key)
        self.assertEqual(info["LSMinimumSystemVersion"], "12.0")
        self.assertTrue(info["SUVerifyUpdateBeforeExtraction"])
        self.assertTrue(info["SURequireSignedFeed"])
        self.assertFalse(info["SUEnableAutomaticChecks"])
        self.assertFalse(info["SUAllowsAutomaticUpdates"])
        self.assertTrue((self.app / "Contents/Frameworks/Sparkle.framework/Sparkle").is_symlink())
        self.assertEqual((self.app / "Contents/Resources/Sparkle-LICENSE.txt").read_text(), "Sparkle license fixture")

    def test_development_rebuild_removes_old_release_configuration(self):
        module.configure(self.app, self.sdk, self.key)
        module.configure(self.app, self.sdk)
        self.assertFalse(any(key in self.read() for key in module.UPDATE_KEYS))
        self.assertFalse((self.app / "Contents/Frameworks/Sparkle.framework").exists())
        self.assertFalse((self.app / "Contents/Resources/Sparkle-LICENSE.txt").exists())

    def test_invalid_release_never_changes_existing_installation(self):
        original = self.plist.read_bytes()
        for key, url, version in [("bad", module.FEED_URL, ""),
                                  (self.key, "http://example.com/feed", ""),
                                  (self.key, "https://user:secret@example.com/feed", ""),
                                  (self.key, module.FEED_URL, "1.2.3-beta"),
                                  (self.key, module.FEED_URL, "01.2.3")]:
            with self.subTest(key=key, url=url, version=version), self.assertRaises(ValueError):
                module.configure(self.app, self.sdk, key, url, version)
            self.assertEqual(self.plist.read_bytes(), original)

    def test_missing_sdk_cannot_produce_an_update_capable_bundle(self):
        with self.assertRaises(ValueError):
            module.configure(self.app, self.root / "missing", self.key)
        self.assertNotIn("SUPublicEDKey", self.read())


if __name__ == "__main__":
    unittest.main()
