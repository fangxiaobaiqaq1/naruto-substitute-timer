import AVFoundation
import AVKit
import Network
import UIKit

// All host state, sockets, PiP callbacks and rendering are confined to main.
// PiP's actual video samples are the background workload; no silent audio,
// background task loop or JS timer is used to keep this process alive.
final class CaptureHost: NSObject, AVPictureInPictureControllerDelegate, AVPictureInPictureSampleBufferPlaybackDelegate {
  static let shared = CaptureHost()
  static var broadcastExtensionIdentifier: String? {
    guard let directory = Bundle.main.builtInPlugInsURL,
          let files = try? FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil) else { return nil }
    let identifiers = files.sorted { $0.lastPathComponent < $1.lastPathComponent }.compactMap { url -> String? in
      guard url.pathExtension == "appex", let bundle = Bundle(url: url),
            let info = bundle.infoDictionary?["NSExtension"] as? [String: Any],
            info["NSExtensionPointIdentifier"] as? String == "com.apple.broadcast-services-upload",
            let attributes = info["NSExtensionAttributes"] as? [String: Any],
            attributes["RPBroadcastProcessMode"] as? String == "RPBroadcastProcessModeSampleBuffer",
            let executable = bundle.executableURL, FileManager.default.fileExists(atPath: executable.path) else { return nil }
      return bundle.bundleIdentifier
    }
    let configured = Bundle.main.object(forInfoDictionaryKey: "NRTBroadcastExtensionIdentifier") as? String
    // Re-signing may change both bundle IDs without rewriting our custom key.
    // The actual embedded bundle is authoritative; never pick a nonexistent ID.
    return identifiers.first(where: { $0 == configured }) ?? identifiers.first
  }
  let renderer = CountdownRenderer()
  var onState: (([String: Any]) -> Void)?
  private var pip: AVPictureInPictureController?
  private var possibleObservation: NSKeyValueObservation?
  private var timer: DispatchSourceTimer?
  private var listener: NWListener?
  private var client: NWConnection?
  private var input = Data()
  private var session = ""
  private var broadcastID = ""
  private var receivedSequence = 0
  private var sentSequence = 0
  private var lastPacket = 0.0
  private var lastFrame = 0.0
  private var lastEmit = 0.0
  private var lastRender = 0.0
  private var pendingUntil = 0.0
  private var startIssued = false
  private var stopInProgress = false
  private var deadlines = Array(repeating: CaptureDeadline(), count: 4)
  private var settings = CaptureProtocol.settings([:])
  private var state: [String: Any] = [
    "available": true, "prepared": false, "broadcastActive": false,
    "pipSupported": false, "pipPossible": false, "pipPending": false, "pipActive": false,
    "connected": false, "message": "请先准备，再开启画中画与系统录屏。",
    "leftDeadlineMs": 0, "rightDeadlineMs": 0,
    "leftAlternateDeadlineMs": 0, "rightAlternateDeadlineMs": 0,
    "leftCount": -1, "rightCount": -1, "detectedPlayerSide": "", "match": false,
    "frames": 0, "lastFrameAtMs": 0, "leftNinja": "", "rightNinja": "",
    "leftSubstitutions": 0, "rightSubstitutions": 0, "scene": "", "latencyMs": 0,
    "frameWidth": 0, "frameHeight": 0, "stale": true, "renderedFrames": 0
  ]
  private static let deadlineKeys = ["leftDeadlineMs", "rightDeadlineMs", "leftAlternateDeadlineMs", "rightAlternateDeadlineMs"]

  private override init() {
    super.init()
    state["pipSupported"] = AVPictureInPictureController.isPictureInPictureSupported()
    if Self.broadcastExtensionIdentifier == nil {
      state["available"] = false
      state["message"] = "安装包缺少有效的系统录屏扩展，请安装完整 IPA。"
    }
    NotificationCenter.default.addObserver(self, selector: #selector(background), name: UIApplication.didEnterBackgroundNotification, object: nil)
    NotificationCenter.default.addObserver(self, selector: #selector(foreground), name: UIApplication.willEnterForegroundNotification, object: nil)
    NotificationCenter.default.addObserver(self, selector: #selector(audioInterrupted(_:)), name: AVAudioSession.interruptionNotification, object: nil)
    NotificationCenter.default.addObserver(self, selector: #selector(mediaReset), name: AVAudioSession.mediaServicesWereResetNotification, object: nil)
  }

  func snapshot() -> [String: Any] { state }
  private func emit() { onState?(state) }
  private func message(_ value: String) { state["message"] = value; emit() }

  func prepare() throws -> [String: Any] {
    guard Self.broadcastExtensionIdentifier != nil else {
      state["available"] = false
      message("安装包缺少有效的系统录屏扩展，请安装完整 IPA。")
      throw NSError(domain: "NarutoCapture", code: 3, userInfo: [NSLocalizedDescriptionKey: state["message"] as? String ?? "录屏扩展缺失"])
    }
    guard !stopInProgress else {
      throw NSError(domain: "NarutoCapture", code: 2, userInfo: [NSLocalizedDescriptionKey: "画中画正在关闭，请稍后再试。"])
    }
    guard AVPictureInPictureController.isPictureInPictureSupported() else {
      message("此设备不支持画中画；不能保证切到游戏后继续计时。")
      return state
    }
    guard listener == nil else { return state }
    guard UIApplication.shared.applicationState == .active else {
      throw NSError(domain: "NarutoCapture", code: 4, userInfo: [NSLocalizedDescriptionKey: "请在 App 前台准备画中画。"])
    }
    let audio = AVAudioSession.sharedInstance()
    try audio.setCategory(.playback, mode: .moviePlayback, options: [.mixWithOthers])
    try audio.setActive(true)
    let parameters = NWParameters.tcp
    parameters.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: CaptureProtocol.port)!)
    parameters.allowLocalEndpointReuse = true
    parameters.acceptLocalOnly = true
    parameters.includePeerToPeer = false
    let server: NWListener
    do { server = try NWListener(using: parameters) }
    catch {
      try? audio.setActive(false, options: [.notifyOthersOnDeactivation])
      throw error
    }
    listener = server
    server.stateUpdateHandler = { [weak self, weak server] status in
      guard let self, let server, self.listener === server else { return }
      switch status {
      case .ready:
        self.state["prepared"] = true
        self.message("已准备：先开启画中画，再点系统录屏按钮选择替身计时器。")
      case .failed(let error), .waiting(let error):
        self.stop(reason: "本地采集监听失败：\(error.localizedDescription)")
      default: break
      }
    }
    server.newConnectionHandler = { [weak self] in self?.accept($0) }
    server.start(queue: .main)
    if pip == nil {
      let source = AVPictureInPictureController.ContentSource(sampleBufferDisplayLayer: renderer.layer, playbackDelegate: self)
      let controller = AVPictureInPictureController(contentSource: source)
      controller.delegate = self
      controller.canStartPictureInPictureAutomaticallyFromInline = false
      controller.requiresLinearPlayback = true
      pip = controller
      possibleObservation = controller.observe(\.isPictureInPicturePossible, options: [.initial, .new]) { [weak self] _, _ in
        DispatchQueue.main.async { self?.updatePossible() }
      }
    }
    if timer == nil {
      let source = DispatchSource.makeTimerSource(queue: .main)
      source.schedule(deadline: .now(), repeating: 1 / CaptureProtocol.number(settings["backgroundFPS"], 8), leeway: .milliseconds(15))
      source.setEventHandler { [weak self] in self?.tick() }
      timer = source
      source.resume()
    }
    render()
    return state
  }

  func startPictureInPicture() throws -> [String: Any] {
    _ = try prepare()
    guard let pip, !pip.isPictureInPictureActive, !startIssued else { return state }
    guard UIApplication.shared.applicationState == .active else {
      message("请回到 App 前台开启画中画。")
      return state
    }
    pendingUntil = CaptureProtocol.now() + 8
    state["pipPending"] = true
    message("正在等待系统允许画中画；请保持预览可见。")
    render()
    updatePossible()
    return state
  }

  private func updatePossible() {
    state["pipPossible"] = pip?.isPictureInPicturePossible ?? false
    if !startIssued, pendingUntil > CaptureProtocol.now(), pip?.isPictureInPicturePossible == true,
       UIApplication.shared.applicationState == .active {
      // Mark before calling: KVO/delegate callbacks may reenter. Keep the
      // deadline until the didStart/failed callback so pending is never endless.
      startIssued = true
      pip?.startPictureInPicture()
    }
    emit()
  }

  func setSettings(_ value: [String: Any]) -> [String: Any] {
    settings = CaptureProtocol.settings(value)
    timer?.schedule(deadline: .now(), repeating: 1 / CaptureProtocol.number(settings["backgroundFPS"], 8), leeway: .milliseconds(15))
    sendConfiguration()
    render()
    return state
  }

  func stop(reason: String = "采集已停止；可重新准备。") {
    send(["type": "stop"])
    let old = client
    client = nil
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { old?.cancel() }
    listener?.cancel(); listener = nil
    timer?.cancel(); timer = nil
    pendingUntil = 0
    startIssued = false
    state["prepared"] = false
    state["pipPending"] = false
    invalidateEvidence()
    state["broadcastActive"] = false
    state["connected"] = false
    state["message"] = reason
    if pip?.isPictureInPictureActive == true {
      stopInProgress = true
      pip?.stopPictureInPicture()
    }
    render()
    try? AVAudioSession.sharedInstance().setActive(false, options: [.notifyOthersOnDeactivation])
    emit()
  }

  @objc private func background() {
    // The user must start PiP before leaving. Do not claim background capture
    // works while the host can be suspended by iOS.
    if pip?.isPictureInPictureActive != true {
      stop(reason: "未开启画中画便离开 App，已停止采集。请返回开启画中画。")
    }
  }
  @objc private func foreground() { tick(); updatePossible() }
  @objc private func audioInterrupted(_ note: Notification) {
    if (note.userInfo?[AVAudioSessionInterruptionTypeKey] as? NSNumber)?.uintValue == AVAudioSession.InterruptionType.began.rawValue {
      stop(reason: "音频会话被系统中断，采集已停止；请返回重新开启。")
    }
  }
  @objc private func mediaReset() {
    stop(reason: "系统媒体服务已重置，请重新准备画中画。")
    possibleObservation = nil
    pip = nil
    stopInProgress = false
    state["pipActive"] = false
    state["pipPossible"] = false
    emit()
  }

  private func accept(_ connection: NWConnection) {
    guard client == nil, case let .hostPort(host, _) = connection.endpoint,
          host == NWEndpoint.Host("127.0.0.1") else { connection.cancel(); return }
    client = connection
    session = UUID().uuidString
    receivedSequence = 0; sentSequence = 0
    input.removeAll(keepingCapacity: false)
    lastPacket = CaptureProtocol.now()
    connection.stateUpdateHandler = { [weak self, weak connection] status in
      guard let self, let connection, self.client === connection else { return }
      switch status {
      case .ready: self.receive(connection)
      case .failed, .cancelled: self.disconnect("采集连接已断开，已确认计时继续。")
      default: break
      }
    }
    connection.start(queue: .main)
  }

  private func receive(_ connection: NWConnection) {
    connection.receive(minimumIncompleteLength: 1, maximumLength: 4096) { [weak self, weak connection] data, _, complete, error in
      guard let self, let connection, self.client === connection else { return }
      if let data {
        self.input.append(data)
        guard self.input.count <= CaptureProtocol.limit else { self.disconnect("采集消息超出限制。"); return }
        while let newline = self.input.firstIndex(of: 10) {
          let line = self.input.prefix(upTo: newline)
          self.input.removeSubrange(...newline)
          guard let packet = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else {
            self.disconnect("采集消息格式错误。"); return
          }
          self.handle(packet)
          if self.client !== connection { return }
        }
      }
      if complete || error != nil { self.disconnect("采集连接已断开，已确认计时继续。"); return }
      self.receive(connection)
    }
  }

  private func handle(_ packet: [String: Any]) {
    guard CaptureProtocol.number(packet["version"]) == 1, let type = packet["type"] as? String else {
      disconnect("采集协议版本不兼容。"); return
    }
    if type == "hello" {
      guard receivedSequence == 0, state["connected"] as? Bool != true,
            let incomingID = packet["broadcastID"] as? String, UUID(uuidString: incomingID) != nil else {
        disconnect("采集握手无效或重复。"); return
      }
      if broadcastID != incomingID {
        broadcastID = incomingID
        deadlines = Array(repeating: CaptureDeadline(), count: 4)
        for key in Self.deadlineKeys { state[key] = 0 }
        state["frames"] = 0; state["leftSubstitutions"] = 0; state["rightSubstitutions"] = 0
        state["leftNinja"] = ""; state["rightNinja"] = ""
        state["lastFrameAtMs"] = 0
        lastFrame = 0
        invalidateEvidence()
      }
      state["connected"] = true; state["broadcastActive"] = true
      lastPacket = CaptureProtocol.now()
      sendConfiguration()
      message("系统录屏已连接，正在等待游戏画面。")
      return
    }
    guard packet["session"] as? String == session,
          let sequence = CaptureProtocol.sequence(packet["sequence"]), sequence > receivedSequence,
          state["connected"] as? Bool == true else { disconnect("采集会话或序号无效。"); return }
    receivedSequence = sequence
    lastPacket = CaptureProtocol.now()
    switch type {
    case "ping": send(["type": "pong"])
    case "state": apply(packet)
    case "paused":
      invalidateEvidence()
      message("系统录屏暂停；已确认计时继续。")
    case "resumed": message("系统录屏恢复，正在重新确认画面。")
    case "finished", "error": disconnect(String((packet["message"] as? String ?? "系统录屏已停止。").prefix(500)))
    default: disconnect("未知采集消息。")
    }
  }

  private func apply(_ packet: [String: Any]) {
    let now = CaptureProtocol.now()
    let captured = CaptureProtocol.number(packet["capturedAtMonotonic"], -1)
    let wall = CaptureProtocol.number(packet["capturedAtMs"], -1)
    guard captured > lastFrame, captured <= now + 0.25, now - captured < 5,
          wall > 0, wall <= 9_007_199_254_740_991 else { return }
    lastFrame = captured
    for (index, key) in Self.deadlineKeys.enumerated() {
      // Missing/invalid fields never erase a clock. Explicit zero comes only
      // from a validated recognition snapshot (confirmed round reset).
      let value = CaptureProtocol.number(packet[key], -1)
      if value >= 0, value <= min(9_007_199_254_740_991, wall + 60_000) {
        deadlines[index].update(value, sentAtMs: wall, sentAtMonotonic: captured)
        state[key] = value
      }
    }
    for key in ["leftCount", "rightCount"] {
      let n = CaptureProtocol.number(packet[key], -1)
      state[key] = n.rounded() == n && (-1...6).contains(n) ? Int(n) : -1
    }
    let aliases = ["frameWidth": "width", "frameHeight": "height", "leftSubstitutions": "leftEventCount", "rightSubstitutions": "rightEventCount"]
    for key in ["frames", "leftSubstitutions", "rightSubstitutions", "frameWidth", "frameHeight"] {
      let value = CaptureProtocol.number(packet[key] ?? packet[aliases[key] ?? key], -1)
      if value >= 0, value <= 9_007_199_254_740_991, value.rounded() == value { state[key] = value }
    }
    for key in ["leftNinja", "rightNinja", "scene", "message", "ocrStatus", "ocrError", "recognitionError", "playerName", "opponentName"] {
      if let text = packet[key] as? String { state[key] = String(text.prefix(key == "message" ? 500 : 100)) }
    }
    let side = packet["detectedPlayerSide"] as? String ?? ""
    state["detectedPlayerSide"] = ["left", "right"].contains(side) ? side : ""
    state["match"] = CaptureProtocol.bool(packet["match"])
    state["lastFrameAtMs"] = wall
    state["latencyMs"] = min(60_000, max(0, CaptureProtocol.number(packet["latencyMs"])))
    state["deliveryLatencyMs"] = max(0, (now - captured) * 1000)
    state["stale"] = false
    // Recognition events can arrive at 10 Hz; publish at most 4 Hz to JS.
    if now - lastEmit >= 0.25 { lastEmit = now; emit() }
  }

  private func sendConfiguration() {
    guard state["connected"] as? Bool == true else { return }
    send(["type": "configure", "settings": settings])
  }
  private func send(_ value: [String: Any]) {
    guard let client else { return }
    var packet = value
    sentSequence += 1
    packet["version"] = 1; packet["session"] = session; packet["sequence"] = sentSequence
    guard let data = CaptureProtocol.encode(packet) else { disconnect("无法编码采集设置。"); return }
    client.send(content: data, completion: .contentProcessed { [weak self, weak client] error in
      guard let self, let client, self.client === client else { return }
      if error != nil { self.disconnect("发送采集控制失败。") }
    })
  }
  private func disconnect(_ reason: String) {
    let old = client; client = nil; old?.cancel()
    session = ""; input.removeAll(keepingCapacity: false)
    state["connected"] = false; state["broadcastActive"] = false
    invalidateEvidence()
    message(reason)
  }
  private func invalidateEvidence() {
    state["stale"] = true; state["match"] = false
    state["leftCount"] = -1; state["rightCount"] = -1
    state["detectedPlayerSide"] = ""
  }
  private func tick() {
    let now = CaptureProtocol.now()
    if client != nil, now - lastPacket > 6 { disconnect("采集连接超时；已确认计时继续。") }
    if state["connected"] as? Bool == true, lastFrame > 0, now - lastFrame > 3,
       state["stale"] as? Bool != true {
      invalidateEvidence(); message("画面超过 3 秒未更新；已确认计时继续，等待新证据。")
    }
    if pip?.isPictureInPictureActive == true, lastRender > 0, now - lastRender > 4 {
      stop(reason: "画中画视频帧超过 4 秒无法更新，采集已停止，请重新开启。")
      return
    }
    if pendingUntil > 0, now > pendingUntil {
      pendingUntil = 0; startIssued = false; state["pipPending"] = false
      message("系统尚未允许画中画。请保持预览可见并重新点击，勿直接切换游戏。")
    }
    render()
  }
  private func render() {
    let now = CaptureProtocol.now()
    func label(_ index: Int, _ title: String) -> String {
      let clock = deadlines[index]
      let value = clock.epochMs == 0 ? "--" : String(format: "%.1f", clock.remaining(at: now))
      var text = "\(title)  \(value)"
      if deadlines[index + 2].epochMs > 0 {
        text += " / " + String(format: "%.1f", deadlines[index + 2].remaining(at: now))
      }
      return text
    }
    let chosen = settings["playerSide"] as? String ?? "auto"
    let side = chosen == "auto" ? (state["detectedPlayerSide"] as? String ?? "") : chosen
    let both = settings["showBothSides"] as? Bool == true || side.isEmpty
    let lines = both ? [label(0, "左侧"), label(1, "右侧")] : [side == "left" ? label(1, "对手") : label(0, "对手")]
    let stale = state["stale"] as? Bool ?? true
    let visible = both ? [0, 1] : [side == "left" ? 1 : 0]
    let warning = visible.contains { index in
      [deadlines[index], deadlines[index + 2]].contains { clock in
        let remaining = clock.remaining(at: now)
        return clock.epochMs > 0 && remaining > 0 && remaining <= 3
      }
    }
    func identity(_ index: Int) -> String {
      let prefix = index == 0 ? "left" : "right"
      let ninja = String((state[prefix + "Ninja"] as? String ?? "").prefix(12))
      let count = Int(CaptureProtocol.number(state[prefix + "Substitutions"]))
      return "\(ninja.isEmpty ? (index == 0 ? "左侧" : "右侧") : ninja) · 替身 \(count)"
    }
    let status: String
    if stale { status = "画面未确认 · 已有计时继续" }
    else if side.isEmpty { status = "阵营待确认 · 显示双侧" }
    else if state["match"] as? Bool != true { status = "等待对局 · 已有计时继续" }
    else { status = (warning ? "即将恢复 · " : "") + visible.map(identity).joined(separator: " / ") }
    if renderer.render(lines: lines, status: status,
                       scale: CaptureProtocol.number(settings["fontScale"], 0.75), stale: stale, warning: warning) {
      lastRender = now
      state["renderedFrames"] = (state["renderedFrames"] as? Int ?? 0) + 1
    }
  }

  func pictureInPictureControllerDidStartPictureInPicture(_ pictureInPictureController: AVPictureInPictureController) {
    guard listener != nil else { pictureInPictureController.stopPictureInPicture(); return }
    pendingUntil = 0; startIssued = false; state["pipPending"] = false; state["pipActive"] = true
    message("画中画已开启；系统录屏连接后即可切到游戏。")
  }
  func pictureInPictureControllerDidStopPictureInPicture(_ pictureInPictureController: AVPictureInPictureController) {
    stopInProgress = false
    state["pipActive"] = false; state["pipPending"] = false
    stop(reason: "画中画已关闭，采集已停止。")
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  failedToStartPictureInPictureWithError error: Error) {
    pendingUntil = 0; startIssued = false; state["pipPending"] = false; state["pipActive"] = false
    message("画中画启动失败：\(error.localizedDescription)")
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completionHandler: @escaping (Bool) -> Void) {
    completionHandler(true)
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController, setPlaying playing: Bool) {
    if !playing { stop(reason: "系统暂停了画中画，采集已停止。") }
  }
  func pictureInPictureControllerTimeRangeForPlayback(_ pictureInPictureController: AVPictureInPictureController) -> CMTimeRange {
    CMTimeRange(start: .zero, duration: .positiveInfinity)
  }
  func pictureInPictureControllerIsPlaybackPaused(_ pictureInPictureController: AVPictureInPictureController) -> Bool { listener == nil }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  didTransitionToRenderSize newRenderSize: CMVideoDimensions) {}
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  skipByInterval skipInterval: CMTime, completionHandler: @escaping () -> Void) { completionHandler() }
}
