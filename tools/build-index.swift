// widgets/*를 검사하고 index.json을 만든다.
//
//   swift tools/build-index.swift             검사 후 index.json을 새로 쓴다(내용이 같으면 그대로 둔다)
//   swift tools/build-index.swift --check     검사 + 커밋된 index.json이 지금 widgets/와 같은지 확인(CI)
//   swift tools/build-index.swift --validate  규칙 검사만 한다(index.json은 보지 않는다. PR CI)
//   swift tools/build-index.swift --renew     위젯이 그대로여도 새로 쓴다(sequence +1, 만료 30일 연장)
//
// `expiresAt`은 언제나 `generatedAt` + 30일이다. 앱은 만료된 목록으로 설치·업데이트를 하지
// 않는다 — 옛 서명 목록을 계속 되돌려 주는 것(동결 공격)으로 새 위젯·철회를 숨기지 못하게
// 한다. 그래서 위젯이 바뀌지 않아도 **한 달 안에 --renew로 다시 만들고 서명해야** 한다.
// `--check`는 만료가 7일 안으로 다가오면 실패해 CI(주 1회 예약 실행)가 알린다.
//
// 저장소 루트에서 실행한다. 규칙은 betterDock 앱의 사용자 위젯 규칙
// (`Sources/BetterDock/UserWidgetPolicy.swift`)을 그대로 옮기고, 마켓에서만 더 좁힌 것은
// CONTRIBUTING.md에 "마켓 추가 규칙"으로 적었다.
import CryptoKit
import Darwin
import Foundation

// MARK: - 규칙 상수 (앱과 같은 값)

let 최대파일수 = 2_000
let 최대바이트: Int64 = 64 * 1_048_576
let 금지ID접두어 = "com.betterdock."
let 인터프리터: [String: (flags: Set<String>, inline: String)] = [
    "/bin/sh": (["-e", "-u", "-x"], "-c"),
    "/bin/bash": (["-e", "-u", "-x", "--norc", "--noprofile"], "-c"),
    "/bin/zsh": (["-e", "-u", "-x", "-f"], "-c"),
    "/usr/bin/python3": (["-I", "-B", "-u", "-E", "-s"], "-c"),
    "/usr/bin/osascript": ([], "-e"),
]
let 허용builtin: Set<String> = ["clock", "system", "battery", "systemMonitor"]
// 공식 배포는 임의 패키지의 플래그만으로 허용하지 않는다. 앱과 함께 제공하는 17개 ID와
// 각 ID의 소스 종류만 받는다. 앱은 설치 시 자신의 내장 manifest 바이트와도 대조한다.
let 공식소스: [String: (type: String, kind: String?)] = [
    "com.betterdock.agent-presence": ("builtin", "agents"),
    "com.betterdock.ai-usage": ("builtin", "aiUsage"),
    "com.betterdock.api-usage": ("builtin", "apiUsage"),
    "com.betterdock.app-launcher": ("builtin", "appLauncher"),
    "com.betterdock.bluetooth-battery": ("builtin", "bluetoothBattery"),
    "com.betterdock.calendar": ("builtin", "calendar"),
    "com.betterdock.clock": ("builtin", "clock"),
    "com.betterdock.cpu-usage": ("builtin", "system"),
    "com.betterdock.dev-status": ("builtin", "devStatus"),
    "com.betterdock.disk-usage": ("builtin", "system"),
    "com.betterdock.github-prs": ("http", nil),
    "com.betterdock.memory-usage": ("builtin", "system"),
    "com.betterdock.now-playing": ("builtin", "nowPlaying"),
    "com.betterdock.settings": ("static", nil),
    "com.betterdock.system-monitor": ("builtin", "systemMonitor"),
    "com.betterdock.timer": ("builtin", "timer"),
    "com.betterdock.weather": ("http", nil),
]
let 카테고리: Set<String> = ["system", "time", "developer", "apps", "other"]
let 소스종류: Set<String> = ["static", "shell", "http", "builtin"]
let 권한종류: Set<String> = ["network", "shell"]
let market키: Set<String> = ["author", "description", "category", "version", "minAppVersion", "changelog", "homepage", "distribution"]
let 유효기간: TimeInterval = 30 * 24 * 60 * 60
let 갱신여유일 = 7

struct Violation: Error { let message: String }
func fail(_ message: String) -> Violation { Violation(message: message) }

func matches(_ s: String, _ pattern: String) -> Bool {
    s.range(of: pattern, options: .regularExpression) != nil
}
func isValidID(_ id: String) -> Bool { matches(id, #"^[a-z0-9][a-z0-9._-]{0,63}\z"#) }
func isSemver(_ v: String) -> Bool { matches(v, #"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z"#) }
func isSafeComponent(_ c: String) -> Bool { matches(c, #"^[A-Za-z0-9_-][A-Za-z0-9._-]*\z"#) }
func hasTemplate(_ s: String) -> Bool { s.contains("{{") || s.contains("}}") }

// MARK: - JSON (중복 키 거부)

/// 같은 객체 안에 같은 키가 두 번 나오면 거부한다. 검사기(JSONSerialization)와 앱의
/// 디코더가 중복 키 중 서로 다른 쪽을 고르면, 검토한 값과 실행되는 값이 갈린다.
struct DuplicateKeyScanner {
    let b: [UInt8]
    var i = 0
    init(_ data: Data) { b = [UInt8](data) }

    mutating func skipWS() { while i < b.count, [0x20, 0x09, 0x0A, 0x0D].contains(b[i]) { i += 1 } }
    func at(_ c: Character) -> Bool { i < b.count && b[i] == c.asciiValue! }
    mutating func expect(_ c: Character) throws {
        skipWS()
        guard at(c) else { throw fail("JSON 문법 오류(바이트 \(i))") }
        i += 1
    }
    mutating func string() throws -> String {
        guard at("\"") else { throw fail("JSON 문법 오류(바이트 \(i))") }
        let start = i
        i += 1
        while i < b.count {
            if b[i] == 0x5C { i += 2; continue }
            if b[i] == 0x22 {
                i += 1
                guard let s = try? JSONSerialization.jsonObject(with: Data(b[start..<i]), options: .fragmentsAllowed) as? String
                else { throw fail("JSON 문자열 오류(바이트 \(start))") }
                return s
            }
            i += 1
        }
        throw fail("JSON 문자열이 닫히지 않았다")
    }
    mutating func value(_ path: String) throws {
        skipWS()
        guard i < b.count else { throw fail("JSON이 일찍 끝났다") }
        if at("{") {
            i += 1
            var keys = Set<String>()
            skipWS()
            if at("}") { i += 1; return }
            while true {
                skipWS()
                let key = try string()
                guard keys.insert(key).inserted else { throw fail("JSON 키가 중복된다: \(path).\(key)") }
                try expect(":")
                try value("\(path).\(key)")
                skipWS()
                if at(",") { i += 1; continue }
                try expect("}")
                return
            }
        } else if at("[") {
            i += 1
            skipWS()
            if at("]") { i += 1; return }
            var n = 0
            while true {
                try value("\(path)[\(n)]")
                n += 1
                skipWS()
                if at(",") { i += 1; continue }
                try expect("]")
                return
            }
        } else if at("\"") {
            _ = try string()
        } else {
            while i < b.count, ![0x2C, 0x5D, 0x7D, 0x20, 0x09, 0x0A, 0x0D].contains(b[i]) { i += 1 }
        }
    }
}

func readObject(_ url: URL, name: String) throws -> [String: Any] {
    guard let data = try? Data(contentsOf: url) else { throw fail("\(name)을(를) 읽을 수 없다") }
    guard !data.starts(with: [0xEF, 0xBB, 0xBF]) else { throw fail("\(name)에 BOM이 있다(BOM 없는 UTF-8로 저장)") }
    guard String(data: data, encoding: .utf8) != nil else { throw fail("\(name)이(가) UTF-8이 아니다") }
    guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
        throw fail("\(name)이(가) JSON 객체가 아니거나 문법이 틀렸다")
    }
    var scanner = DuplicateKeyScanner(data)
    do { try scanner.value("") } catch let v as Violation { throw fail("\(name): \(v.message)") }
    return object
}

func isBool(_ v: Any?) -> Bool {
    guard let n = v as? NSNumber else { return false }
    return CFGetTypeID(n) == CFBooleanGetTypeID()
}
func nonEmptyString(_ v: Any?) -> String? {
    guard let s = v as? String, !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
    return s
}

// MARK: - 폴더 훑기

struct FileEntry { let path: String; let url: URL; let size: Int64 }

/// 앱의 `WidgetFolderHash.walk`와 같은 규칙(심볼릭 링크·일반 파일이 아닌 것 거부, 개수·크기 상한)에
/// 마켓 규칙(경로 글자, 숨김 파일 금지)을 더한다. `.DS_Store`는 건너뛴다(.gitignore 대상).
func walk(_ folder: URL) throws -> [FileEntry] {
    func lstatOf(_ url: URL) -> stat? {
        var info = stat()
        return lstat(url.path, &info) == 0 ? info : nil
    }
    guard let top = lstatOf(folder) else { throw fail("폴더를 읽을 수 없다") }
    guard (top.st_mode & S_IFMT) == S_IFDIR else { throw fail("위젯 폴더가 디렉터리가 아니다(심볼릭 링크 폴더 금지)") }
    var files: [FileEntry] = []
    func visit(_ dir: URL, _ prefix: String) throws {
        guard let names = try? FileManager.default.contentsOfDirectory(atPath: dir.path) else {
            throw fail("폴더를 읽을 수 없다: \(prefix.isEmpty ? "." : prefix)")
        }
        for name in names where name != ".DS_Store" {
            let url = dir.appendingPathComponent(name)
            let rel = prefix.isEmpty ? name : "\(prefix)/\(name)"
            guard isSafeComponent(name) else {
                throw fail("파일·폴더 이름은 영문·숫자·._-만 쓰고 .으로 시작할 수 없다: \(rel)")
            }
            guard let info = lstatOf(url) else { throw fail("읽을 수 없다: \(rel)") }
            switch info.st_mode & S_IFMT {
            case S_IFLNK: throw fail("심볼릭 링크는 쓸 수 없다: \(rel)")
            case S_IFDIR: try visit(url, rel)
            case S_IFREG:
                files.append(FileEntry(path: rel, url: url, size: Int64(info.st_size)))
                if files.count > 최대파일수 { throw fail("파일이 \(최대파일수)개를 넘는다") }
            default: throw fail("일반 파일이 아닌 항목이 있다: \(rel)")
            }
        }
    }
    try visit(folder, "")
    files.sort { Array($0.path.utf8).lexicographicallyPrecedes(Array($1.path.utf8)) }
    guard files.reduce(Int64(0), { $0 + $1.size }) <= 최대바이트 else {
        throw fail("폴더 전체가 \(최대바이트 / 1_048_576)MB를 넘는다")
    }
    return files
}

/// 앱의 `UserRenderPolicy.relativeInside`와 같다.
func relativeInside(_ path: String, files: Set<String>) -> String? {
    guard !path.isEmpty, !path.hasPrefix("/"), !path.hasPrefix("~"), !hasTemplate(path), !path.contains("\\")
    else { return nil }
    var parts: [Substring] = []
    for part in path.split(separator: "/", omittingEmptySubsequences: true) {
        if part == "." { continue }
        if part == ".." { guard !parts.isEmpty else { return nil }; parts.removeLast(); continue }
        parts.append(part)
    }
    let joined = parts.joined(separator: "/")
    return files.contains(joined) ? joined : nil
}

/// 앱의 `literalOrigin`과 같되 마켓은 https만 받는다.
func literalHTTPSOrigin(_ value: String, what: String) throws -> String {
    guard value.lowercased().hasPrefix("https://") else {
        throw fail("\(what)은(는) https://로 시작하는 고정 주소여야 한다(마켓은 http 불가): \(value)")
    }
    let hostPart = String(value.dropFirst(8).prefix { !"/?#".contains($0) })
    guard !hostPart.isEmpty, !hasTemplate(hostPart), !hostPart.contains("@"), !hostPart.contains("\\"),
          let url = URL(string: "https://\(hostPart)"), let host = url.host?.lowercased(), !host.isEmpty
    else { throw fail("\(what)의 호스트가 고정 주소가 아니다(템플릿·@·\\ 불가): \(value)") }
    return "https://\(host)" + (url.port.map { ":\($0)" } ?? "")
}

// MARK: - 셸 소스 (앱의 shellPlan과 같은 해석)

func checkShell(_ source: [String: Any], files: Set<String>) throws -> [String] {
    guard source["cwd"] == nil else { throw fail("shell 소스에 cwd를 쓸 수 없다(언제나 위젯 폴더에서 실행)") }
    guard let command = source["command"] as? String, !command.isEmpty else { throw fail("shell 소스에 command가 없다") }
    var args: [String] = []
    if let raw = source["args"] {
        guard let array = raw as? [Any] else { throw fail("shell args는 배열이어야 한다") }
        for item in array {
            guard let s = item as? String else { throw fail("shell args는 문자열만 쓸 수 있다") }
            args.append(s)
        }
    }
    guard !hasTemplate(command), !args.contains(where: hasTemplate) else {
        throw fail("shell command와 args에는 템플릿({{ }})을 쓸 수 없다")
    }
    guard let interpreter = 인터프리터[command] else {
        throw fail("command는 허용된 인터프리터(\(인터프리터.keys.sorted().joined(separator: ", ")))여야 한다(마켓은 폴더 안 실행 파일 직접 실행 불가): \(command)")
    }
    var index = 0
    while index < args.count, args[index].hasPrefix("-") {
        let option = args[index]
        if option == interpreter.inline {
            guard index + 1 < args.count else { throw fail("\(option) 뒤에 스크립트가 없다") }
            return [command] + args
        }
        guard interpreter.flags.contains(option) else { throw fail("\(command)에 허용되지 않은 옵션이다: \(option)") }
        index += 1
    }
    guard index < args.count else { throw fail("\(command) 뒤에 위젯 폴더 안의 스크립트 파일이 있어야 한다") }
    guard args[index].hasPrefix("/") == false, relativeInside(args[index], files: files) != nil else {
        throw fail("스크립트는 위젯 폴더 안 파일의 상대 경로여야 한다: \(args[index])")
    }
    return [command] + args
}

// MARK: - compact 훑기

func collect(_ node: Any, images: inout [Any], openURLs: inout [Any]) {
    if let dict = node as? [String: Any] {
        if let v = dict["image"] { images.append(v) }
        if let tap = dict["tap"] as? [String: Any], let u = tap["openURL"] { openURLs.append(u) }
        for key in dict.keys.sorted() { collect(dict[key]!, images: &images, openURLs: &openURLs) }
    } else if let array = node as? [Any] {
        for item in array { collect(item, images: &images, openURLs: &openURLs) }
    }
}

// MARK: - 위젯 하나

func buildEntry(folder: URL, folderName: String) throws -> [String: Any] {
    guard isValidID(folderName) else { throw fail("폴더 이름(=id)이 ^[a-z0-9][a-z0-9._-]{0,63}$를 따르지 않는다") }
    let entries = try walk(folder)
    let files = Set(entries.map(\.path))
    guard files.contains("manifest.json") else { throw fail("manifest.json이 없다") }
    guard files.contains("market.json") else { throw fail("market.json이 없다") }

    let manifest = try readObject(folder.appendingPathComponent("manifest.json"), name: "manifest.json")
    let market = try readObject(folder.appendingPathComponent("market.json"), name: "market.json")
    let distribution: String
    if let raw = market["distribution"] {
        guard let value = raw as? String, ["official", "community"].contains(value) else {
            throw fail("market.json distribution은 official 또는 community여야 한다")
        }
        distribution = value
    } else {
        distribution = "community"
    }
    let official = distribution == "official"

    // manifest.json
    guard let sv = manifest["schemaVersion"] as? NSNumber, !isBool(sv), sv.doubleValue == 1 else {
        throw fail("manifest.json schemaVersion은 1이어야 한다")
    }
    guard let id = manifest["id"] as? String else { throw fail("manifest.json에 id가 없다") }
    guard id == folderName else { throw fail("manifest.json id(\(id))가 폴더 이름(\(folderName))과 같아야 한다") }
    if official {
        guard 공식소스[id] != nil else { throw fail("알 수 없는 공식 위젯 ID: \(id)") }
        guard files == ["manifest.json", "market.json"] else {
            throw fail("공식 위젯은 manifest.json과 market.json 두 파일만 배포한다")
        }
    } else {
        guard !id.hasPrefix(금지ID접두어) else { throw fail("id는 \(금지ID접두어)로 시작할 수 없다(공식 위젯 전용)") }
    }
    guard let name = nonEmptyString(manifest["name"]) else { throw fail("manifest.json에 name이 없다") }
    guard let version = manifest["version"] as? String, isSemver(version) else {
        throw fail("manifest.json version은 MAJOR.MINOR.PATCH 형식이어야 한다")
    }
    let manifestCategory: String
    if let raw = manifest["category"] {
        guard let c = raw as? String, 카테고리.contains(c) else {
            throw fail("manifest.json category는 \(카테고리.sorted().joined(separator: ", ")) 중 하나여야 한다")
        }
        manifestCategory = c
    } else {
        manifestCategory = "other"
    }
    var permissions: [String] = []
    if let raw = manifest["permissions"] {
        guard let array = raw as? [Any] else { throw fail("permissions는 배열이어야 한다") }
        for item in array {
            guard let p = item as? String, 권한종류.contains(p) else { throw fail("알 수 없는 권한: \(item)") }
            guard !permissions.contains(p) else { throw fail("권한이 중복된다: \(p)") }
            permissions.append(p)
        }
    }
    permissions.sort()
    guard let data = manifest["data"] as? [String: Any], let source = data["source"] as? [String: Any],
          let type = source["type"] as? String else { throw fail("manifest.json에 data.source.type이 없다") }
    guard 소스종류.contains(type) else { throw fail("소스 type은 \(소스종류.sorted().joined(separator: ", ")) 중 하나여야 한다") }
    if official, let expected = 공식소스[id] {
        guard type == expected.type, source["kind"] as? String == expected.kind else {
            throw fail("공식 위젯 \(id)의 소스는 \(expected.type)/\(expected.kind ?? "-")이어야 한다")
        }
    }
    guard let compact = manifest["compact"], compact is [String: Any] else { throw fail("manifest.json에 compact가 없다") }

    var shellArgv: [String]?
    var httpOrigins: [String] = []
    var builtinKinds: [String] = []
    switch type {
    case "shell":
        shellArgv = try checkShell(source, files: files)
    case "http":
        guard let url = source["url"] as? String else { throw fail("http 소스에 url이 없다") }
        httpOrigins = [try literalHTTPSOrigin(url, what: "http url")]
    case "builtin":
        guard let kind = source["kind"] as? String, official || 허용builtin.contains(kind) else {
            throw fail("builtin 종류는 \(허용builtin.sorted().joined(separator: ", "))만 쓸 수 있다")
        }
        builtinKinds = [kind]
    default:
        break
    }

    var images: [Any] = []
    var openURLs: [Any] = []
    collect(compact, images: &images, openURLs: &openURLs)
    var imageOrigins = Set<String>()
    for value in images {
        guard let s = value as? String else { throw fail("image 값은 문자열이어야 한다") }
        if s.lowercased().hasPrefix("http") {
            imageOrigins.insert(try literalHTTPSOrigin(s, what: "image"))
        } else if official && hasTemplate(s) {
            // 앱에 포함된 네이티브 소스가 제공하는 앱 로고 등. 정적 호스트로 표시하지 않는다.
            continue
        } else if hasTemplate(s) {
            throw fail("로컬 이미지 경로에는 템플릿을 쓸 수 없다: \(s)")
        } else if relativeInside(s, files: files) == nil {
            throw fail("로컬 이미지는 위젯 폴더 안 파일의 상대 경로여야 한다: \(s)")
        }
    }
    var openURLOrigins = Set<String>()
    for value in openURLs {
        guard let s = value as? String else { throw fail("openURL 값은 문자열이어야 한다") }
        if official && hasTemplate(s) && !s.lowercased().hasPrefix("https://") {
            // calendar/nowPlaying/devStatus의 런타임 URL은 builtinKinds에 드러난다.
            continue
        }
        openURLOrigins.insert(try literalHTTPSOrigin(s, what: "openURL"))
    }

    // 권한은 쓰는 만큼만, 쓰는 것은 빠짐없이.
    let needsShell = type == "shell"
    let needsNetwork = type == "http" || !imageOrigins.isEmpty
    guard permissions.contains("shell") == needsShell else {
        throw fail(needsShell ? "shell 소스는 permissions에 \"shell\"이 있어야 한다" : "shell 소스가 아닌데 \"shell\" 권한을 선언했다")
    }
    // 공식 네이티브 aiUsage/apiUsage도 네트워크를 쓴다. 선언된 권한은 그대로 요약한다.
    guard official ? (!needsNetwork || permissions.contains("network")) : permissions.contains("network") == needsNetwork else {
        throw fail(needsNetwork ? "http 소스나 원격 이미지는 permissions에 \"network\"가 있어야 한다" : "네트워크를 쓰지 않는데 \"network\" 권한을 선언했다")
    }

    // market.json
    if let unknown = Set(market.keys).subtracting(market키).sorted().first {
        throw fail("market.json에 알 수 없는 키: \(unknown)")
    }
    guard let author = nonEmptyString(market["author"]) else { throw fail("market.json author가 없다") }
    guard let description = market["description"] as? [String: Any], Set(description.keys) == ["ko", "en"],
          let ko = nonEmptyString(description["ko"]), let en = nonEmptyString(description["en"]) else {
        throw fail("market.json description은 {\"ko\", \"en\"} 두 문자열이어야 한다")
    }
    guard let category = market["category"] as? String, category == manifestCategory else {
        throw fail("market.json category가 manifest.json category(\(manifestCategory))와 같아야 한다")
    }
    guard let marketVersion = market["version"] as? String, marketVersion == version else {
        throw fail("market.json version이 manifest.json version(\(version))과 같아야 한다")
    }
    guard let minAppVersion = market["minAppVersion"] as? String, isSemver(minAppVersion) else {
        throw fail("market.json minAppVersion은 MAJOR.MINOR.PATCH 형식이어야 한다")
    }
    guard let changelog = nonEmptyString(market["changelog"]) else { throw fail("market.json changelog가 없다") }
    var homepage: Any = NSNull()
    if let raw = market["homepage"] {
        guard let h = raw as? String else { throw fail("market.json homepage는 문자열이어야 한다") }
        _ = try literalHTTPSOrigin(h, what: "homepage")
        guard !hasTemplate(h) else { throw fail("homepage에는 템플릿을 쓸 수 없다") }
        homepage = h
    }

    var fileList: [[String: Any]] = []
    for entry in entries {
        guard let bytes = try? Data(contentsOf: entry.url), Int64(bytes.count) == entry.size else {
            throw fail("읽는 중에 파일이 바뀌었거나 읽을 수 없다: \(entry.path)")
        }
        let digest = SHA256.hash(data: bytes).map { String(format: "%02x", $0) }.joined()
        fileList.append(["path": entry.path, "size": entry.size, "sha256": digest])
    }

    var result: [String: Any] = [
        "id": id,
        "name": name,
        "description": ["ko": ko, "en": en],
        "author": author,
        "category": category,
        "version": version,
        "minAppVersion": minAppVersion,
        "permissions": permissions,
        "shell": shellArgv.map { ["argv": $0] as Any } ?? NSNull(),
        "httpOrigins": httpOrigins,
        "imageOrigins": imageOrigins.sorted(),
        "openURLOrigins": openURLOrigins.sorted(),
        "builtinKinds": builtinKinds,
        "files": fileList,
        "changelog": changelog,
        "homepage": homepage,
    ]
    // 생략된 distribution은 계속 community다. 기존 항목의 바이트 계약을 유지한다.
    if official || market["distribution"] != nil { result["distribution"] = distribution }
    return result
}

// MARK: - 실행

func serialize(_ document: [String: Any]) -> Data {
    var data = try! JSONSerialization.data(withJSONObject: document,
                                           options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes])
    data.append(0x0A)
    return data
}

let arguments = Array(CommandLine.arguments.dropFirst())
let mode = arguments.first ?? "--write"
guard ["--write", "--check", "--validate", "--renew"].contains(mode), arguments.count <= 1 else {
    FileHandle.standardError.write("사용법: swift tools/build-index.swift [--check | --validate | --renew]\n".data(using: .utf8)!)
    exit(2)
}

let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
let widgetsDir = root.appendingPathComponent("widgets")
let indexURL = root.appendingPathComponent("index.json")
var isDir: ObjCBool = false
guard FileManager.default.fileExists(atPath: widgetsDir.path, isDirectory: &isDir), isDir.boolValue else {
    FileHandle.standardError.write("widgets/ 폴더가 없다. 저장소 루트에서 실행하라.\n".data(using: .utf8)!)
    exit(2)
}

var problems: [String] = []
var widgets: [[String: Any]] = []
let names = ((try? FileManager.default.contentsOfDirectory(atPath: widgetsDir.path)) ?? [])
    .filter { $0 != ".DS_Store" && $0 != ".gitkeep" }
    .sorted { Array($0.utf8).lexicographicallyPrecedes(Array($1.utf8)) }
for name in names {
    do {
        widgets.append(try buildEntry(folder: widgetsDir.appendingPathComponent(name), folderName: name))
    } catch let v as Violation {
        problems.append("widgets/\(name): \(v.message)")
    } catch {
        problems.append("widgets/\(name): \(error)")
    }
}

guard problems.isEmpty else {
    for p in problems { FileHandle.standardError.write("✗ \(p)\n".data(using: .utf8)!) }
    FileHandle.standardError.write("규칙 위반 \(problems.count)건\n".data(using: .utf8)!)
    exit(1)
}
print("✓ 위젯 \(widgets.count)개 검사 통과")
if mode == "--validate" { exit(0) }

let formatter = ISO8601DateFormatter()
formatter.timeZone = TimeZone(identifier: "UTC")
formatter.formatOptions = [.withInternetDateTime]

/// `expiresAt`은 `generatedAt`에서만 나온다. 그래서 `--check`가 두 값을 따로 믿지 않고
/// 다시 계산해 바이트로 비교할 수 있다(손으로 늘린 만료는 걸린다).
func document(generatedAt: String, sequence: Int) -> [String: Any]? {
    guard let generated = formatter.date(from: generatedAt) else { return nil }
    return ["schemaVersion": 1, "generatedAt": generatedAt,
            "expiresAt": formatter.string(from: generated.addingTimeInterval(유효기간)),
            "sequence": sequence, "widgets": widgets]
}

var previous: (data: Data, generatedAt: String, sequence: Int)?
if let data = try? Data(contentsOf: indexURL) {
    guard let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let generatedAt = object["generatedAt"] as? String, formatter.date(from: generatedAt) != nil,
          let seq = object["sequence"] as? NSNumber, !isBool(seq), seq.doubleValue >= 1,
          seq.doubleValue == seq.doubleValue.rounded() else {
        FileHandle.standardError.write("✗ 기존 index.json을 읽을 수 없다(generatedAt·sequence 확인)\n".data(using: .utf8)!)
        exit(1)
    }
    previous = (data, generatedAt, seq.intValue)
}

if mode == "--check" {
    guard let previous else {
        FileHandle.standardError.write("✗ index.json이 없다. swift tools/build-index.swift를 실행하라.\n".data(using: .utf8)!)
        exit(1)
    }
    let expected = serialize(document(generatedAt: previous.generatedAt, sequence: previous.sequence)!)
    guard expected == previous.data else {
        FileHandle.standardError.write("✗ index.json이 widgets/와 다르다(expiresAt 포함). build-index → sign-index를 실행해 커밋하라.\n".data(using: .utf8)!)
        exit(1)
    }
    print("✓ index.json이 widgets/와 일치한다(sequence \(previous.sequence))")
    let expires = formatter.date(from: previous.generatedAt)!.addingTimeInterval(유효기간)
    let 남은일 = Int(expires.timeIntervalSinceNow / 86_400)
    guard expires.timeIntervalSinceNow > TimeInterval(갱신여유일 * 86_400) else {
        FileHandle.standardError.write("✗ index.json이 \(formatter.string(from: expires))에 만료된다(\(남은일)일 남음). swift tools/build-index.swift --renew → sign-index를 실행해 커밋하라.\n".data(using: .utf8)!)
        exit(1)
    }
    print("✓ 만료 \(formatter.string(from: expires)) (\(남은일)일 남음)")
    exit(0)
}

if mode != "--renew", let previous,
   serialize(document(generatedAt: previous.generatedAt, sequence: previous.sequence)!) == previous.data {
    print("변경 없음: index.json을 그대로 둔다(sequence \(previous.sequence))")
    exit(0)
}
let sequence = (previous?.sequence ?? 0) + 1
let output = serialize(document(generatedAt: formatter.string(from: Date()), sequence: sequence)!)
do {
    try output.write(to: indexURL, options: .atomic)
} catch {
    FileHandle.standardError.write("✗ index.json을 쓸 수 없다: \(error)\n".data(using: .utf8)!)
    exit(1)
}
print("✓ index.json을 썼다(sequence \(sequence)). 이어서 swift tools/sign-index.swift로 서명하라.")
