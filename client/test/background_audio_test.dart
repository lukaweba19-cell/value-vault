// Lifecycle wiring for the iOS silent-audio background keep-alive:
//  * backgrounding (paused) asks the native side to enable it;
//  * returning to the foreground disables it again;
//  * inactive/hidden (call UI, app switcher, control centre) never toggle;
//  * non-iOS platforms never touch the channel.
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:solo_checker/background_audio.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  late List<String> methods;
  late TestDefaultBinaryMessenger messenger;

  setUp(() {
    methods = <String>[];
    messenger = TestDefaultBinaryMessengerBinding
        .instance.defaultBinaryMessenger;
    messenger.setMockMethodCallHandler(BackgroundAudio.channel, (call) async {
      methods.add(call.method);
      return null;
    });
  });

  tearDown(() {
    messenger.setMockMethodCallHandler(BackgroundAudio.channel, null);
    debugDefaultTargetPlatformOverride = null;
    BackgroundAudio.instance.enabled = false;
  });

  test('backgrounding on iOS enables the keep-alive exactly once', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.paused);
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.paused);
    expect(methods, ['enable']);
    expect(BackgroundAudio.instance.enabled, isTrue);
  });

  test('returning to the foreground disables it again', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    BackgroundAudio.instance.enabled = true;
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.resumed);
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.resumed);
    expect(methods, ['disable']);
    expect(BackgroundAudio.instance.enabled, isFalse);
  });

  test('paused followed by resumed round-trips the channel', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.paused);
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.resumed);
    expect(methods, ['enable', 'disable']);
    expect(BackgroundAudio.instance.enabled, isFalse);
  });

  test('inactive/hidden never toggle (call UI, app switcher, control centre)',
      () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    BackgroundAudio.instance.enabled = true;
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.inactive);
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.hidden);
    expect(methods, isEmpty);
    expect(BackgroundAudio.instance.enabled, isTrue);
  });

  test('non-iOS platforms never touch the channel', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.android;
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.paused);
    expect(methods, isEmpty);
    expect(BackgroundAudio.instance.enabled, isFalse);
  });

  test('a binary without the channel falls back to no keep-alive', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    messenger.setMockMethodCallHandler(
        BackgroundAudio.channel, (call) => throw MissingPluginException());
    await BackgroundAudio.instance
        .handleLifecycleState(AppLifecycleState.paused);
    expect(BackgroundAudio.instance.enabled, isFalse);
  });
}
