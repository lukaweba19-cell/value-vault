import AVFoundation
import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    // File picking (file_picker) and link opening (url_launcher) are regular
    // Flutter plugins — registering them here is all the iOS side needs.
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)

    // Background keep-alive: the Dart side asks for this when the app is
    // about to be suspended (see client/lib/background_audio.dart) so long
    // checks and mail sync keep running while the app is in the background.
    let channel = FlutterMethodChannel(
      name: "solo_checker/background_audio",
      binaryMessenger: engineBridge.applicationRegistrar.messenger()
    )
    channel.setMethodCallHandler { call, result in
      switch call.method {
      case "enable":
        SilentAudioManager.shared.start()
        result(nil)
      case "disable":
        SilentAudioManager.shared.stop()
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }
}

/// Background keep-alive built on a loop of pure digital silence.
///
/// iOS suspends an app ~30s after it is backgrounded unless it is doing
/// something the system keeps alive for — audio playback is the classic
/// case (this is exactly how "background play" music apps work). We start
/// "playing" a looping buffer of all-zero samples only while the app is in
/// the background, which grants the process background audio time so the
/// in-process Go engine (checker runs, mail sync) keeps going.
///
/// It cannot interfere with the user's own audio:
///  * the samples are digital zeros — absolutely nothing is ever audible;
///  * the session category is `.playback` + `.mixWithOthers`, so it never
///    stops, ducks or pauses the user's own music;
///  * phone calls, Siri and other apps' audio interrupt the session — we
///    yield immediately (pause) and only resume when the system says the
///    interruption is over (`.shouldResume`);
///  * on `stop()` the session is deactivated with `.notifyOthersOnDeactivation`
///    so other apps' audio behaves correctly afterwards.
final class SilentAudioManager {
  static let shared = SilentAudioManager()

  private var engine = AVAudioEngine()
  private var player = AVAudioPlayerNode()
  /// The engine + session are up right now.
  private var running = false
  /// The keep-alive was requested; survives interruptions so we can resume.
  private var wantPlaying = false
  private var graphAttached = false
  private var observersInstalled = false

  private init() {}

  /// Starts the silent loop. Called from the Flutter method channel (main
  /// thread); hops to main defensively anyway.
  func start() {
    DispatchQueue.main.async { [weak self] in
      guard let self else { return }
      self.wantPlaying = true
      if self.running { return }
      do {
        let session = AVAudioSession.sharedInstance()
        // mixWithOthers: never interrupts the user's music; the loop itself
        // is inaudible digital silence.
        try session.setCategory(.playback, mode: .default, options: [.mixWithOthers])
        try session.setActive(true)
      } catch {
        // Another part of the app owns audio right now — never fight it.
        return
      }
      self.installObserversIfNeeded()
      self.attachGraphIfNeeded()
      self.engine.prepare()
      do {
        try self.engine.start()
      } catch {
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        return
      }
      self.scheduleSilenceAndPlay()
      self.running = true
    }
  }

  /// Stops the loop and hands the audio session back to the system.
  func stop() {
    DispatchQueue.main.async { [weak self] in
      guard let self else { return }
      self.wantPlaying = false
      self.teardown()
    }
  }

  // MARK: - engine plumbing (main thread only)

  private func teardown() {
    guard running else { return }
    running = false
    player.stop()
    engine.stop()
    try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
  }

  private func attachGraphIfNeeded() {
    guard !graphAttached else { return }
    graphAttached = true
    engine.attach(player)
    let fmt = AVAudioFormat(standardFormatWithSampleRate: 44_100, channels: 1)
    engine.connect(player, to: engine.mainMixerNode, format: fmt)
  }

  private func scheduleSilenceAndPlay() {
    // player.stop() above cleared any scheduled buffers, so a fresh looping
    // buffer is scheduled on every start.
    if let buf = Self.silenceBuffer() {
      player.scheduleBuffer(buf, at: nil, options: .loops, completionHandler: nil)
    }
    player.play()
  }

  /// One second of digital silence (all-zero float samples).
  private static func silenceBuffer() -> AVAudioPCMBuffer? {
    let format = AVAudioFormat(standardFormatWithSampleRate: 44_100, channels: 1)
    let frames = AVAudioFrameCount(44_100)
    guard
      let format,
      let buf = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames)
    else { return nil }
    buf.frameLength = frames
    if let channel = buf.floatChannelData {
      // The buffer's backing memory is not guaranteed to be zeroed.
      for i in 0..<Int(frames) {
        channel[0][i] = 0
      }
    }
    return buf
  }

  // MARK: - system audio events (main thread)

  private func installObserversIfNeeded() {
    guard !observersInstalled else { return }
    observersInstalled = true
    let center = NotificationCenter.default
    center.addObserver(
      forName: AVAudioSession.interruptionNotification,
      object: nil,
      queue: .main
    ) { [weak self] note in
      self?.handleInterruption(note)
    }
    center.addObserver(
      forName: AVAudioSession.routeChangeNotification,
      object: nil,
      queue: .main
    ) { [weak self] _ in
      self?.handleRouteChange()
    }
    center.addObserver(
      forName: AVAudioSession.mediaServicesWereResetNotification,
      object: nil,
      queue: .main
    ) { [weak self] _ in
      self?.handleMediaServicesReset()
    }
  }

  /// Phone call / Siri / another app took the audio session: yield at once,
  /// resume only when the system says we may.
  private func handleInterruption(_ note: Notification) {
    guard wantPlaying else { return }
    guard
      let raw = note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
      let type = AVAudioSession.InterruptionType(rawValue: raw)
    else { return }
    switch type {
    case .began:
      player.pause()
    case .ended:
      let optsRaw = (note.userInfo?[AVAudioSessionInterruptionOptionKey] as? UInt) ?? 0
      let opts = AVAudioSession.InterruptionOptions(rawValue: optsRaw)
      if opts.contains(.shouldResume) {
        try? AVAudioSession.sharedInstance().setActive(true)
        if running && !player.isPlaying {
          player.play()
        }
      }
    @unknown default:
      break
    }
  }

  /// Headphones unplugged / AirPods switched: the system may pause us. The
  /// loop is silent so it is safe (and desirable for the keep-alive) to
  /// resume — nothing can blast out of the speaker.
  private func handleRouteChange() {
    guard wantPlaying, running, !player.isPlaying else { return }
    try? AVAudioSession.sharedInstance().setActive(true)
    player.play()
  }

  /// The media server died — every audio object is invalid. Rebuild.
  private func handleMediaServicesReset() {
    guard wantPlaying else { return }
    running = false
    graphAttached = false
    engine = AVAudioEngine()
    player = AVAudioPlayerNode()
    start()
  }
}
