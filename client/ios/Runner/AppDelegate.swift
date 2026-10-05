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
  }
}
