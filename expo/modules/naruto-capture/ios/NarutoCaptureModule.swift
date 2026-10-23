import ExpoModulesCore
import ReplayKit
import UIKit

public final class NarutoCaptureModule: Module {
  private let documents = CaptureDocuments()

  public func definition() -> ModuleDefinition {
    Name("NarutoCapture")
    Events("onState")
    OnCreate { [weak self] in
      DispatchQueue.main.async {
        CaptureHost.shared.onState = { [weak self] state in self?.sendEvent("onState", state) }
      }
    }
    Function("getState") { () -> [String: Any] in
      if Thread.isMainThread { return CaptureHost.shared.snapshot() }
      return DispatchQueue.main.sync { CaptureHost.shared.snapshot() }
    }
    AsyncFunction("prepare") { () throws -> [String: Any] in
      try CaptureHost.shared.prepare()
    }.runOnQueue(.main)
    AsyncFunction("startPictureInPicture") { () throws -> [String: Any] in
      try CaptureHost.shared.startPictureInPicture()
    }.runOnQueue(.main)
    AsyncFunction("stop") { () -> [String: Any] in
      CaptureHost.shared.stop()
      return CaptureHost.shared.snapshot()
    }.runOnQueue(.main)
    AsyncFunction("setSettings") { (settings: [String: Any]) -> [String: Any] in
      CaptureHost.shared.setSettings(settings)
    }.runOnQueue(.main)
    AsyncFunction("setControls") { (patch: [String: Any]) -> [String: Any] in
      CaptureHost.shared.setControls(patch)
    }.runOnQueue(.main)
    AsyncFunction("pickCalibrationImage") { (promise: Promise) in
      self.documents.pick(image: true, promise: promise)
    }.runOnQueue(.main)
    AsyncFunction("importCalibration") { (promise: Promise) in
      self.documents.pick(image: false, promise: promise)
    }.runOnQueue(.main)
    AsyncFunction("shareDocument") { (name: String, text: String, promise: Promise) in
      self.documents.share(name: name, text: text, promise: promise)
    }.runOnQueue(.main)
    View(NarutoCaptureView.self) {}
  }
}

final class NarutoCaptureView: ExpoView {
  private let picker = RPSystemBroadcastPickerView(frame: .zero)
  private let hint = UILabel()

  required init(appContext: AppContext? = nil) {
    super.init(appContext: appContext)
    clipsToBounds = true
    backgroundColor = .black
    layer.cornerRadius = 12
    picker.preferredExtension = CaptureHost.broadcastExtensionIdentifier
    picker.showsMicrophoneButton = false
    picker.accessibilityLabel = "选择替身计时器，开始系统录屏"
    addSubview(picker)
    hint.text = "系统录屏"
    hint.textColor = .white
    hint.textAlignment = .center
    hint.font = .systemFont(ofSize: 12)
    addSubview(hint)
  }

  override func didMoveToWindow() {
    super.didMoveToWindow()
    if window != nil {
      layer.insertSublayer(CaptureHost.shared.renderer.layer, at: 0)
      setNeedsLayout()
    }
  }

  override func layoutSubviews() {
    super.layoutSubviews()
    let buttonWidth: CGFloat = 74
    CATransaction.begin()
    CATransaction.setDisableActions(true)
    CaptureHost.shared.renderer.layer.frame = CGRect(x: 0, y: 0, width: max(1, bounds.width - buttonWidth), height: bounds.height)
    CATransaction.commit()
    picker.frame = CGRect(x: bounds.width - buttonWidth + 7, y: max(0, (bounds.height - 70) / 2), width: 60, height: 50)
    hint.frame = CGRect(x: bounds.width - buttonWidth, y: picker.frame.maxY, width: buttonWidth, height: 20)
  }
}
