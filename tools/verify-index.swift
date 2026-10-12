// index.json.sig와 index.json에 적힌 모든 파일을 앱이 하듯 확인한다.
//
//   swift tools/verify-index.swift <공개키 base64>
//
// 1) index.json 바이트에 대한 Ed25519 서명  2) 위젯마다 각 파일의 크기와 SHA-256
// 3) widgets/<id>/ 안에 목록에 없는 파일이 없는지(.DS_Store 제외)
import CryptoKit
import Darwin
import Foundation

func die(_ message: String) -> Never {
    FileHandle.standardError.write("✗ \(message)\n".data(using: .utf8)!)
    exit(1)
}

let arguments = Array(CommandLine.arguments.dropFirst())
guard arguments.count == 1 else { die("사용법: swift tools/verify-index.swift <공개키 base64>") }
guard let keyData = Data(base64Encoded: arguments[0]), keyData.count == 32,
      let publicKey = try? Curve25519.Signing.PublicKey(rawRepresentation: keyData) else {
    die("공개키가 32바이트 Ed25519 raw 값의 base64가 아니다")
}

let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
guard let index = try? Data(contentsOf: root.appendingPathComponent("index.json")) else { die("index.json이 없다") }
guard let sigText = try? String(contentsOf: root.appendingPathComponent("index.json.sig"), encoding: .utf8),
      let signature = Data(base64Encoded: sigText.trimmingCharacters(in: .whitespacesAndNewlines)) else {
    die("index.json.sig가 없거나 base64가 아니다")
}
guard publicKey.isValidSignature(signature, for: index) else { die("서명이 맞지 않는다") }
print("✓ 서명 확인")

guard let object = try? JSONSerialization.jsonObject(with: index) as? [String: Any],
      object["schemaVersion"] as? Int == 1, let sequence = object["sequence"] as? Int,
      let widgets = object["widgets"] as? [[String: Any]] else { die("index.json 형식이 맞지 않는다") }

func safe(_ component: String) -> Bool {
    component.range(of: #"^[A-Za-z0-9_-][A-Za-z0-9._-]*\z"#, options: .regularExpression) != nil
}

var problems: [String] = []
var fileCount = 0
for widget in widgets {
    guard let id = widget["id"] as? String, safe(id), let files = widget["files"] as? [[String: Any]] else {
        problems.append("id나 files가 없는 항목")
        continue
    }
    let folder = root.appendingPathComponent("widgets").appendingPathComponent(id)
    var listed = Set<String>()
    for file in files {
        guard let path = file["path"] as? String, let size = file["size"] as? Int, let sha = file["sha256"] as? String,
              path.split(separator: "/", omittingEmptySubsequences: false).allSatisfy({ safe(String($0)) }) else {
            problems.append("\(id): 파일 항목 형식이 맞지 않는다")
            continue
        }
        listed.insert(path)
        var info = stat()
        let url = folder.appendingPathComponent(path)
        guard lstat(url.path, &info) == 0, (info.st_mode & S_IFMT) == S_IFREG, let data = try? Data(contentsOf: url) else {
            problems.append("\(id)/\(path): 일반 파일이 아니거나 없다")
            continue
        }
        let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        if data.count != size { problems.append("\(id)/\(path): 크기가 다르다(\(data.count) ≠ \(size))") }
        if digest != sha { problems.append("\(id)/\(path): SHA-256이 다르다") }
        fileCount += 1
    }
    if let enumerator = FileManager.default.enumerator(atPath: folder.path) {
        for case let rel as String in enumerator {
            var isDir: ObjCBool = false
            FileManager.default.fileExists(atPath: folder.appendingPathComponent(rel).path, isDirectory: &isDir)
            if !isDir.boolValue, (rel as NSString).lastPathComponent != ".DS_Store", !listed.contains(rel) {
                problems.append("\(id)/\(rel): index.json에 없는 파일이다")
            }
        }
    }
}
guard problems.isEmpty else {
    for p in problems { FileHandle.standardError.write("✗ \(p)\n".data(using: .utf8)!) }
    exit(1)
}
print("✓ 위젯 \(widgets.count)개, 파일 \(fileCount)개의 크기·SHA-256 확인(sequence \(sequence))")
