#!/usr/bin/env python3
"""Configure the built bundle before codesign. No network or private keys."""
import base64
import os
from pathlib import Path
import plistlib
import re
import shutil
import sys
from urllib.parse import urlparse

FEED_URL = "https://github.com/MarDonhauser/magentic/releases/latest/download/appcast.xml"
UPDATE_KEYS = ("SUPublicEDKey", "SUFeedURL", "SUEnableAutomaticChecks", "SUAllowsAutomaticUpdates",
               "SUAutomaticallyUpdate", "SUVerifyUpdateBeforeExtraction", "SURequireSignedFeed")


def configure(app, sdk, public_key="", feed_url=FEED_URL, version=""):
    app = Path(app)
    if app.suffix != ".app" or app.is_symlink():
        raise ValueError("Ein echtes .app-Bundle wird benötigt.")
    plist_path = app / "Contents/Info.plist"
    # Wails templates may begin with DOCTYPE rather than an XML declaration,
    # which plistlib's format auto-detection does not recognize.
    data = plist_path.read_bytes()
    info = plistlib.loads(data, fmt=plistlib.FMT_BINARY if data.startswith(b"bplist") else plistlib.FMT_XML)
    if info.get("CFBundleIdentifier") != "com.wails.magentic":
        raise ValueError("Das Bundle gehört nicht zu magentic.")
    if version and not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("Die Release-Version muss MAJOR.MINOR.PATCH entsprechen.")
    framework = app / "Contents/Frameworks/Sparkle.framework"
    license_path = app / "Contents/Resources/Sparkle-LICENSE.txt"
    if public_key:
        try:
            key = base64.b64decode(public_key, validate=True)
        except ValueError:
            raise ValueError("Der öffentliche Update-Schlüssel ist ungültig.") from None
        if len(key) != 32:
            raise ValueError("Der öffentliche Update-Schlüssel muss 32 Bytes lang sein.")
        url = urlparse(feed_url)
        if url.scheme != "https" or not url.hostname or url.username or url.password:
            raise ValueError("Der Update-Feed muss eine HTTPS-Adresse ohne Zugangsdaten sein.")
        sdk_framework = Path(sdk) / "Sparkle.framework"
        if not (sdk_framework / "Sparkle").is_file():
            raise ValueError("Sparkle fehlt. Zuerst scripts/prepare-sparkle.sh ausführen.")
        sdk_license = Path(sdk) / "LICENSE"
        if not sdk_license.is_file():
            raise ValueError("Die Sparkle-Lizenz fehlt im SDK.")
        with (sdk_framework / "Resources/Info.plist").open("rb") as source:
            sdk_info = plistlib.load(source)
        minimum = sdk_info["LSMinimumSystemVersion"]
        if tuple(map(int, info.get("LSMinimumSystemVersion", "0").split("."))) < tuple(map(int, minimum.split("."))):
            info["LSMinimumSystemVersion"] = minimum
    # A local rebuild must not accidentally retain a previous release's feed/key.
    for name in UPDATE_KEYS:
        info.pop(name, None)
    if framework.is_symlink():
        framework.unlink()
    elif framework.exists():
        shutil.rmtree(framework)
    license_path.unlink(missing_ok=True)
    if public_key:
        framework.parent.mkdir(parents=True, exist_ok=True)
        shutil.copytree(sdk_framework, framework, symlinks=True)
        license_path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(sdk_license, license_path)
        info.update(SUPublicEDKey=public_key, SUFeedURL=feed_url,
                    SUEnableAutomaticChecks=False, SUAllowsAutomaticUpdates=False,
                    SUAutomaticallyUpdate=False, SUVerifyUpdateBeforeExtraction=True,
                    SURequireSignedFeed=True)
    if version:
        info.update(CFBundleVersion=version, CFBundleShortVersionString=version)
    with plist_path.open("wb") as target:
        plistlib.dump(info, target)


if __name__ == "__main__":
    try:
        configure(sys.argv[1], Path(__file__).resolve().parent.parent / "app/build/sparkle",
                  os.getenv("MAGENTIC_UPDATE_PUBLIC_KEY", ""),
                  os.getenv("MAGENTIC_UPDATE_FEED_URL", FEED_URL), os.getenv("MAGENTIC_VERSION", ""))
    except (ValueError, OSError, plistlib.InvalidFileException) as error:
        sys.exit(f"Update-Konfiguration fehlgeschlagen: {error}")
