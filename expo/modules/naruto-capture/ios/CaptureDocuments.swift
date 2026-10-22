import ExpoModulesCore
import ImageIO
import UniformTypeIdentifiers
import UIKit

// Files picker grants scoped access to only the user-selected document. No photo
// library entitlement, no full-library permission, and no IPC image transfer.
final class CaptureDocuments: NSObject, UIDocumentPickerDelegate {
  private var pending: Promise?
  private var image = false

  private func presenter() -> UIViewController? {
    guard UIApplication.shared.applicationState == .active else { return nil }
    let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
    var controller = scenes.first(where: { $0.activationState == .foregroundActive })?
      .windows.first(where: { $0.isKeyWindow })?.rootViewController
    while let next = controller?.presentedViewController { controller = next }
    return controller
  }

  func pick(image: Bool, promise: Promise) {
    guard pending == nil, let controller = presenter() else {
      promise.reject("ERR_DOCUMENT_BUSY", "请返回 App，并关闭当前文件选择器后重试。")
      return
    }
    self.image = image
    pending = promise
    let picker = UIDocumentPickerViewController(forOpeningContentTypes: image ? [.image] : [.json], asCopy: true)
    picker.allowsMultipleSelection = false
    picker.delegate = self
    controller.present(picker, animated: true)
  }

  func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
    pending?.reject("ERR_DOCUMENT_CANCELLED", "已取消选择。")
    pending = nil
  }

  func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
    guard let promise = pending else { return }
    pending = nil
    guard let url = urls.first else { promise.reject("ERR_DOCUMENT_EMPTY", "没有选择文件。"); return }
    let scoped = url.startAccessingSecurityScopedResource()
    defer { if scoped { url.stopAccessingSecurityScopedResource() } }
    do {
      let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
      if image {
        guard size > 0, size <= 64 * 1024 * 1024,
              let source = CGImageSourceCreateWithURL(url as CFURL, [kCGImageSourceShouldCache: false] as CFDictionary),
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = properties[kCGImagePropertyPixelWidth] as? NSNumber,
              let height = properties[kCGImagePropertyPixelHeight] as? NSNumber,
              width.doubleValue > 0, height.doubleValue > 0,
              width.doubleValue * height.doubleValue <= 100_000_000 else {
          promise.reject("ERR_IMAGE_INVALID", "请选择不超过 64 MiB 的有效截图。"); return
        }
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent("NarutoCalibration", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        // Only the latest selection is used by the editor; keep storage bounded.
        for file in (try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)) ?? [] {
          try? FileManager.default.removeItem(at: file)
        }
        let target = directory.appendingPathComponent(UUID().uuidString).appendingPathExtension(url.pathExtension)
        try FileManager.default.copyItem(at: url, to: target)
        let orientation = (properties[kCGImagePropertyOrientation] as? NSNumber)?.intValue ?? 1
        let swapped = (5...8).contains(orientation)
        promise.resolve(["uri": target.absoluteString,
                         "width": swapped ? height.intValue : width.intValue,
                         "height": swapped ? width.intValue : height.intValue])
      } else {
        // Read at most limit+1 even if the provider changes file size after stat.
        guard size <= 65_536, let stream = InputStream(url: url) else {
          promise.reject("ERR_DOCUMENT_SIZE", "校准 JSON 不得超过 64 KiB。"); return
        }
        stream.open(); defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while data.count <= 65_536 {
          let count = stream.read(&buffer, maxLength: min(buffer.count, 65_537 - data.count))
          if count < 0 { throw stream.streamError ?? CocoaError(.fileReadUnknown) }
          if count == 0 { break }
          data.append(contentsOf: buffer.prefix(count))
        }
        guard data.count <= 65_536, let text = String(data: data, encoding: .utf8),
              (try? JSONSerialization.jsonObject(with: data)) != nil else {
          promise.reject("ERR_DOCUMENT_INVALID", "请选择不超过 64 KiB 的 UTF-8 JSON 文件。"); return
        }
        promise.resolve(text)
      }
    } catch { promise.reject("ERR_DOCUMENT_READ", error.localizedDescription) }
  }

  func share(name: String, text: String, promise: Promise) {
    guard let controller = presenter(), pending == nil else {
      promise.reject("ERR_DOCUMENT_BUSY", "请返回 App 并关闭其他弹窗。"); return
    }
    guard let data = text.data(using: .utf8), data.count <= 1_048_576 else {
      promise.reject("ERR_DOCUMENT_SIZE", "导出内容不得超过 1 MiB。"); return
    }
    let safe = String(String.UnicodeScalarView(name.unicodeScalars.filter {
      CharacterSet.alphanumerics.contains($0) || "-_.".unicodeScalars.contains($0)
    }.prefix(100)))
    do {
      let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
      try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
      let file = directory.appendingPathComponent(safe.isEmpty || safe == "." || safe == ".." ? "naruto-diagnostics.json" : safe)
      try data.write(to: file, options: .atomic)
      let sheet = UIActivityViewController(activityItems: [file], applicationActivities: nil)
      sheet.popoverPresentationController?.sourceView = controller.view
      sheet.popoverPresentationController?.sourceRect = CGRect(x: controller.view.bounds.midX, y: controller.view.bounds.midY, width: 1, height: 1)
      sheet.completionWithItemsHandler = { _, _, _, error in
        try? FileManager.default.removeItem(at: directory)
        if let error { promise.reject("ERR_DOCUMENT_SHARE", error.localizedDescription) }
        else { promise.resolve(nil) }
      }
      controller.present(sheet, animated: true)
    } catch { promise.reject("ERR_DOCUMENT_WRITE", error.localizedDescription) }
  }
}
