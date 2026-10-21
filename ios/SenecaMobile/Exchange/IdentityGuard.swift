import Foundation
import Security

/// Where this iPhone remembers which device identity is its own: somewhere a
/// backup restored onto another iPhone does not bring along.
protocol IdentityVault: Sendable {
    /// The device identity this iPhone holds in `journal`.
    func read(journal: String) -> String?
    /// Stores the identity, or forgets it. Returns whether it was kept.
    @discardableResult
    func write(journal: String, device: String?) -> Bool
}

/// The keychain, readable only on this iPhone: an item marked "this device
/// only" is not carried to another iPhone by a backup or a transfer, while
/// the journal and its sync history in the app's container are.
struct KeychainIdentityVault: IdentityVault {
    private let service = "io.sglavoie.seneca.sync-identity"

    private func query(_ journal: String) -> [String: Any] {
        [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: journal]
    }

    func read(journal: String) -> String? {
        var lookup = query(journal)
        lookup[kSecReturnData as String] = true
        lookup[kSecMatchLimit as String] = kSecMatchLimitOne
        var found: CFTypeRef?
        guard SecItemCopyMatching(lookup as CFDictionary, &found) == errSecSuccess, let data = found as? Data else {
            return nil
        }
        return String(decoding: data, as: UTF8.self)
    }

    @discardableResult
    func write(journal: String, device: String?) -> Bool {
        SecItemDelete(query(journal) as CFDictionary)
        guard let device else { return true }
        var item = query(journal)
        item[kSecValueData as String] = Data(device.utf8)
        item[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        return SecItemAdd(item as CFDictionary, nil) == errSecSuccess
    }
}

/// Keeps a copy of another device's journal from speaking as that device.
///
/// An iPhone restored from a backup, or set up from an old one, gets the
/// app's container whole: the journal and the sync history with the old
/// device's identity in it. Two devices exchanging under one identity would
/// each publish changes the other never made under the same numbers. So the
/// identity a device enrolled or joined with is also kept in the keychain,
/// on this iPhone only. A sync history whose identity the keychain does not
/// hold is a copy: it is not exchanged until the author rejoins as this
/// iPhone, which keeps every word and takes a new identity.
///
/// The marker file says the guard was already in place; it travels with the
/// container, so an installation from before the guard adopts its own
/// identity once, while a copy restored elsewhere is caught.
struct IdentityGuard: Sendable {
    let vault: any IdentityVault
    let marker: URL

    /// Whether this iPhone may exchange as `device` of `journal`.
    func accepts(journal: String, device: String) -> Bool {
        let own = vault.read(journal: journal)
        if own == device {
            return true
        }
        let guarded = FileManager.default.fileExists(atPath: marker.path)
        if own == nil && !guarded {
            // The first exchange since the guard arrived, on the iPhone that
            // enrolled. A keychain that keeps nothing never blocks anyone.
            adopt(journal: journal, device: device)
            return true
        }
        return false
    }

    /// Records `device` as this iPhone's own, after enrolling or joining.
    func adopt(journal: String, device: String) {
        guard vault.write(journal: journal, device: device) else { return }
        try? FileManager.default.createDirectory(at: marker.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? Data("guarded\n".utf8).write(to: marker, options: .atomic)
    }

    /// Forgets the identity, after leaving sync.
    func release(journal: String) {
        vault.write(journal: journal, device: nil)
    }
}
