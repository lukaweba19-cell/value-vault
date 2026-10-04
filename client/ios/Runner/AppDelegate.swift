import Flutter
import UIKit
import UniformTypeIdentifiers

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  private var filePickerChannel: FlutterMethodChannel?

  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)

    // Browse buttons on iOS: there is no Win32/PowerShell dialog in the Go
    // engine on this platform, so the Dart layer (client/lib/api.dart) asks
    // this channel to open Apple's document picker instead. The picked
    // document is copied into the app sandbox and its path is returned in
    // the same {"path": "..."} shape /api/pick produces on Windows.
    let channel = FlutterMethodChannel(
      name: "com.solodolosouls.soloChecker/filePicker",
      binaryMessenger: engineBridge.applicationRegistrar.messenger()
    )
    channel.setMethodCallHandler { call, result in
      if call.method == "pickFile" {
        SoloFilePicker.shared.present(result: result)
      } else {
        result(FlutterMethodNotImplemented)
      }
    }
    filePickerChannel = channel
  }
}

/// Presents UIDocumentPickerViewController and hands the chosen file to Dart.
///
/// The engine reads every path it is given with plain filesystem calls, so a
/// security-scoped iCloud/Files URL would go stale as soon as access is
/// revoked — the document is therefore copied into Documents/imports first,
/// which also makes "Remember proxy list" survive an app relaunch.
final class SoloFilePicker: NSObject, UIDocumentPickerDelegate {
  static let shared = SoloFilePicker()
  private var pending: FlutterResult?

  func present(result: @escaping FlutterResult) {
    guard pending == nil else {
      result(FlutterError(code: "busy", message: "A file picker is already open", details: nil))
      return
    }
    guard let top = Self.topViewController() else {
      result(FlutterError(code: "no-view", message: "No view controller is available", details: nil))
      return
    }
    pending = result
    let picker = UIDocumentPickerViewController(
      forOpeningContentTypes: [UTType.item],
      asCopy: false
    )
    picker.delegate = self
    picker.allowsMultipleSelection = false
    // Explicit full screen: the target is universal (iPhone + iPad), and on
    // iPad a sheet-style picker would otherwise need a popover source view.
    picker.modalPresentationStyle = .fullScreen
    top.present(picker, animated: true, completion: nil)
  }

  func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
    finish(nil)
  }

  func documentPicker(
    _ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]
  ) {
    guard let url = urls.first else {
      finish(nil)
      return
    }
    let scoped = url.startAccessingSecurityScopedResource()
    defer {
      if scoped {
        url.stopAccessingSecurityScopedResource()
      }
    }
    do {
      let fm = FileManager.default
      let documents = try fm.url(
        for: .documentDirectory, in: .userDomainMask, appropriateFor: nil, create: true
      )
      let imports = documents.appendingPathComponent("imports", isDirectory: true)
      if !fm.fileExists(atPath: imports.path) {
        try fm.createDirectory(at: imports, withIntermediateDirectories: true)
      }
      let dst = imports.appendingPathComponent(url.lastPathComponent)
      if fm.fileExists(atPath: dst.path) {
        try fm.removeItem(at: dst)
      }
      try fm.copyItem(at: url, to: dst)
      finish(dst.path)
    } catch {
      finish(nil)
    }
  }

  /// Foreground scene → key window → root view controller → topmost presented.
  ///
  /// UIApplication.keyWindow / UIApplication.windows are deprecated after the
  /// UIScene migration (and nil once a scene-based app is running), so the
  /// lookup walks UIWindowScene instead.
  private static func topViewController() -> UIViewController? {
    let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
    let active =
      scenes.first(where: { $0.activationState == .foregroundActive })
      ?? scenes.first
    let windows = active?.windows ?? []
    let window = windows.first(where: { $0.isKeyWindow }) ?? windows.first
    var top = window?.rootViewController
    while let presented = top?.presentedViewController {
      top = presented
    }
    return top
  }

  private func finish(_ path: String?) {
    let result = pending
    pending = nil
    result?(path)
  }
}
