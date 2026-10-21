package com.narutotimer.update

/**
 * What a downloaded APK must satisfy before it is handed to the system installer (audit UR-04;
 * pure Kotlin, checked on the host by android/tools/appcheck). The release's SHA-256 only proves
 * the file is the one the release lists; this ties it to the running app:
 *
 * 1. the same package name (no other app through the trusted "下载并安装" flow);
 * 2. the same signing certificate(s) as the running app — or, for a single-signer APK whose
 *    signing key was rotated (APK Signature Scheme v3), a proven rotation history containing
 *    the running app's certificate, which Android itself accepts as an update;
 * 3. a higher versionCode than the installed one (no reinstall or downgrade of an older build).
 *
 * Certificates are compared as lowercase hex SHA-256 of their DER encoding.
 */
object ApkCheck {
    class Archive(
        val packageName: String,
        val versionCode: Long,
        /** Certificates that signed the APK contents (SigningInfo.apkContentsSigners). */
        val signers: Set<String>,
        /** Rotation history, oldest first (SigningInfo.signingCertificateHistory); empty if none or multi-signer. */
        val signerHistory: List<String> = emptyList(),
    )

    class Installed(val packageName: String, val versionCode: Long, val signers: Set<String>)

    /** Null = OK to install; otherwise the reason, in Chinese, for the user. [archive] null = could not be parsed. */
    fun problem(archive: Archive?, installed: Installed): String? {
        if (archive == null) return "无法解析下载的安装包（文件损坏或签名无效），已拒绝安装。"
        if (archive.packageName != installed.packageName) {
            return "下载的安装包不是本应用（包名 ${archive.packageName.ifEmpty { "未知" }}，应为 ${installed.packageName}），已拒绝安装。"
        }
        if (installed.signers.isEmpty()) return "无法读取本应用的签名证书，无法确认更新包来源，已拒绝安装。"
        if (archive.signers.isEmpty()) return "安装包没有可验证的签名证书，已拒绝安装。"
        if (!sameSigner(archive, installed)) {
            return "安装包的签名证书与当前应用不一致，可能不是官方发布的版本，已拒绝安装。"
        }
        if (archive.versionCode <= installed.versionCode) {
            return "安装包的版本号（versionCode ${archive.versionCode}）不高于当前版本（versionCode ${installed.versionCode}），已拒绝安装。"
        }
        return null
    }

    private fun sameSigner(archive: Archive, installed: Installed): Boolean {
        if (archive.signers == installed.signers) return true
        // Key rotation: one new signer that proves (signed lineage) it succeeds our current one.
        return archive.signers.size == 1 && installed.signers.size == 1 &&
            archive.signerHistory.size > 1 && archive.signerHistory.last() in archive.signers &&
            installed.signers.first() in archive.signerHistory
    }
}
