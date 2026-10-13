#!/usr/bin/env python3
"""Copy/check all official manifests byte-for-byte from a BetterDock app checkout.

Run from any directory. This tool does not build/sign the index or publish files.
"""

import argparse
import json
import sys
from pathlib import Path


OFFICIAL_WIDGETS = {
    "agent-presence": ("AI 에이전트의 작업 상태와 실행 중인 세션을 표시합니다.", "See AI agent activity and running sessions."),
    "ai-usage": ("AI 구독 사용 한도와 OpenRouter 계정 전체 잔액을 확인합니다.", "Track AI subscription limits and the account-wide OpenRouter balance."),
    "api-usage": ("OpenAI·Anthropic API 사용량과 비용을 확인합니다.", "Track OpenAI and Anthropic API usage and spending."),
    "app-launcher": ("선택한 앱을 원하는 아이콘 크기로 표시하고 실행합니다.", "Launch selected apps with adjustable icon sizes."),
    "bluetooth-battery": ("연결된 블루투스 기기의 배터리 잔량을 표시합니다.", "Show battery levels for connected Bluetooth devices."),
    "calendar": ("선택한 캘린더의 다음 일정을 표시합니다.", "Show upcoming events from selected calendars."),
    "clock": ("현재 날짜와 시간을 표시합니다.", "Show the current date and time."),
    "cpu-usage": ("CPU 사용률을 실시간으로 확인합니다.", "Monitor current CPU utilization."),
    "dev-status": ("로컬 Git 저장소와 Docker의 상태를 확인합니다.", "Check local Git repositories and Docker status."),
    "disk-usage": ("디스크 사용량과 남은 공간을 확인합니다.", "Monitor disk usage and remaining space."),
    "github-prs": ("GitHub에서 나에게 리뷰를 요청한 열린 PR 수를 확인합니다.", "See open GitHub pull requests awaiting your review."),
    "memory-usage": ("메모리 사용률을 실시간으로 확인합니다.", "Monitor current memory utilization."),
    "now-playing": ("재생 중인 음악을 표시하고 재생·다음 곡을 제어합니다.", "See the current track and control playback and the next track."),
    "settings": ("Dock에서 betterDock 설정을 바로 엽니다.", "Open betterDock settings directly from the Dock."),
    "system-monitor": ("CPU·메모리 등 선택한 시스템 정보를 함께 표시합니다.", "Show selected system metrics including CPU and memory."),
    "timer": ("타이머와 포모도로 집중 시간을 관리합니다.", "Manage timers and Pomodoro focus sessions."),
    "weather": ("설정한 위치의 현재 날씨를 확인합니다.", "See the current weather at your chosen location."),
}


def expected_packages(source: Path) -> dict[str, dict[str, bytes]]:
    paths = sorted(source.glob("*/manifest.json"))
    if {path.parent.name for path in paths} != set(OFFICIAL_WIDGETS):
        raise ValueError("앱의 공식 위젯 폴더 목록이 도구의 17개 목록과 다릅니다. 목록을 먼저 검토하세요.")
    result = {}
    for path in paths:
        if path.is_symlink() or path.parent.is_symlink():
            raise ValueError(f"공식 소스에 심볼릭 링크가 있습니다: {path}")
        manifest_bytes = path.read_bytes()
        manifest = json.loads(manifest_bytes)
        slug = path.parent.name
        widget_id = "com.betterdock." + slug
        if manifest.get("id") != widget_id:
            raise ValueError(f"공식 매니페스트 ID가 폴더와 다릅니다: {path}")
        ko, en = OFFICIAL_WIDGETS[slug]
        market = {
            "author": "betterDock / s1ckdark",
            "description": {"ko": ko, "en": en},
            "category": manifest.get("category", "other"),
            "version": manifest["version"],
            "minAppVersion": "0.1.0",
            "changelog": "betterDock 공식 위젯 · 앱에 포함된 동일 버전의 정의로 설치합니다.",
            "homepage": f"https://github.com/s1ckdark/betterDock/tree/main/Resources/BuiltinWidgets/{slug}",
            "distribution": "official",
        }
        result[widget_id] = {
            "manifest.json": manifest_bytes,
            "market.json": (json.dumps(market, ensure_ascii=False, indent=2) + "\n").encode("utf-8"),
        }
    return result


def main() -> int:
    repo = Path(__file__).resolve().parent.parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, default=repo.parent / "betterDock/Resources/BuiltinWidgets")
    parser.add_argument("--check", action="store_true", help="Report drift without changing files")
    args = parser.parse_args()
    try:
        packages = expected_packages(args.source)
        mismatches = []
        # Validate all destinations before writing any file. Never erase unexpected files.
        for widget_id, files in packages.items():
            folder = repo / "widgets" / widget_id
            if folder.is_symlink():
                raise ValueError(f"대상 폴더가 심볼릭 링크입니다: {folder}")
            if folder.exists():
                extra = {item.name for item in folder.iterdir()} - set(files) - {".DS_Store"}
                if extra:
                    raise ValueError(f"공식 패키지에 예상 밖의 파일이 있습니다: {folder}: {sorted(extra)}")
            for filename, expected in files.items():
                destination = folder / filename
                if destination.is_symlink():
                    raise ValueError(f"대상 파일이 심볼릭 링크입니다: {destination}")
                if not destination.exists() or destination.read_bytes() != expected:
                    mismatches.append((destination, expected))
        if args.check:
            for destination, _ in mismatches:
                print(f"✗ 공식 위젯 동기화 필요: {destination.relative_to(repo)}", file=sys.stderr)
            if mismatches:
                return 1
            print(f"✓ 공식 위젯 {len(packages)}개가 앱의 매니페스트 및 마켓 정보와 일치합니다")
        else:
            for destination, expected in mismatches:
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(expected)
            print(f"✓ 공식 위젯 {len(packages)}개 동기화 완료({len(mismatches)}개 파일 변경)")
        return 0
    except (OSError, ValueError, KeyError) as error:
        print(f"✗ {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
