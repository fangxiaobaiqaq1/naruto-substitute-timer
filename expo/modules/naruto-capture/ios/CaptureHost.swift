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
  private var deadlines = Array(repeating: CaptureDeadline(), count: 4)
  private var settings = CaptureProtocol.settings([:])
  private var state: [String: Any] = [
    "available": true, "prepared": false, "broadcastActive": false,
    "pipSupported": false, "pipPossible": false, "pipPending": false, "pipActive": false,
    "connected": false, "message": "请先准备，再开启画中画与系统录屏。",
    "leftDeadlineMs": 0, "rightDeadlineMs": 0,
    "leftAlternateDeadlineMs": 0, "rightAlternateDeadlineMs": 0,
    "leftCount": -1, "rightCount": -1, "match": false,
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

  func snapshot() -> [String: Any] {
    var output = state
    let monotonic = CaptureProtocol.now()
    let wall = Date().timeIntervalSince1970 * 1000
    // JS formats wall-clock deadlines. Rebase only the exported view after a
    // clock correction; native event timestamps and PiP clocks remain pinned.
    for (index, key) in Self.deadlineKeys.enumerated() {
      output[key] = deadlines[index].epochMs == 0 ? 0 : wall + deadlines[index].remaining(at: monotonic) * 1000
    }
    if lastFrame > 0 { output["lastFrameAtMs"] = wall - max(0, monotonic - lastFrame) * 1000 }
    return output
  }
  private func emit() { onState?(snapshot()) }
  private func message(_ value: String) { state["message"] = value; emit() }

  func prepare() throws -> [String: Any] {
    guard Self.broadcastExtensionIdentifier != nil else {
      state["available"] = false
      message("安装包缺少有效的系统录屏扩展，请安装完整 IPA。")
      throw NSError(domain: "NarutoCapture", code: 3, userInfo: [NSLocalizedDescriptionKey: state["message"] as? String ?? "录屏扩展缺失"])
    }
    guard AVPictureInPictureController.isPictureInPictureSupported() else {
      message("此设备不支持画中画；不能保证切到游戏后继续计时。")
      return snapshot()
    }
    guard listener == nil else { return snapshot() }
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
    server.newConnectionHandler = { [weak self, weak server] connection in
      guard let self, let server, self.listener === server else { connection.cancel(); return }
      self.accept(connection)
    }
    server.start(queue: .main)
    if pip == nil {
      let source = AVPictureInPictureController.ContentSource(sampleBufferDisplayLayer: renderer.layer, playbackDelegate: self)
      let controller = AVPictureInPictureController(contentSource: source)
      controller.delegate = self
      controller.canStartPictureInPictureAutomaticallyFromInline = false
      // Display-only live timer: hide scrubbing/skip. iOS always shows both sides.
      controller.requiresLinearPlayback = true
      pip = controller
      // AVKit may query playback during initialization, before identity is set.
      controller.invalidatePlaybackState()
      possibleObservation = controller.observe(\.isPictureInPicturePossible, options: [.initial, .new]) { [weak self, weak controller] _, _ in
        DispatchQueue.main.async {
          guard let self, let controller, self.pip === controller else { return }
          self.updatePossible()
        }
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
    return snapshot()
  }

  func startPictureInPicture() throws -> [String: Any] {
    _ = try prepare()
    guard let pip, !pip.isPictureInPictureActive, !startIssued else { return snapshot() }
    guard UIApplication.shared.applicationState == .active else {
      message("请回到 App 前台开启画中画。")
      return snapshot()
    }
    pendingUntil = CaptureProtocol.now() + 8
    state["pipPending"] = true
    message("正在等待系统允许画中画；请保持预览可见。")
    render()
    updatePossible()
    return snapshot()
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
    let next = CaptureProtocol.settings(value)
    guard !NSDictionary(dictionary: settings).isEqual(to: next) else { return snapshot() }
    settings = next
    timer?.schedule(deadline: .now(), repeating: 1 / CaptureProtocol.number(settings["backgroundFPS"], 8), leeway: .milliseconds(15))
    sendConfiguration()
    render()
    emit()
    return snapshot()
  }

  func stop(reason: String = "采集已停止；可重新准备。") {
    send(["type": "stop"])
    let old = client
    client = nil
    DispatchQueue.main.asyncAfter(deadline: .now() + 0.2) { old?.cancel() }
    listener?.cancel(); listener = nil
    timer?.cancel(); timer = nil
    invalidatePictureInPicture()
    state["prepared"] = false
    invalidateEvidence()
    state["broadcastActive"] = false
    state["connected"] = false
    state["message"] = reason
    render()
    try? AVAudioSession.sharedInstance().setActive(false, options: [.notifyOthersOnDeactivation])
    emit()
  }

  private func invalidatePictureInPicture() {
    // Identity is the generation gate. Retire BEFORE calling AVKit, whose
    // callbacks may reenter or arrive after a new preparation. Never wait for
    // didStop from a discarded controller (especially after a media reset).
    let old = pip
    pip = nil
    possibleObservation?.invalidate()
    possibleObservation = nil
    pendingUntil = 0
    startIssued = false
    state["pipPending"] = false
    state["pipActive"] = false
    state["pipPossible"] = false
    // stop alone need not cancel a start in flight: detach its source too, and
    // the identity guard stops it again if a late didStart still arrives.
    old?.stopPictureInPicture()
    old?.contentSource = nil // Release the reusable renderer layer immediately.
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
    renderer.layer.flush()
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
    connection.receive(minimumIncompleteLength: 1, maximumLength: CaptureProtocol.receiveLimit) { [weak self, weak connection] data, _, complete, error in
      guard let self, let connection, self.client === connection else { return }
      if let data {
        let lines: [Data]
        do { lines = try CaptureProtocol.receiveLines(data, buffering: &self.input) }
        catch { self.disconnect("采集消息超出限制。"); return }
        for line in lines {
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
    for key in ["leftNinja", "rightNinja", "scene", "message", "recognitionError"] {
      if let text = packet[key] as? String { state[key] = String(text.prefix(key == "message" ? 500 : 100)) }
    }
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
      // A timeout must retire the outstanding request, not reopen the gate on
      // the same controller and later accept its stale didStart/didStop.
      stop(reason: "画中画启动超时，采集已停止。请保持预览可见，重新准备并开启画中画。")
      return
    }
    render()
  }
  private func render() {
    let now = CaptureProtocol.now()
    // Same text/colour rules as the APK's both-side card (Session.overlayState).
    func clock(_ index: Int) -> (text: String, color: UIColor) {
      let tenths = CountdownRenderer.tenths(deadlines[index].epochMs == 0 ? 0 : deadlines[index].remaining(at: now))
      if tenths == 0 { return ("—", CountdownRenderer.idle) }
      return ("\(tenths / 10).\(tenths % 10)", tenths <= 30 ? CountdownRenderer.warn : CountdownRenderer.live)
    }
    // Compact strip: countdowns only. Ninja names/counts stay on the app page.
    func column(_ index: Int) -> CountdownColumn {
      let primary = clock(index)
      var alternate: (text: String, color: UIColor)?
      if deadlines[index + 2].epochMs > 0 {
        let value = clock(index + 2)
        alternate = ("10秒 " + (value.text == "—" ? "就绪" : value.text), value.color)
      }
      return CountdownColumn(title: index == 0 ? "左" : "右", clock: primary.text,
                             color: primary.color, alternate: alternate)
    }
    // Side labels carry the status: orange = picture unconfirmed (clocks keep
    // running), grey = waiting for a match, white = live match.
    let label: UIColor = (state["stale"] as? Bool ?? true) ? .systemOrange
      : (state["match"] as? Bool == true ? CountdownRenderer.tag : CountdownRenderer.idle)
    if renderer.render(columns: [column(0), column(1)], labelColor: label) {
      lastRender = now
      state["renderedFrames"] = (state["renderedFrames"] as? Int ?? 0) + 1
    }
  }

  func pictureInPictureControllerDidStartPictureInPicture(_ pictureInPictureController: AVPictureInPictureController) {
    guard pip === pictureInPictureController, listener != nil else {
      pictureInPictureController.stopPictureInPicture()
      return
    }
    pendingUntil = 0; startIssued = false; state["pipPending"] = false; state["pipActive"] = true
    message("画中画已开启；系统录屏连接后即可切到游戏。")
  }
  func pictureInPictureControllerDidStopPictureInPicture(_ pictureInPictureController: AVPictureInPictureController) {
    guard pip === pictureInPictureController else { return }
    stop(reason: "画中画已关闭，采集已停止。")
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  failedToStartPictureInPictureWithError error: Error) {
    guard pip === pictureInPictureController else { return }
    stop(reason: "画中画启动失败：\(error.localizedDescription)")
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completionHandler: @escaping (Bool) -> Void) {
    completionHandler(pip === pictureInPictureController)
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController, setPlaying playing: Bool) {
    // A live timer has nothing to pause; an accidental tap must not stop
    // capture. Keep running and resync AVKit's button to "playing".
    guard pip === pictureInPictureController else { return }
    pictureInPictureController.invalidatePlaybackState()
  }
  func pictureInPictureControllerTimeRangeForPlayback(_ pictureInPictureController: AVPictureInPictureController) -> CMTimeRange {
    guard pip === pictureInPictureController else { return .invalid }
    return CMTimeRange(start: .negativeInfinity, duration: .positiveInfinity) // Live content.
  }
  func pictureInPictureControllerIsPlaybackPaused(_ pictureInPictureController: AVPictureInPictureController) -> Bool {
    pip !== pictureInPictureController || listener == nil
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  didTransitionToRenderSize newRenderSize: CMVideoDimensions) {
    guard pip === pictureInPictureController else { return }
  }
  func pictureInPictureController(_ pictureInPictureController: AVPictureInPictureController,
                                  skipByInterval skipInterval: CMTime, completion completionHandler: @escaping @Sendable () -> Void) {
    completionHandler() // Linear live playback has no seeking; always complete.
  }
}
