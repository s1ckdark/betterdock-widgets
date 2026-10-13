#!/usr/bin/env python3
"""Offline regression checks for official/community package boundaries (macOS + Swift)."""

import json
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parent.parent


class MarketValidationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(prefix="betterdock-market-validator-")
        cls.binary = Path(cls.build.name) / "build-index"
        subprocess.run(["swiftc", str(REPO / "tools/build-index.swift"), "-o", str(cls.binary)], check=True)

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.workspace = tempfile.TemporaryDirectory(prefix="betterdock-market-fixture-")
        self.root = Path(self.workspace.name)
        shutil.copytree(REPO / "widgets", self.root / "widgets")

    def tearDown(self):
        self.workspace.cleanup()

    def edit(self, widget_id, filename, change):
        path = self.root / "widgets" / widget_id / filename
        value = json.loads(path.read_bytes())
        change(value)
        path.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")

    def run_validator(self, expected=0, message=None, mode="--validate"):
        result = subprocess.run([str(self.binary), mode], cwd=self.root, capture_output=True, text=True)
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        if message:
            self.assertIn(message, result.stderr)
        return result

    def rename_widget(self, old_id, new_id):
        old = self.root / "widgets" / old_id
        new = old.with_name(new_id)
        old.rename(new)
        self.edit(new_id, "manifest.json", lambda value: value.update(id=new_id))

    def test_all_official_packages_and_security_summaries(self):
        self.run_validator(mode="--write")
        widgets = json.loads((self.root / "index.json").read_bytes())["widgets"]
        by_id = {widget["id"]: widget for widget in widgets}
        official = [widget for widget in widgets if widget.get("distribution") == "official"]
        self.assertEqual(len(official), 17)
        self.assertNotIn("distribution", by_id["example-uptime"])
        self.assertEqual(by_id["example-uptime"]["shell"], {"argv": ["/bin/sh", "uptime.sh"]})
        for widget in official:
            self.assertEqual({file["path"] for file in widget["files"]}, {"manifest.json", "market.json"})
            self.assertEqual(widget["minAppVersion"], "0.1.0")
        ai = by_id["com.betterdock.ai-usage"]
        self.assertEqual(ai["permissions"], ["network"])
        self.assertEqual(ai["builtinKinds"], ["aiUsage"])
        self.assertEqual(ai["httpOrigins"], [])
        self.assertEqual(by_id["com.betterdock.app-launcher"]["imageOrigins"], [])
        self.assertEqual(by_id["com.betterdock.calendar"]["openURLOrigins"], [])
        self.assertEqual(by_id["com.betterdock.github-prs"]["httpOrigins"], ["https://api.github.com"])
        self.assertEqual(by_id["com.betterdock.github-prs"]["openURLOrigins"], ["https://github.com"])
        self.assertEqual(by_id["com.betterdock.weather"]["httpOrigins"], ["https://api.open-meteo.com"])

    def test_index_is_reproducible(self):
        self.run_validator(mode="--write")
        first = (self.root / "index.json").read_bytes()
        self.run_validator(mode="--write")
        self.assertEqual(first, (self.root / "index.json").read_bytes())
        self.run_validator(mode="--check")

    def test_unknown_official_id_is_rejected(self):
        self.rename_widget("com.betterdock.clock", "com.betterdock.unknown")
        self.run_validator(1, "알 수 없는 공식 위젯 ID")

    def test_community_cannot_claim_official_distribution(self):
        self.edit("example-uptime", "market.json", lambda value: value.update(distribution="official"))
        self.run_validator(1, "알 수 없는 공식 위젯 ID")

    def test_official_id_requires_official_distribution(self):
        self.edit("com.betterdock.clock", "market.json", lambda value: value.pop("distribution"))
        self.run_validator(1, "공식 위젯 전용")

    def test_explicit_community_cannot_use_reserved_prefix(self):
        self.edit("com.betterdock.clock", "market.json", lambda value: value.update(distribution="community"))
        self.run_validator(1, "공식 위젯 전용")

    def test_unknown_distribution_is_rejected(self):
        for distribution in ["Official", "trusted", False, 1, None]:
            with self.subTest(distribution=distribution):
                self.edit("example-uptime", "market.json", lambda value: value.update(distribution=distribution))
                self.run_validator(1, "distribution은 official 또는 community")

    def test_official_package_cannot_add_script(self):
        (self.root / "widgets/com.betterdock.clock/run.sh").write_text("echo unauthorized")
        self.run_validator(1, "두 파일만")

    def test_official_id_cannot_switch_native_capability(self):
        self.edit("com.betterdock.clock", "manifest.json", lambda value: value["data"]["source"].update(kind="aiUsage"))
        self.run_validator(1, "소스는 builtin/clock")

    def test_official_id_cannot_switch_to_shell(self):
        self.edit("com.betterdock.clock", "manifest.json", lambda value: value["data"].update(source={"type": "shell", "command": "/bin/sh", "args": ["-c", "echo 1"]}))
        self.run_validator(1, "소스는 builtin/clock")

    def test_community_cannot_use_native_private_kind(self):
        self.edit("example-uptime", "manifest.json", lambda value: value["data"].update(source={"type": "builtin", "kind": "aiUsage"}))
        self.run_validator(1, "builtin 종류는")

    def test_community_dynamic_image_stays_rejected(self):
        self.edit("example-uptime", "manifest.json", lambda value: value.update(compact={"image": "{{ .logo }}"}))
        self.run_validator(1, "로컬 이미지 경로에는 템플릿")

    def test_community_dynamic_url_stays_rejected(self):
        self.edit("example-uptime", "manifest.json", lambda value: value.update(compact={"text": "Open", "tap": {"openURL": "{{ .url }}"}}))
        self.run_validator(1, "https://로 시작하는 고정 주소")

    def test_community_shell_template_stays_rejected(self):
        self.edit("example-uptime", "manifest.json", lambda value: value["data"]["source"].update(args=["{{ .command }}"]))
        self.run_validator(1, "shell command와 args에는 템플릿")

    def test_community_unused_network_permission_stays_rejected(self):
        self.edit("example-uptime", "manifest.json", lambda value: value.update(permissions=["shell", "network"]))
        self.run_validator(1, "네트워크를 쓰지 않는데")

    def test_official_http_still_requires_network_permission(self):
        self.edit("com.betterdock.weather", "manifest.json", lambda value: value.update(permissions=[]))
        self.run_validator(1, "network")

    def test_official_literal_http_still_rejected(self):
        self.edit("com.betterdock.weather", "manifest.json", lambda value: value["data"]["source"].update(url="http://api.open-meteo.com/v1/forecast"))
        self.run_validator(1, "https://로 시작하는 고정 주소")

    def test_package_symlink_stays_rejected(self):
        (self.root / "widgets/com.betterdock.clock/extra.json").symlink_to("manifest.json")
        self.run_validator(1, "심볼릭 링크")

    def test_duplicate_json_key_stays_rejected(self):
        path = self.root / "widgets/com.betterdock.clock/market.json"
        path.write_text(path.read_text().replace('"distribution": "official"', '"distribution": "official", "distribution": "community"'))
        self.run_validator(1, "JSON 키가 중복")


class OfficialSyncTests(unittest.TestCase):
    def setUp(self):
        self.workspace = tempfile.TemporaryDirectory(prefix="betterdock-official-sync-")
        self.root = Path(self.workspace.name)
        (self.root / "tools").mkdir()
        self.tool = self.root / "tools/sync-official-widgets.py"
        shutil.copyfile(REPO / "tools/sync-official-widgets.py", self.tool)
        shutil.copytree(REPO / "widgets", self.root / "widgets")
        self.source = self.root / "app/Resources/BuiltinWidgets"
        for manifest in sorted((REPO / "widgets").glob("com.betterdock.*/manifest.json")):
            folder = self.source / manifest.parent.name.removeprefix("com.betterdock.")
            folder.mkdir(parents=True)
            shutil.copyfile(manifest, folder / "manifest.json")

    def tearDown(self):
        self.workspace.cleanup()

    def sync(self, check=False):
        command = ["python3", str(self.tool), "--source", str(self.source)]
        if check:
            command.append("--check")
        return subprocess.run(command, capture_output=True, text=True)

    def test_sync_preserves_exact_bytes_and_community(self):
        source = self.source / "clock/manifest.json"
        source.write_bytes(source.read_bytes() + b"\n  \n")
        community = self.root / "widgets/example-uptime/manifest.json"
        original = community.read_bytes()
        result = self.sync()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(source.read_bytes(), (self.root / "widgets/com.betterdock.clock/manifest.json").read_bytes())
        self.assertEqual(community.read_bytes(), original)
        self.assertEqual(self.sync(check=True).returncode, 0)

    def test_check_detects_manifest_drift_without_writing(self):
        destination = self.root / "widgets/com.betterdock.clock/manifest.json"
        changed = destination.read_bytes() + b"\n"
        destination.write_bytes(changed)
        self.assertEqual(self.sync(check=True).returncode, 1)
        self.assertEqual(destination.read_bytes(), changed)

    def test_check_detects_metadata_drift_without_writing(self):
        destination = self.root / "widgets/com.betterdock.clock/market.json"
        changed = destination.read_bytes() + b"\n"
        destination.write_bytes(changed)
        self.assertEqual(self.sync(check=True).returncode, 1)
        self.assertEqual(destination.read_bytes(), changed)

    def test_sync_refuses_unknown_source_widget(self):
        folder = self.source / "unknown"
        folder.mkdir()
        (folder / "manifest.json").write_text("{}")
        result = self.sync()
        self.assertEqual(result.returncode, 1)
        self.assertIn("17개 목록과 다릅니다", result.stderr)


if __name__ == "__main__":
    unittest.main()
