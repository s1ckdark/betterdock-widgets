# index.json 형식

`swift tools/build-index.swift`가 `widgets/*`에서 만든다. 손으로 고치지 않는다.

```json
{
  "schemaVersion": 1,
  "generatedAt": "2026-10-12T00:00:00Z",
  "expiresAt": "2026-11-11T00:00:00Z",
  "sequence": 2,
  "widgets": [
    {
      "id": "example-uptime",
      "name": "가동 시간",
      "description": { "ko": "…", "en": "…" },
      "author": "s1ckdark",
      "category": "system",
      "version": "1.0.0",
      "minAppVersion": "0.1.0",
      "permissions": ["shell"],
      "shell": { "argv": ["/bin/sh", "uptime.sh"] },
      "httpOrigins": [],
      "imageOrigins": [],
      "openURLOrigins": [],
      "builtinKinds": [],
      "files": [
        { "path": "manifest.json", "size": 556, "sha256": "…" }
      ],
      "changelog": "첫 버전",
      "homepage": "https://…"
    }
  ]
}
```

## 최상위

| 키 | 형식 | 뜻 |
|---|---|---|
| `schemaVersion` | 정수 | 언제나 `1`. 모르는 값이면 앱은 목록을 쓰지 않는다 |
| `generatedAt` | 문자열 | 만든 시각, ISO 8601 UTC(`yyyy-MM-ddTHH:mm:ssZ`). 표시용 |
| `expiresAt` | 문자열 | 만료 시각, ISO 8601 UTC. 언제나 `generatedAt` + 30일. **필수** — 없으면 앱은 목록 전체를 버린다. 지났으면 앱은 목록을 보여 주되 설치·업데이트를 막는다(아래 "만료") |
| `sequence` | 정수 ≥ 1 | 목록이 바뀔 때마다 1씩 오른다. **앱은 이전에 받아들인 값보다 작은 목록을 거부한다**(되돌리기 방지). 같은 값은 같은 목록이다 |
| `widgets` | 배열 | `id`의 UTF-8 바이트 순으로 정렬 |

## 위젯 항목

| 키 | 형식 | 출처 |
|---|---|---|
| `id` | 문자열 | `manifest.json`의 `id` = 폴더 이름. `^[a-z0-9][a-z0-9._-]{0,63}$`, `com.betterdock.` 접두어 금지 |
| `name` | 문자열 | `manifest.json`의 `name` |
| `description` | `{ "ko", "en" }` | `market.json` |
| `author` | 문자열 | `market.json` |
| `category` | 문자열 | `system` · `time` · `developer` · `apps` · `other` (`manifest.json`과 같다) |
| `version` | 문자열 | `MAJOR.MINOR.PATCH` (`manifest.json`과 같다) |
| `minAppVersion` | 문자열 | `MAJOR.MINOR.PATCH`. 앱 버전이 이보다 낮으면 설치하지 않는다 |
| `permissions` | 문자열 배열 | `manifest.json`의 `permissions`, 정렬. `network` · `shell` |
| `shell` | `{ "argv": [...] }` 또는 `null` | 셸 소스면 `[command, ...args]`를 **매니페스트에 적힌 그대로**(스크립트는 폴더 기준 상대 경로). 앱은 실행할 때 강제 옵션(`python3 -I -B`, `zsh -f`)을 붙이고 스크립트를 절대 경로로 바꾼다 |
| `httpOrigins` | 문자열 배열 | `http` 소스의 `https://host[:port]` |
| `imageOrigins` | 문자열 배열 | `compact`에 고정으로 적힌 원격 이미지의 `https://host[:port]`, 정렬·중복 제거 |
| `openURLOrigins` | 문자열 배열 | `compact`의 탭 동작 `openURL`에 적힌 주소의 `https://host[:port]`, 정렬·중복 제거 |
| `builtinKinds` | 문자열 배열 | `builtin` 소스의 `kind` |
| `files` | 배열 | 위젯 폴더의 모든 일반 파일(`.DS_Store` 제외). `path`의 UTF-8 바이트 순 |
| `files[].path` | 문자열 | 폴더 기준 상대 경로. 각 부분은 `^[A-Za-z0-9_-][A-Za-z0-9._-]*$` |
| `files[].size` | 정수 | 바이트 |
| `files[].sha256` | 문자열 | 소문자 16진수 64자 |
| `changelog` | 문자열 | `market.json` |
| `homepage` | 문자열 또는 `null` | `market.json`(선택, `https://`) |

`permissions`·`shell`·`httpOrigins`·`imageOrigins`·`openURLOrigins`·`builtinKinds`는 마켓 화면에서 설치 전에
보여 주기 위한 요약이다. 실제 권한 판정은 앱이 내려받은 `manifest.json`으로 다시 한다.
요약이 매니페스트에서 다시 만든 값과 하나라도 다르면 앱은 설치하지 않는다.

## 파일 내려받기

```
https://raw.githubusercontent.com/s1ckdark/betterdock-widgets/main/widgets/<id>/<path>
```

앱은 받은 바이트의 크기와 SHA-256이 `files[]`와 하나라도 다르면 설치를 중단한다. 목록에
없는 파일은 받지 않는다.

## 서명

- `index.json.sig` = `index.json` 파일의 **바이트 그대로**에 대한 Ed25519 서명을 base64로
  적은 것(줄바꿈 없음. 검증할 때 앞뒤 공백은 무시해도 된다).
- 공개키는 Ed25519 raw 32바이트의 base64다(README 참고). 앱에 박혀 있다.
- 앱은 서명을 먼저 확인하고, 맞을 때만 JSON을 해석한다.
- 비밀키는 메인테이너 Mac의 로그인 Keychain에만 있다(generic password, service
  `com.betterdock.market-signing`, account `index-signing-key-v1`).

## 만료

서명된 목록은 `sequence` 덕분에 **더 오래된** 목록으로 되돌릴 수는 없지만, 지금 목록을 계속
되돌려 주는 것(동결)은 막지 못한다. 그러면 새 위젯·보안 업데이트·철회가 앱에 닿지 않는다.
`expiresAt`이 그 기간을 30일로 묶는다.

- 앱은 `expiresAt`이 지난 목록으로 **설치·업데이트를 하지 않는다**(둘러보기는 된다).
- 그래서 위젯이 바뀌지 않아도 메인테이너는 **적어도 한 달에 한 번** 다시 만들어 서명한다:

  ```sh
  swift tools/build-index.swift --renew   # sequence +1, generatedAt·expiresAt 새로
  swift tools/sign-index.swift
  swift tools/verify-index.swift <공개키>
  ```

- CI는 주 1회 예약 실행되고, 만료가 7일 안으로 다가오면 `--check`가 실패한다.
- 앱에는 처음 실행에서도 지킬 `sequence` 하한(`MarketRules.minimumSequence`)이 박혀 있다.
  처음 설치한 앱이 그보다 오래된 서명 목록을 받으면 거부한다.

## 형식의 안정성

`index.json`은 키를 정렬하고 들여쓰기 2칸, 슬래시를 이스케이프하지 않는 JSON에 끝 줄바꿈 하나다.
같은 `widgets/`에서 언제나 같은 바이트가 나오므로 CI(`--check`)가 `generatedAt`·`sequence`만
기존 값으로 두고(`expiresAt`은 `generatedAt`에서 다시 계산) 다시 만들어 바이트 단위로 비교한다.
