# betterDock 위젯 마켓

[betterDock](https://github.com/s1ckdark/betterDock)(macOS Dock 앱)의 커뮤니티 위젯 저장소다.
모든 위젯은 메인테이너가 검토한 뒤에만 들어오고, 목록(`index.json`)은 서명되어 배포된다.

## 설치하는 법

앱의 **마켓** 탭에서 위젯을 고르고 **설치**를 누른다. 앱이 이 저장소의 `index.json`을 받아
서명을 확인하고, 위젯 파일을 내려받아 하나하나 검증한 뒤 사용자 위젯 폴더에 설치한다.
셸·네트워크 권한을 쓰는 위젯은 설치할 때 앱의 **권한 승인 대화상자**가 그대로 뜬다 —
마켓에서 받았다고 승인을 건너뛰지 않는다.

## 신뢰 모델

1. **검토.** 위젯은 PR로만 들어온다. 메인테이너가 [검토 목록](.github/pull_request_template.md)에
   따라 매니페스트와 스크립트를 직접 읽고 병합한다. CI는 규칙 위반을 자동으로 걸러 낸다.
2. **서명된 목록.** 병합 뒤 메인테이너가 `index.json`을 만들고, 메인테이너의 Mac Keychain에만
   있는 Ed25519 비밀키로 서명해 `index.json.sig`를 함께 올린다. 비밀키는 이 저장소·CI 어디에도
   없다.
3. **앱의 확인.**
   - 앱에 박힌 공개키로 `index.json.sig`를 검증한다. 맞지 않으면 목록 전체를 버린다.
   - `sequence`가 이전에 본 값(처음이면 앱에 박힌 하한)보다 작으면 거부한다(옛 목록으로 되돌리기 공격 방지).
   - `expiresAt`(서명 후 30일)이 지난 목록으로는 설치·업데이트를 하지 않는다(목록 동결 방지).
   - 파일마다 `index.json`에 적힌 크기와 SHA-256이 맞아야 설치한다.
   - 설치 후에는 일반 사용자 위젯과 똑같이 권한 승인과 폴더 지문 확인을 거친다.

서명 공개키(Ed25519, raw 32바이트 base64):

```
pwO6l6HlEPMEaOxjIrBNZsjC5yPWHJvhQFsfuiZ+DXU=
```

## 위젯 올리기

1. 이 저장소를 포크하고 `widgets/<id>/`에 `manifest.json`, `market.json`, 스크립트를 넣는다.
2. `swift tools/build-index.swift --validate`로 규칙을 확인한다.
3. PR을 연다. **`index.json`·`index.json.sig`는 고치지 않는다**(메인테이너가 만든다).

자세한 규칙은 [CONTRIBUTING.md](CONTRIBUTING.md), 목록 형식은 [docs/index-format.md](docs/index-format.md)를 본다.
위젯 요청·문제 제보는 [이슈](../../issues/new/choose)로 한다.

## 메인테이너: PR 병합 뒤

```sh
git pull
swift tools/build-index.swift            # 검사 + index.json 갱신(sequence +1, 만료 30일 뒤)
swift tools/sign-index.swift             # Keychain 키로 index.json.sig 작성
swift tools/verify-index.swift pwO6l6HlEPMEaOxjIrBNZsjC5yPWHJvhQFsfuiZ+DXU=
git add index.json index.json.sig && git commit -m "index: <id> <버전> 반영" && git push
```

## 메인테이너: 매달 다시 서명하기

> **`index.json`은 서명 후 30일에 만료된다. 적어도 한 달에 한 번은 다시 서명한다.**
> 만료된 목록으로는 앱이 설치·업데이트를 하지 않는다. CI(Validate)가 주 1회 돌아
> 만료 7일 전부터 실패하므로, 그 알림을 받으면 아래를 실행한다.

```sh
git pull
swift tools/build-index.swift --renew    # 위젯이 그대로여도 sequence +1, 만료 연장
swift tools/sign-index.swift
swift tools/verify-index.swift pwO6l6HlEPMEaOxjIrBNZsjC5yPWHJvhQFsfuiZ+DXU=
git add index.json index.json.sig && git commit -m "index: 서명 갱신" && git push
```

처음 한 번은 `swift tools/keygen.swift`로 키를 만든다(이미 있으면 그대로 쓰고 공개키만 출력한다).

---

## English

Community widget marketplace for **betterDock**, a macOS Dock app with manifest-based widgets.

- **Install:** open the app's **Market** tab and click *Install*. The app downloads `index.json`,
  verifies its Ed25519 signature with an embedded public key, checks `sequence` against rollback,
  downloads each file and verifies its size and SHA-256. Widgets that use shell or network
  permissions still show the app's permission consent dialog.
- **Trust:** every widget is reviewed by the maintainer via PR. The index is signed with a key
  that lives only in the maintainer's Mac Keychain — never in this repo or CI. The signed index
  expires 30 days after signing (`expiresAt`); the maintainer re-signs it at least monthly
  (`build-index --renew`), and the app refuses installs/updates from an expired index.
- **Submit:** add `widgets/<id>/` with `manifest.json` + `market.json`, run
  `swift tools/build-index.swift --validate`, open a PR. Do not edit `index.json` or its signature.
  See [CONTRIBUTING.md](CONTRIBUTING.md) (Korean) for the rules.
