# 위젯 올리기

위젯 하나는 `widgets/<id>/` 폴더 하나다. 매니페스트 문법 자체는 앱 저장소의
[위젯 개발 가이드](https://github.com/s1ckdark/betterDock/blob/main/docs/widget-development.md)를
따른다. 이 문서는 마켓에 올릴 때 지켜야 하는 것만 적는다.

## 폴더 구성

```
widgets/
  example-uptime/          ← 폴더 이름 = manifest.json의 id
    manifest.json          ← 필수. 앱이 읽는 위젯 정의
    market.json            ← 필수. 마켓에 보일 정보
    uptime.sh              ← 스크립트·이미지 등(선택)
```

- **`id`**: `^[a-z0-9][a-z0-9._-]{0,63}$`(소문자·숫자·`.`·`_`·`-`, 64자까지). 폴더 이름과
  같아야 한다. **`com.betterdock.`으로 시작할 수 없다**(내장 위젯 전용). 다른 위젯과 겹치면 안 된다.
- **파일·폴더 이름**: 영문·숫자·`.`·`_`·`-`만 쓰고 `.`으로 시작할 수 없다(숨김 파일 금지).
  내려받는 URL에 그대로 들어가기 때문이다.
- **심볼릭 링크 금지**(폴더 안 어디에도). 일반 파일과 폴더만 둔다.
- **상한**: 파일 2,000개, 폴더 전체 64MB(앱의 상한과 같다). 작을수록 좋다.
- `.DS_Store`는 무시된다(`.gitignore`에 있다).

## market.json

```json
{
  "author": "github-아이디 또는 이름",
  "description": { "ko": "한 줄 설명", "en": "One-line description" },
  "category": "system",
  "version": "1.0.0",
  "minAppVersion": "0.1.0",
  "changelog": "이번 버전에서 바뀐 것",
  "homepage": "https://github.com/…"
}
```

| 키 | 필수 | 규칙 |
|---|---|---|
| `author` | ✓ | 비어 있지 않은 문자열 |
| `description` | ✓ | `ko`와 `en` 두 키만, 둘 다 비어 있지 않은 문자열 |
| `category` | ✓ | `system` · `time` · `developer` · `apps` · `other`. `manifest.json`의 `category`(없으면 `other`)와 같아야 한다 |
| `version` | ✓ | `MAJOR.MINOR.PATCH`(숫자만). `manifest.json`의 `version`과 같아야 한다. 고칠 때마다 올린다 |
| `minAppVersion` | ✓ | `MAJOR.MINOR.PATCH`. 이 위젯이 동작하는 가장 낮은 앱 버전 |
| `changelog` | ✓ | 비어 있지 않은 문자열 |
| `homepage` | | `https://` 주소 |

그 밖의 키는 거부한다.

## 사용자 위젯 규칙 (앱이 강제하는 것)

마켓 위젯은 설치되면 **사용자 위젯**이다. 앱(`UserWidgetPolicy`)이 아래를 어긴 위젯은 불러오지
않는다. `tools/build-index.swift`가 같은 규칙으로 미리 검사한다.

### `shell` 소스 — `permissions`에 `"shell"` 필요

- `command`와 `args` **어디에도 템플릿(`{{ }}`)을 쓸 수 없다.** 승인 대화상자에 보인 argv가 실행되는
  argv와 글자 그대로 같아야 한다. 설정 값은 셸로 가지 않고, `secret` 설정만 `WIDGET_<키>` 환경
  변수로 간다.
- `command`는 아래 인터프리터 중 하나다. `/usr/bin/env`는 받지 않는다.

  | 인터프리터 | 쓸 수 있는 옵션(값 없음) | 인라인 스크립트 |
  |---|---|---|
  | `/bin/sh` | `-e` `-u` `-x` | `-c` |
  | `/bin/bash` | `-e` `-u` `-x` `--norc` `--noprofile` | `-c` |
  | `/bin/zsh` | `-e` `-u` `-x` `-f` | `-c` |
  | `/usr/bin/python3` | `-I` `-B` `-u` `-E` `-s` | `-c` |
  | `/usr/bin/osascript` | 없음 | `-e` |

  표에 없는 옵션(`bash -O extglob`, `python3 -W ignore`, `osascript -l JavaScript` 등)은 거부한다.
  옵션 뒤에는 인라인 스크립트 옵션과 고정 문자열이 오거나, **위젯 폴더 안의 스크립트 파일**(폴더
  기준 상대 경로, 폴더 밖으로 나가는 `..` 금지)이 와야 한다.
- 실행할 때 `/usr/bin/python3`에는 `-I -B`, `/bin/zsh`에는 `-f`가 언제나 앞에 붙는다.
- 앱의 환경 변수를 물려받지 않는다. `PATH=/usr/bin:/bin`, `HOME`, `LANG`, `WIDGET_<키>`만 있다.
  `/usr/sbin` 등의 도구는 절대 경로로 부른다.
- **`cwd`는 지정할 수 없다.** 언제나 위젯 폴더에서 실행된다.
- 표준 출력에 JSON을 낸다. 시간 초과 10초.
- **위젯 폴더에 파일을 쓰지 않는다**(캐시·로그 등). 폴더 지문이 바뀌어 다시 승인해야 한다.
  남길 파일은 `~/Library/Caches/…` 등 폴더 밖에 둔다.

### `http` 소스 — `permissions`에 `"network"` 필요

- `url`의 scheme과 호스트는 고정이어야 한다(템플릿, `@`, `\` 불가). 경로와 쿼리에는 템플릿을 쓸 수 있다.
- 리디렉션을 따라가지 않는다(3xx는 실패).

### `builtin` 소스

- `clock` · `system` · `battery` · `systemMonitor`만 쓸 수 있다.

### 이미지·탭 동작

- **원격 이미지**(`image`가 `http…`): scheme과 호스트가 고정이어야 하고 `"network"` 권한이 필요하다.
- **로컬 이미지**: 위젯 폴더 안 파일의 상대 경로만(템플릿·절대 경로·`~`·폴더 밖 `..` 불가).
- **`openURL`**: scheme과 호스트가 고정인 주소만.
- 탭 동작은 `openURL` · `launchApp`(사용자가 고른 앱만) · `openSettings` · `configureWidget`만
  동작한다. `requestAccess` · `mediaPlayPause` · `mediaNext` · `timerToggle` · `timerReset`은 쓸 수 없다.

## 마켓 추가 규칙 (앱보다 좁다)

| 규칙 | 이유 |
|---|---|
| `command`는 인터프리터만. 폴더 안 실행 파일을 직접 `command`로 쓰지 않는다 | 내려받은 파일에는 실행 권한이 없다. `/bin/sh script.sh`처럼 쓴다 |
| http `url`, 원격 이미지, `openURL`, `homepage`는 **`https://`만** | 평문 요청은 받지 않는다 |
| `permissions`는 쓰는 만큼만, 빠짐없이: `shell` ⇔ shell 소스, `network` ⇔ http 소스 또는 원격 이미지 | 승인 대화상자에 실제로 쓰는 권한만 보이게 |
| 소스 `type`은 `static` · `shell` · `http` · `builtin` | |
| shell `args`는 문자열만 | |
| JSON에 **중복 키 금지**, BOM 없는 UTF-8 | 검토한 값과 앱이 읽는 값이 갈리지 않게 |
| `id` = 폴더 이름, `version`은 `market.json`과 같게 | |

## 올리기 전에

```sh
swift tools/build-index.swift --validate
```

- 스크립트를 직접 돌려 JSON이 나오는지 본다:
  `env -i PATH=/usr/bin:/bin HOME="$HOME" LANG=ko_KR.UTF-8 /bin/sh widgets/<id>/script.sh`
- 앱의 `~/Library/Application Support/BetterDock/widgets/`에 폴더를 복사해 **다시 불러오기**로
  실제로 그려지는지 확인한다.
- `index.json`·`index.json.sig`·`tools/`·`.github/`는 고치지 않는다.

## 검토 기준

메인테이너는 [PR 템플릿](.github/pull_request_template.md)의 목록대로 본다. 특히:

- 스크립트를 **한 줄씩** 읽는다. 하는 일이 설명과 같고, 그 이상을 하지 않는가.
- 네트워크로 무엇을 보내는가(개인 정보, 파일 내용, 시크릿을 다른 곳으로 보내지 않는가).
- 폴더 밖 파일을 읽거나 쓰거나 지우지 않는가. 다른 프로그램을 내려받거나 실행하지 않는가.
- 난독화·인코딩된 코드, `eval`, `curl | sh` 같은 것이 없는가.
- 이름·아이콘·설명이 다른 위젯이나 앱을 사칭하지 않는가.

어긋나면 병합하지 않는다. 병합된 위젯도 문제가 드러나면 목록에서 내린다.
