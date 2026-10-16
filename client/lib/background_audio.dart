// iOS background keep-alive, Dart side.
//
// iOS suspends an app ~30s after backgrounding unless it is doing something
// the system keeps alive for. The native side (ios/Runner/AppDelegate.swift,
// SilentAudioManager) can "play" a looping buffer of pure digital silence so
// the process earns background audio time — long checks and mail sync then
// keep running while the app is in the background.
//
// This observer only drives that: `paused` (app really went to background)
// → enable, `resumed` (back in foreground) → disable. Nothing runs while the
// app is visible, and the silence is inaudible + mixWithOthers, so the
// user's own music, calls and Siri are never affected.
//
// Non-iOS platforms are a no-op; the channel name must match the Swift side.
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

class BackgroundAudio extends WidgetsBindingObserver {
  BackgroundAudio._();
  static final BackgroundAudio instance = BackgroundAudio._();

  /// Channel name must match the Swift FlutterMethodChannel.
  static const MethodChannel channel =
      MethodChannel('solo_checker/background_audio');

  /// Whether the keep-alive is currently requested (test-visible).
  @visibleForTesting
  bool enabled = false;

  /// Registers the lifecycle observer. Call once from main().
  void install() {
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    handleLifecycleState(state);
  }

  @visibleForTesting
  Future<void> handleLifecycleState(AppLifecycleState state) async {
    if (defaultTargetPlatform != TargetPlatform.iOS) return;
    switch (state) {
      case AppLifecycleState.paused:
        if (enabled) return;
        enabled = true;
        try {
          await channel.invokeMethod('enable');
        } on MissingPluginException {
          // Binary without the channel — behave like before (no keep-alive).
          enabled = false;
        }
      case AppLifecycleState.resumed:
        if (!enabled) return;
        enabled = false;
        try {
          await channel.invokeMethod('disable');
        } on MissingPluginException {
          // Nothing was started; nothing to stop.
        }
      default:
        // inactive/hidden fire during calls' UI, the app switcher and the
        // control centre — the keep-alive must keep running through those.
        break;
    }
  }
}
