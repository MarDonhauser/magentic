// Verify the release key matches the public key embedded in all installations.
// Only the file path and public key are arguments; never log the private seed.
import Foundation
import CryptoKit

do {
    let encoded = try String(contentsOfFile: CommandLine.arguments[1], encoding: .utf8)
        .trimmingCharacters(in: .whitespacesAndNewlines)
    guard let seed = Data(base64Encoded: encoded), seed.count == 32,
          let expected = Data(base64Encoded: CommandLine.arguments[2]), expected.count == 32 else {
        throw NSError(domain: "UpdateKey", code: 1)
    }
    let key = try Curve25519.Signing.PrivateKey(rawRepresentation: seed)
    guard key.publicKey.rawRepresentation == expected else {
        throw NSError(domain: "UpdateKey", code: 2)
    }
} catch {
    fputs("Update-Schlüssel ungültig oder öffentlicher und privater Schlüssel passen nicht zusammen.\n", stderr)
    exit(1)
}
