import Foundation
import ReplayKit
import CoreMedia
import CoreVideo
import ImageIO
import Network

// No App Group entitlement is required. Only recognition results, never screen
// pixels or recordings, leave this extension, and only over IPv4 loopback.
final class SampleHandler: RPBroadcastSampleHandler {
  private let lock = NSLock()
  private let engineLock = NSRecursiveLock()
  private let broadcastID = UUID().uuidString
  // Keep a fixed epoch/monotonic pair across wall-clock corrections.
  private let wallOffsetMs = Date().timeIntervalSince1970 * 1000 - CaptureProtocol.now() * 1000
  private var hostSequence = 0
  private let networkQueue = DispatchQueue(label: "com.narutotimer.broadcast.link")
  private var connection: NWConnection?
  private var heartbeat: DispatchSourceTimer?
  private var input = Data()
  private var session = ""
  private var sequence: Int = 0
  private var lastPong = CaptureProtocol.now()
  private var connectionStarted = CaptureProtocol.now()
  private var sending = false
  private var pendingState: [String: Any]?
  private var pendingSettings: [String: Any]?
  private var configured = false
  private var finished = false
  private var paused = false
  private var engine: NTRecognitionEngine?
  private var lastAnalysis = 0.0
  private var fps = 8.0
  private var frames = 0

  override func broadcastStarted(withSetupInfo setupInfo: [String: NSObject]?) {
    engineLock.lock(); defer { engineLock.unlock() }
    guard let path = Bundle.main.path(forResource: "RecognitionAssets", ofType: nil) else {
      fail("识别资源缺失，请重新安装完整 IPA（含采集扩展）。")
      return
    }
    do {
      engine = try NTRecognitionEngine(assetsPath: path)
      guard engine != nil else { fail("识别引擎未能初始化。"); return }
    } catch {
      fail("识别引擎初始化失败：\(error.localizedDescription)")
      return
    }
    networkQueue.async { [weak self] in
      guard let self else { return }
      self.connectionStarted = CaptureProtocol.now()
      self.connect()
      let timer = DispatchSource.makeTimerSource(queue: self.networkQueue)
      timer.schedule(deadline: .now() + 1, repeating: 1)
      timer.setEventHandler { [weak self] in self?.tick() }
      self.heartbeat = timer
      timer.resume()
    }
  }

  override func processSampleBuffer(_ sampleBuffer: CMSampleBuffer, with sampleBufferType: RPSampleBufferType) {
    guard sampleBufferType == .video else { return }
    engineLock.lock(); defer { engineLock.unlock() }
    lock.lock()
    let stopped = finished
    let ready = configured
    let configuration = pendingSettings
    pendingSettings = nil
    lock.unlock()
    guard !stopped, ready else { return }
    if let configuration {
      engine?.configure(configuration)
      if let value = configuration["backgroundFPS"] as? NSNumber, value.doubleValue.isFinite {
        fps = min(10, max(1, value.doubleValue))
      }
    }
    guard !paused, let engine else { return }
    let monotonic = CaptureProtocol.now()
    guard monotonic - lastAnalysis >= 1.0 / fps else { return }
    if lastAnalysis > 0, monotonic - lastAnalysis > 2 { engine.pauseObservations() }
    lastAnalysis = monotonic
    guard let pixels = CMSampleBufferGetImageBuffer(sampleBuffer) else { engine.pauseObservations(); return }
    let rawOrientation = (CMGetAttachment(sampleBuffer, key: RPVideoSampleOrientationKey as CFString,
                                        attachmentModeOut: nil) as? NSNumber)?.intValue ?? 1
    let orientation = (1...8).contains(rawOrientation) ? rawOrientation : 1
    let capturedAt = wallOffsetMs + monotonic * 1000
    // ReplayKit owns this sample. Finish reading its pixels before returning;
    // do not retain a shallow CMSampleBuffer copy in an asynchronous queue.
    autoreleasepool {
      var result = engine.analyze(pixels, orientation: orientation, capturedAtMs: capturedAt) as? [String: Any] ?? [:]
      frames += 1
      result["type"] = "state"
      result["capturedAtMs"] = capturedAt
      result["capturedAtMonotonic"] = monotonic
      result["frames"] = frames
      networkQueue.async { [weak self, result] in
        guard let self, !self.isFinished else { return }
        self.pendingState = result
        self.flushState()
      }
    }
  }

  override func broadcastPaused() {
    engineLock.lock(); defer { engineLock.unlock() }
    paused = true
    engine?.pauseObservations()
    networkQueue.async { [weak self] in
      self?.pendingState = nil
      self?.sendControl(["type": "paused", "message": "系统录屏已暂停，倒计时按实际时间继续。"])
    }
  }

  override func broadcastResumed() {
    engineLock.lock(); defer { engineLock.unlock() }
    paused = false
    engine?.pauseObservations()
    lastAnalysis = 0
    networkQueue.async { [weak self] in
      self?.sendControl(["type": "resumed", "message": "系统录屏已恢复，正在重新确认画面。"])
    }
  }

  override func broadcastFinished() {
    engineLock.lock(); defer { engineLock.unlock() }
    lock.lock()
    finished = true
    pendingSettings = nil
    lock.unlock()
    engine?.reset()
    engine = nil
    networkQueue.async { [weak self] in
      guard let self else { return }
      self.sendControl(["type": "finished", "message": "系统录屏已结束。"])
      self.heartbeat?.cancel()
      self.heartbeat = nil
      self.pendingState = nil
      let old = self.connection
      self.connection = nil
      self.networkQueue.asyncAfter(deadline: .now() + 0.2) { old?.cancel() }
    }
  }

  private var isFinished: Bool {
    lock.lock(); defer { lock.unlock() }
    return finished
  }

  private func fail(_ message: String) {
    lock.lock()
    guard !finished else { lock.unlock(); return }
    finished = true
    lock.unlock()
    networkQueue.async { [weak self] in
      self?.sendControl(["type": "error", "message": message])
      self?.heartbeat?.cancel()
      self?.heartbeat = nil
      self?.pendingState = nil
      let old = self?.connection
      self?.connection = nil
      self?.networkQueue.asyncAfter(deadline: .now() + 0.2) { old?.cancel() }
    }
    // ReplayKit only exposes an error-ending API to upload extensions. Do not
    // pretend this is the same as the user stopping normally in Control Center.
    finishBroadcastWithError(NSError(domain: "com.narutotimer.broadcast", code: 1,
                                    userInfo: [NSLocalizedDescriptionKey: message]))
  }

  private func connect() {
    guard !isFinished, connection == nil else { return }
    input.removeAll(keepingCapacity: false)
    session = ""
    sequence = 0
    hostSequence = 0
    sending = false
    let client = NWConnection(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: CaptureProtocol.port)!, using: .tcp)
    connection = client
    client.stateUpdateHandler = { [weak self, weak client] state in
      guard let self, let client, self.connection === client else { return }
      switch state {
      case .ready:
        self.sendControl(["type": "hello", "version": 1, "broadcastID": self.broadcastID])
        self.receive(on: client)
      case .failed, .cancelled:
        self.disconnect(client)
      default: break
      }
    }
    client.start(queue: networkQueue)
  }

  private func disconnect(_ client: NWConnection) {
    guard connection === client else { return }
    connection = nil
    sending = false
    if !session.isEmpty { connectionStarted = CaptureProtocol.now() }
    session = ""
    pendingState = nil
    input.removeAll(keepingCapacity: false)
    client.cancel()
    lock.lock()
    configured = false
    lock.unlock()
  }

  private func receive(on client: NWConnection) {
    client.receive(minimumIncompleteLength: 1, maximumLength: 4096) { [weak self, weak client] data, _, complete, error in
      guard let self, let client, self.connection === client else { return }
      if let data {
        self.input.append(data)
        guard self.input.count <= 16_384 else { self.fail("采集控制消息超出限制，已停止。"); return }
        while let newline = self.input.firstIndex(of: 10) {
          let line = self.input.prefix(upTo: newline)
          self.input.removeSubrange(...newline)
          guard let message = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else {
            self.fail("采集控制协议无效，已停止。")
            return
          }
          self.handle(message)
          if self.isFinished { return }
        }
      }
      if complete || error != nil { self.disconnect(client); return }
      self.receive(on: client)
    }
  }

  private func handle(_ message: [String: Any]) {
    guard CaptureProtocol.number(message["version"]) == 1,
          let incoming = CaptureProtocol.sequence(message["sequence"]), incoming > hostSequence,
          let type = message["type"] as? String else { fail("采集控制序号无效。"); return }
    hostSequence = incoming
    if type == "configure" {
      guard (message["version"] as? NSNumber)?.intValue == 1,
            let next = message["session"] as? String, UUID(uuidString: next) != nil,
            (session.isEmpty || next == session),
            let settings = message["settings"] as? [String: Any] else {
        fail("采集配置不兼容，请确保主程序和扩展来自同一个 IPA。")
        return
      }
      session = next
      lastPong = CaptureProtocol.now()
      lock.lock()
      pendingSettings = CaptureProtocol.settings(settings)
      configured = true
      lock.unlock()
      flushState()
    } else if type == "pong", message["session"] as? String == session, !session.isEmpty {
      lastPong = CaptureProtocol.now()
    } else if type == "stop", message["session"] as? String == session, !session.isEmpty {
      fail("计时器已停止采集。")
    } else {
      fail("采集控制会话或消息类型无效。")
    }
  }

  private func tick() {
    guard !isFinished else { return }
    let now = CaptureProtocol.now()
    if session.isEmpty {
      if now - connectionStarted > 10 {
        fail("未连接到计时器。请先回到 App 点击准备并开启画中画，再开始录屏。")
        return
      }
      if connection == nil { connect() }
    } else if now - lastPong > 8 {
      fail("计时器或画中画已暂停响应，已停止录屏以避免后台空转。")
    } else {
      sendControl(["type": "ping"])
    }
  }

  private func sendControl(_ message: [String: Any]) {
    guard let connection else { return }
    var packet = message
    packet["version"] = 1
    if !session.isEmpty {
      sequence += 1
      packet["session"] = session
      packet["sequence"] = sequence
    }
    guard let data = encode(packet) else { return }
    connection.send(content: data, completion: .contentProcessed { [weak self, weak connection] error in
      if error != nil, let connection { self?.disconnect(connection) }
    })
  }

  private func flushState() {
    guard !sending, !session.isEmpty, let connection, var state = pendingState else { return }
    pendingState = nil
    sequence += 1
    state["version"] = 1
    state["session"] = session
    state["sequence"] = sequence
    guard let data = encode(state) else { fail("识别结果无效，已停止录屏。"); return }
    sending = true
    connection.send(content: data, completion: .contentProcessed { [weak self, weak connection] error in
      guard let self, let connection, self.connection === connection else { return }
      self.sending = false
      if error != nil { self.disconnect(connection) } else { self.flushState() }
    })
  }

  private func encode(_ packet: [String: Any]) -> Data? {
    CaptureProtocol.encode(packet)
  }
}
