// index 서명 키를 이 Mac의 로그인 Keychain에 만들거나(없을 때만) 다시 쓰고, 공개키만 출력한다.
//
//   swift tools/keygen.swift
//
// 비밀키는 출력하지도, 파일로 쓰지도 않는다. Keychain 항목:
//   종류 generic password, service com.betterdock.market-signing, account index-signing-key-v1,
//   값 = Ed25519 비밀키 rawRepresentation(32바이트)
// 이미 있으면 덮어쓰지 않고 그 키를 쓴다.
import CryptoKit
import Foundation
import Security

let service = "com.betterdock.market-signing"
let account = "index-signing-key-v1"

func die(_ message: String) -> Never {
    FileHandle.standardError.write("✗ \(message)\n".data(using: .utf8)!)
    exit(1)
}

func loadKey() -> Curve25519.Signing.PrivateKey? {
    let query: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: service,
        kSecAttrAccount as String: account,
        kSecReturnData as String: true,
        kSecMatchLimit as String: kSecMatchLimitOne,
    ]
    var result: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    if status == errSecItemNotFound { return nil }
    guard status == errSecSuccess, let data = result as? Data else { die("Keychain을 읽지 못했다(OSStatus \(status))") }
    guard data.count == 32, let key = try? Curve25519.Signing.PrivateKey(rawRepresentation: data) else {
        die("Keychain 항목이 32바이트 Ed25519 키가 아니다. 덮어쓰지 않고 멈춘다.")
    }
    return key
}

let key: Curve25519.Signing.PrivateKey
if let existing = loadKey() {
    key = existing
    print("기존 Keychain 키를 쓴다(\(service) / \(account)).")
} else {
    let fresh = Curve25519.Signing.PrivateKey()
    let attributes: [String: Any] = [
        kSecClass as String: kSecClassGenericPassword,
        kSecAttrService as String: service,
        kSecAttrAccount as String: account,
        kSecAttrLabel as String: "betterDock 위젯 마켓 index 서명 키",
        kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
        kSecAttrSynchronizable as String: false,
        kSecValueData as String: fresh.rawRepresentation,
    ]
    let status = SecItemAdd(attributes as CFDictionary, nil)
    guard status == errSecSuccess else { die("Keychain에 저장하지 못했다(OSStatus \(status))") }
    // 저장한 것을 다시 읽어 같은 키인지 확인한다.
    guard let stored = loadKey(), stored.publicKey.rawRepresentation == fresh.publicKey.rawRepresentation else {
        die("저장한 키를 다시 읽지 못했다")
    }
    key = stored
    print("새 키를 만들어 Keychain에 저장했다(\(service) / \(account)).")
}

print("공개키(base64, Ed25519 raw 32바이트):")
print(key.publicKey.rawRepresentation.base64EncodedString())
