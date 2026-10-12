// index.json을 Keychain의 비밀키로 서명해 index.json.sig(base64, 줄바꿈 없음)를 쓴다.
//
//   swift tools/sign-index.swift
//
// 서명은 index.json 파일의 **바이트 그대로**에 대해 한다. 쓰기 전에 공개키로 검증한다.
// 비밀키는 Keychain에서 읽어 메모리에서만 쓴다(swift tools/keygen.swift가 만든 것).
import CryptoKit
import Foundation
import Security

let service = "com.betterdock.market-signing"
let account = "index-signing-key-v1"

func die(_ message: String) -> Never {
    FileHandle.standardError.write("✗ \(message)\n".data(using: .utf8)!)
    exit(1)
}

let query: [String: Any] = [
    kSecClass as String: kSecClassGenericPassword,
    kSecAttrService as String: service,
    kSecAttrAccount as String: account,
    kSecReturnData as String: true,
    kSecMatchLimit as String: kSecMatchLimitOne,
]
var result: CFTypeRef?
let status = SecItemCopyMatching(query as CFDictionary, &result)
guard status != errSecItemNotFound else { die("서명 키가 없다. 먼저 swift tools/keygen.swift를 실행하라.") }
guard status == errSecSuccess, let raw = result as? Data, raw.count == 32,
      let key = try? Curve25519.Signing.PrivateKey(rawRepresentation: raw) else {
    die("Keychain의 서명 키를 읽지 못했다(OSStatus \(status))")
}

let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
let indexURL = root.appendingPathComponent("index.json")
let sigURL = root.appendingPathComponent("index.json.sig")
guard let index = try? Data(contentsOf: indexURL) else { die("index.json이 없다. 저장소 루트에서 실행하라.") }
guard let object = try? JSONSerialization.jsonObject(with: index) as? [String: Any],
      object["schemaVersion"] as? Int == 1, object["sequence"] is NSNumber else {
    die("index.json 형식이 맞지 않는다. swift tools/build-index.swift로 다시 만들어라.")
}

guard let signature = try? key.signature(for: index) else { die("서명하지 못했다") }
guard key.publicKey.isValidSignature(signature, for: index) else { die("방금 만든 서명이 검증되지 않는다") }
do {
    try Data(signature.base64EncodedString().utf8).write(to: sigURL, options: .atomic)
} catch {
    die("index.json.sig를 쓰지 못했다: \(error)")
}
print("✓ index.json.sig를 썼다(sequence \(object["sequence"]!)).")
print("공개키: \(key.publicKey.rawRepresentation.base64EncodedString())")
