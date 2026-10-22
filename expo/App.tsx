import { StatusBar } from 'expo-status-bar';
import { useEffect, useRef, useState, type ReactNode } from 'react';
import {
  Alert,
  AppState,
  Linking,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from 'react-native';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { initialWindowMetrics, SafeAreaProvider, SafeAreaView } from 'react-native-safe-area-context';
import {
  DEFAULT_CAPTURE_SETTINGS,
  INITIAL_CAPTURE_STATE,
  NativeCaptureView,
  hasNativeCapture,
  normalizeCaptureSettings,
  pickCalibrationImage,
  importCalibrationDocument,
  shareDocument,
  prepareCapture,
  setCaptureSettings,
  startCapturePictureInPicture,
  stopCapture,
  subscribeCaptureState,
  type CaptureSettings,
} from './replayKit';
import { useCountdownNow } from './ios-compat/useCountdownNow';
import { CalibrationEditor } from './CalibrationEditor';
import { APP_VERSION, IOS_BUILDS_URL, RELEASES_URL, checkForUpdate, isDualNinja, type UpdateSource } from './updates';

type TimerSide = 'left' | 'right';
type TimerMode = 'auto' | 'manual';
type CaptureAction = 'prepare' | 'pip' | 'stop';
type MobileSettings = Omit<CaptureSettings, 'dual' | 'ninjaQuery' | 'playerNames'> & {
  rememberSide: boolean;
  opponentNinja: string;
  miniMode: boolean;
  windowOpacity: number;
  miniOpacity: number;
  autoCheckUpdates: boolean;
  updateSource: UpdateSource;
};

const SETTINGS_KEY = 'naruto-timer-ios-settings-v1';
const DEFAULT_SETTINGS: MobileSettings = {
  playerSide: DEFAULT_CAPTURE_SETTINGS.playerSide,
  rememberSide: true,
  playerName: '',
  opponentNinja: '',
  showBothSides: false,
  miniMode: false,
  windowOpacity: 1,
  miniOpacity: 0.85,
  fontScale: DEFAULT_CAPTURE_SETTINGS.fontScale,
  roiScale: 1,
  roiOffsetX: 0,
  roiOffsetY: 0,
  backgroundFPS: 8,
  calibration: { profile: 'duel' },
  autoCheckUpdates: true,
  updateSource: 'gitee',
};
const PRIMARY_SECONDS = 15;
const ALTERNATE_SECONDS = 10;
const NINJA_CHOICES = ['', '照美冥[五代目水影]', '半藏[秽土转生]', '千手柱间[木叶创立]', '宇智波斑[神驹佑将]', '宇智波带土[十尾人柱力]', '漩涡鸣人[暴怒·第六尾]'];

function formatDeadline(deadline: number, now: number): string {
  if (deadline === 0) return '—';
  return deadline <= now ? '就绪' : (Math.floor((deadline - now) / 100) / 10).toFixed(1);
}

function normalizeSettings(value: unknown): MobileSettings {
  const source = value && typeof value === 'object' ? value as Partial<MobileSettings> : {};
  const { dual: _dual, ninjaQuery: _ninjaQuery, playerNames, ...captureSettings } = normalizeCaptureSettings(source);
  const rememberSide = typeof source.rememberSide === 'boolean' ? source.rememberSide : DEFAULT_SETTINGS.rememberSide;
  return {
    ...captureSettings,
    playerName: playerNames.join('\n'),
    playerSide: rememberSide ? captureSettings.playerSide : 'auto',
    rememberSide,
    autoCheckUpdates: source.autoCheckUpdates !== false,
    updateSource: source.updateSource === 'github' ? 'github' : 'gitee',
    opponentNinja: typeof source.opponentNinja === 'string' ? source.opponentNinja.slice(0, 40) : '',
    miniMode: typeof source.miniMode === 'boolean' ? source.miniMode : DEFAULT_SETTINGS.miniMode,
    windowOpacity: typeof source.windowOpacity === 'number' && Number.isFinite(source.windowOpacity)
      ? Math.min(1, Math.max(0.4, source.windowOpacity)) : DEFAULT_SETTINGS.windowOpacity,
    miniOpacity: typeof source.miniOpacity === 'number' && Number.isFinite(source.miniOpacity)
      ? Math.min(1, Math.max(0.2, source.miniOpacity)) : DEFAULT_SETTINGS.miniOpacity,
  };
}

function valueOrWaiting(value: string): string {
  return value === '—' ? '未计时' : value;
}

function MiniPill({
  settings, text, controlsVisible, onToggleControls, onSwap, onToggleBoth, onOpenSettings, onExitMini,
}: {
  settings: MobileSettings;
  text: string;
  controlsVisible: boolean;
  onToggleControls: () => void;
  onSwap: () => void;
  onToggleBoth: () => void;
  onOpenSettings: () => void;
  onExitMini: () => void;
}) {
  return (
    <View style={[styles.pillWrap, { opacity: settings.miniOpacity }]}>
      <Pressable accessibilityRole="button" accessibilityLabel={`${text}。点击${controlsVisible ? '隐藏' : '打开'}控制`} accessibilityState={{ expanded: controlsVisible }} onPress={onToggleControls} style={styles.pill}>
        <Text style={[styles.pillText, { fontSize: 15 * settings.fontScale }]}>{text}</Text>
      </Pressable>
      {controlsVisible ? (
        <View style={styles.pillControls}>
          <MiniButton label="设置" onPress={onOpenSettings} />
          <MiniButton label="换边" onPress={onSwap} />
          <MiniButton label={settings.showBothSides ? '单边计时' : '两边计时'} onPress={onToggleBoth} />
          <MiniButton label="展开" onPress={onExitMini} />
        </View>
      ) : null}
    </View>
  );
}

function MiniButton({ label, onPress }: { label: string; onPress: () => void }) {
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={styles.miniButton}>
      <Text style={styles.miniButtonText}>{label}</Text>
    </Pressable>
  );
}

function SettingRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <View style={styles.settingRow}>
      <Text style={styles.settingLabel}>{label}</Text>
      {children}
    </View>
  );
}

function ValueStepper({ label, value, minimum, maximum, step, unit = '%', onChange }: {
  label: string; value: number; minimum: number; maximum: number; step: number; unit?: '%' | '帧/秒'; onChange: (value: number) => void;
}) {
  const change = (delta: number) => onChange(Math.min(maximum, Math.max(minimum, Math.round((value + delta) * 100) / 100)));
  const display = `${unit === '%' ? Math.round(value * 100) : value}${unit}`;
  return (
    <View style={styles.stepper}>
      <Pressable accessibilityRole="button" accessibilityLabel={`减小${label}`} accessibilityState={{ disabled: value <= minimum }} disabled={value <= minimum} style={styles.stepButton} onPress={() => change(-step)}><Text style={styles.stepText}>−</Text></Pressable>
      <Text accessibilityLabel={`${label} ${display}`} style={styles.stepValue}>{display}</Text>
      <Pressable accessibilityRole="button" accessibilityLabel={`增大${label}`} accessibilityState={{ disabled: value >= maximum }} disabled={value >= maximum} style={styles.stepButton} onPress={() => change(step)}><Text style={styles.stepText}>＋</Text></Pressable>
    </View>
  );
}

export default function App() {
  return (
    <SafeAreaProvider initialMetrics={initialWindowMetrics}>
      <TimerScreen />
    </SafeAreaProvider>
  );
}

function TimerScreen() {
  const { fontScale: systemFontScale } = useWindowDimensions();
  const [settings, setSettings] = useState<MobileSettings>(DEFAULT_SETTINGS);
  const [settingsVisible, setSettingsVisible] = useState(false);
  const [calibrationVisible, setCalibrationVisible] = useState(false);
  const [ninjaChoicesVisible, setNinjaChoicesVisible] = useState(false);
  const [updateMessage, setUpdateMessage] = useState('');
  const [updateNotes, setUpdateNotes] = useState('');
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [controlsVisible, setControlsVisible] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [capture, setCapture] = useState(INITIAL_CAPTURE_STATE);
  const [mode, setMode] = useState<TimerMode>('auto');
  const [busy, setBusy] = useState<CaptureAction | ''>('');
  const [actionError, setActionError] = useState('');
  const [settingsError, setSettingsError] = useState('');
  const [storageError, setStorageError] = useState('');
  const [applyingSettings, setApplyingSettings] = useState(false);
  const [manualDetectedSide, setManualDetectedSide] = useState<TimerSide | ''>('');
  const [deadlines, setDeadlines] = useState({ left: 0, right: 0, leftAlternate: 0, rightAlternate: 0 });
  const settingsQueue = useRef(Promise.resolve());
  const saveQueue = useRef(Promise.resolve());
  const updatePending = useRef(false);
  const lastUpdateCheck = useRef(0);
  const automatic = mode === 'auto';
  const currentDeadlines = automatic ? {
    left: capture.leftDeadlineMs,
    right: capture.rightDeadlineMs,
    leftAlternate: capture.leftAlternateDeadlineMs,
    rightAlternate: capture.rightAlternateDeadlineMs,
  } : deadlines;
  const now = useCountdownNow(Math.max(currentDeadlines.left, currentDeadlines.right, currentDeadlines.leftAlternate, currentDeadlines.rightAlternate));

  const applyNativeSettings = (value: MobileSettings) => {
    const next = normalizeCaptureSettings({ ...value, ninjaQuery: value.opponentNinja, dual: isDualNinja(value.opponentNinja) });
    // Keep slower native calls ordered when the user edits several fields quickly.
    settingsQueue.current = settingsQueue.current.catch(() => undefined).then(() => setCaptureSettings(next));
    return settingsQueue.current;
  };

  useEffect(() => {
    let active = true;
    AsyncStorage.getItem(SETTINGS_KEY)
      .then((stored) => {
        if (active && stored) setSettings(normalizeSettings(JSON.parse(stored)));
      })
      .catch(() => { if (active) setStorageError('无法读取已保存的设置，正在使用默认值；请检查校准与我方位置。'); })
      .finally(() => { if (active) setLoaded(true); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    if (!loaded) return;
    saveQueue.current = saveQueue.current.catch(() => undefined)
      .then(() => AsyncStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)))
      .catch(() => { setStorageError('设置保存失败；关闭 App 后这些改动可能丢失。'); });
  }, [loaded, settings]);

  useEffect(() => subscribeCaptureState(setCapture), []);

  useEffect(() => {
    if (!loaded || !hasNativeCapture) return;
    let active = true;
    setApplyingSettings(true);
    const timer = setTimeout(() => {
      void applyNativeSettings(settings)
        .then(() => { if (active) setSettingsError(''); })
        .catch(() => { if (active) setSettingsError('设置未能同步到原生采集，请重新准备后再开始。'); })
        .finally(() => { if (active) setApplyingSettings(false); });
    }, 200);
    return () => { active = false; clearTimeout(timer); };
  }, [loaded, settings]);

  useEffect(() => {
    if (!controlsVisible) return;
    const timer = setTimeout(() => setControlsVisible(false), 5000);
    return () => clearTimeout(timer);
  }, [controlsVisible]);

  const dual = isDualNinja(settings.opponentNinja);
  const leftDual = automatic ? currentDeadlines.leftAlternate > 0 || isDualNinja(capture.leftNinja) || (dual && settings.playerSide === 'right') : dual;
  const rightDual = automatic ? currentDeadlines.rightAlternate > 0 || isDualNinja(capture.rightNinja) || (dual && settings.playerSide === 'left') : dual;
  const frameAge = now - capture.lastFrameAtMs;
  const freshFrame = capture.frames > 0 && capture.lastFrameAtMs > 0 && frameAge >= 0 && frameAge < 3000;
  const receivingMatch = capture.available && capture.broadcastActive && capture.connected && capture.match && freshFrame;
  const healthy = receivingMatch && capture.prepared && capture.pipSupported && capture.pipActive && !actionError && !settingsError && !storageError && !applyingSettings;
  const detectedSide = automatic ? capture.detectedPlayerSide : manualDetectedSide;
  const playerSide = settings.playerSide === 'auto' ? detectedSide : settings.playerSide;
  const opponentSide = playerSide === 'left' ? 'right' : playerSide === 'right' ? 'left' : '';
  const showBothClocks = settings.showBothSides || (automatic && !playerSide);
  const sideText = settings.playerSide !== 'auto' ? `我方${playerSide === 'left' ? '左' : '右'}`
    : playerSide ? `${automatic ? '原生认边' : '手动假定'}·我方${playerSide === 'left' ? '左' : '右'}` : '我方位置未确认';
  // A lost/obscured frame is not a cancellation of a confirmed substitution.
  const clock = (deadline: number) => formatDeadline(deadline, now);
  const leftText = clock(currentDeadlines.left);
  const rightText = clock(currentDeadlines.right);
  const leftAlternate = clock(currentDeadlines.leftAlternate);
  const rightAlternate = clock(currentDeadlines.rightAlternate);
  const primaryText = opponentSide === 'left' ? leftText : opponentSide === 'right' ? rightText : '—';
  const alternate = opponentSide === 'left' ? leftAlternate : opponentSide === 'right' ? rightAlternate : '—';
  const opponentDual = opponentSide === 'left' ? leftDual : opponentSide === 'right' ? rightDual : dual;
  const running = Object.values(currentDeadlines).some((deadline) => deadline > now);
  const pillText = `${automatic ? '自动计时' : '手动备用'}·${sideText}：${showBothClocks
    ? `左 ${valueOrWaiting(leftText)}${leftDual ? ` / 10秒 ${leftAlternate}` : ''} · 右 ${valueOrWaiting(rightText)}${rightDual ? ` / 10秒 ${rightAlternate}` : ''}`
    : opponentDual ? `15秒 ${valueOrWaiting(primaryText)} / 10秒 ${alternate}` : valueOrWaiting(primaryText)}`;
  const statusText = !capture.available ? '自动采集不可用'
    : !capture.pipSupported ? '画中画不可用'
      : !capture.prepared ? '等待准备采集'
        : !capture.pipActive ? '等待开启画中画'
          : !capture.broadcastActive ? '等待系统录屏授权'
            : !capture.connected ? '广播已开启 · 等待连接'
              : !freshFrame ? '等待新画面 · 尚未确认采集'
                : !capture.match ? '已收到画面 · 等待决斗'
                  : '正在识别决斗';
  const pickerEnabled = capture.available && capture.prepared && capture.pipSupported && capture.pipActive;

  const startCooldown = (side: TimerSide) => {
    if (automatic) return;
    // Manual convenience only: never write a guessed side or deadline to native state.
    setManualDetectedSide(side === 'left' ? 'right' : 'left');
    const startedAt = Date.now();
    const primaryDeadline = startedAt + PRIMARY_SECONDS * 1000;
    const alternateDeadline = dual ? startedAt + ALTERNATE_SECONDS * 1000 : 0;
    if (side === 'left') {
      setDeadlines((current) => ({ ...current, left: primaryDeadline, leftAlternate: alternateDeadline }));
    } else {
      setDeadlines((current) => ({ ...current, right: primaryDeadline, rightAlternate: alternateDeadline }));
    }
  };

  const swapSide = () => {
    const nextSide = playerSide === 'left' ? 'right' : 'left';
    setSettings((current) => ({ ...current, playerSide: nextSide }));
  };

  const runCaptureAction = async (action: CaptureAction) => {
    if (busy) return;
    setBusy(action);
    setActionError('');
    try {
      if (action === 'prepare') {
        await applyNativeSettings(settings);
        setSettingsError('');
      }
      const state = action === 'prepare' ? await prepareCapture()
        : action === 'pip' ? await startCapturePictureInPicture() : await stopCapture();
      // A successful JS call is not evidence that broadcasting or PiP has started.
      setCapture(state);
    } catch (error) {
      const detail = error instanceof Error ? error.message.slice(0, 300) : '原生调用失败，请检查采集状态。';
      setActionError(`${action === 'prepare' ? '准备失败' : action === 'pip' ? '画中画开启失败' : '停止失败'}：${detail}`);
    } finally {
      setBusy('');
    }
  };

  const runUpdateCheck = async (manual: boolean) => {
    if (updatePending.current) return;
    updatePending.current = true;
    setCheckingUpdate(true);
    lastUpdateCheck.current = Date.now();
    try {
      const result = await checkForUpdate(settings.updateSource);
      setUpdateMessage(result.newer ? `发现新版 ${result.version}；iOS 请下载对应 IPA 并重新自签。` : `当前 v${APP_VERSION}，最新正式版 ${result.version}。`);
      setUpdateNotes(result.notes);
    } catch (error) {
      if (manual) setUpdateMessage(error instanceof Error ? error.message : '检查更新失败，请检查网络。');
    } finally {
      updatePending.current = false;
      setCheckingUpdate(false);
    }
  };

  useEffect(() => {
    if (!loaded || !settings.autoCheckUpdates) return;
    const check = () => {
      if (AppState.currentState === 'active' && Date.now() - lastUpdateCheck.current >= 6 * 3600_000) void runUpdateCheck(false);
    };
    const timer = setTimeout(check, 10000);
    const listener = AppState.addEventListener('change', check);
    return () => { clearTimeout(timer); listener.remove(); };
  }, [loaded, settings.autoCheckUpdates, settings.updateSource]);

  const openURL = (url: string) => {
    void Linking.openURL(url).catch(() => Alert.alert('无法打开链接', '请在浏览器打开：' + url));
  };
  const exportDiagnostics = async () => {
    try {
      // Deliberately enumerate fields: account names, screenshots and logs are never exported.
      const report = {
        kind: 'naruto-timer-ios-diagnostics', version: APP_VERSION, createdAt: new Date().toISOString(),
        platform: Platform.OS, systemVersion: Platform.Version,
        capture: { available: capture.available, prepared: capture.prepared, connected: capture.connected,
          broadcastActive: capture.broadcastActive, pipSupported: capture.pipSupported, pipActive: capture.pipActive,
          match: capture.match, scene: capture.scene, frames: capture.frames, lastFrameAtMs: capture.lastFrameAtMs,
          frameWidth: capture.frameWidth, frameHeight: capture.frameHeight, latencyMs: capture.latencyMs,
          leftCount: capture.leftCount, rightCount: capture.rightCount,
          leftSubstitutions: capture.leftSubstitutions, rightSubstitutions: capture.rightSubstitutions },
        settings: { playerSide: settings.playerSide, hasPlayerName: !!settings.playerName,
          showBothSides: settings.showBothSides, backgroundFPS: settings.backgroundFPS,
          roiScale: settings.roiScale, roiOffsetX: settings.roiOffsetX, roiOffsetY: settings.roiOffsetY,
          calibration: settings.calibration },
      };
      await shareDocument('naruto-ios-diagnostics.json', JSON.stringify(report, null, 2));
    } catch (error) { Alert.alert('导出失败', error instanceof Error ? error.message : '无法分享诊断文件。'); }
  };

  const saveSettings = () => {
    setSettingsVisible(false);
    setControlsVisible(false);
  };

  return (
    <SafeAreaView style={styles.safeArea}>
      <StatusBar style="light" />
      <ScrollView contentContainerStyle={styles.container}>
        <Text style={styles.eyebrow}>NARUTO SUBSTITUTE TIMER · IOS</Text>
        <View style={styles.headerLine}>
          <View style={styles.headerCopy}>
            <Text style={styles.title}>替身计时器</Text>
            <Text style={styles.subtitle}>v{APP_VERSION} · 自动采集 · 系统画中画</Text>
          </View>
          <Pressable accessibilityRole="button" accessibilityLabel="打开替身设置" disabled={!loaded} style={styles.closeButton} onPress={() => setSettingsVisible(true)}><Text style={styles.closeText}>设置</Text></Pressable>
        </View>

        <View style={styles.statusCard}>
          <View style={styles.headerLine}>
            <Text style={styles.cardTitle}>自动采集状态</Text>
            <View accessibilityLabel={healthy ? '原生已确认画中画和决斗画面' : '尚未确认完整采集链路'} style={[styles.statusDot, healthy && styles.readyDot]} />
          </View>
          <Text style={[styles.statusText, healthy && styles.readyText]}>{statusText}</Text>
          <Text style={styles.cardHint}>{capture.message}</Text>
          <Text style={styles.cardHint}>广播：{capture.broadcastActive ? '已开启' : '未开启'} · 连接：{capture.connected ? '已连接' : '未连接'}{ '\n' }画中画：{capture.pipActive ? '已开启' : '未开启'} · 决斗：{receivingMatch ? '已识别' : '未确认'}</Text>
          <Text style={styles.cardHint}>{capture.lastFrameAtMs > 0 && frameAge >= 0
            ? `最近画面 ${Math.floor(frameAge / 1000)} 秒前 · 共 ${capture.frames} 帧`
            : '尚未收到有效画面'}{ '\n' }最近识别豆数：左 {capture.leftCount < 0 ? '未知' : capture.leftCount} / 右 {capture.rightCount < 0 ? '未知' : capture.rightCount}</Text>
          <Text style={styles.cardHint}>忍者：左 {capture.leftNinja || '未确认'} / 右 {capture.rightNinja || '未确认'}{'\n'}本局替身：左 {capture.leftSubstitutions} / 右 {capture.rightSubstitutions}{'\n'}分析尺寸 {capture.frameWidth}×{capture.frameHeight} · 单帧识别 {Math.round(capture.latencyMs)} ms（非端到端延迟）</Text>
          <Text style={styles.cardHint}>账号文字识别：{capture.ocrStatus || '尚未启用'}</Text>
          {capture.ocrError || capture.recognitionError ? <Text accessibilityRole="alert" style={styles.errorText}>{capture.recognitionError || capture.ocrError}</Text> : null}
          {!receivingMatch && running ? <Text style={styles.cardHint}>当前画面未确认；已确认的倒计时继续，不凭旧帧新增替身。</Text> : null}
          {capture.available && !capture.pipSupported ? <Text accessibilityRole="alert" style={styles.errorText}>此设备或系统当前不支持画中画，无法在游戏上方显示计时。可切换手动备用模式，仅在本页使用。</Text> : null}
          {actionError || settingsError || storageError ? <Text accessibilityRole="alert" style={styles.errorText}>{actionError || settingsError || storageError}</Text> : null}
          {applyingSettings ? <Text style={styles.cardHint}>正在同步原生设置…</Text> : null}

          <View style={styles.actionRow}>
            <ActionButton label={busy === 'prepare' ? '准备中…' : capture.prepared ? '1. 已准备' : '1. 准备采集'} disabled={!loaded || !capture.available || !!busy || capture.prepared} onPress={() => void runCaptureAction('prepare')} />
            <ActionButton label={busy === 'pip' ? '等待画中画…' : capture.pipActive ? '2. 画中画已开启' : '2. 开启画中画'} disabled={!capture.prepared || !capture.pipSupported || capture.pipActive || !!busy || applyingSettings || !!settingsError} onPress={() => void runCaptureAction('pip')} />
          </View>
          <Text style={styles.stepTitle}>3. 点击系统录屏按钮</Text>
          <Text style={styles.cardHint}>先准备并开启画中画，再点下方系统控件，选择本应用的广播扩展并确认开始直播。这是必需的系统授权；上方按钮不会启动录屏。</Text>
          {NativeCaptureView ? (
            <View pointerEvents={pickerEnabled ? 'auto' : 'none'} accessibilityElementsHidden={!pickerEnabled} importantForAccessibility={pickerEnabled ? 'auto' : 'no-hide-descendants'} style={styles.nativePreviewWrap}>
              <NativeCaptureView style={styles.nativePreview} />
            </View>
          ) : <Text style={styles.errorText}>当前安装包没有可用的系统录屏控件。</Text>}
          <Text style={styles.stepTitle}>4. 切换到游戏</Text>
          <Text style={styles.cardHint}>确认系统广播和画中画已开启后，切到游戏开始决斗。收到有效画面才会显示自动倒计时。</Text>
          <Pressable accessibilityRole="button" accessibilityLabel="停止采集与画中画" accessibilityState={{ disabled: !hasNativeCapture || !!busy }} disabled={!hasNativeCapture || !!busy} style={[styles.outlineButton, (!hasNativeCapture || !!busy) && styles.disabledButton]} onPress={() => void runCaptureAction('stop')}>
            <Text style={styles.outlineText}>{busy === 'stop' ? '正在停止…' : '停止采集与画中画'}</Text>
          </Pressable>
        </View>

        <View style={[styles.segmented, styles.modeSelector]}>
          {([['auto', '自动计时'], ['manual', '手动备用']] as const).map(([value, label]) => (
            <Pressable key={value} accessibilityRole="button" accessibilityLabel={label} accessibilityState={{ selected: mode === value }} onPress={() => setMode(value)} style={[styles.segment, mode === value && styles.segmentSelected]}><Text style={styles.segmentText}>{label}</Text></Pressable>
          ))}
        </View>
        <Text style={styles.cardHint}>{automatic
          ? `${sideText}。自动模式只显示原生识别结果。`
          : '手动备用仅在本页工作；按键只操作备用计时，不会改变自动识别、广播或画中画状态。'}</Text>
        {automatic && settings.playerSide === 'auto' && !capture.detectedPlayerSide ? (
          <Pressable accessibilityRole="button" accessibilityLabel="设置我方位置或游戏名字" style={styles.outlineButton} onPress={() => setSettingsVisible(true)}>
            <Text style={styles.outlineText}>我方位置未确认，显示双边；点此手动选边</Text>
          </Pressable>
        ) : null}

        {settings.miniMode ? (
          <MiniPill settings={settings} text={pillText} controlsVisible={controlsVisible}
            onToggleControls={() => setControlsVisible((visible) => !visible)} onSwap={swapSide}
            onToggleBoth={() => setSettings((current) => ({ ...current, showBothSides: !current.showBothSides }))}
            onOpenSettings={() => setSettingsVisible(true)}
            onExitMini={() => { setControlsVisible(false); setSettings((current) => ({ ...current, miniMode: false })); }} />
        ) : (
          <View style={[styles.fullCard, { opacity: settings.windowOpacity }]}>
            <Text style={styles.cardTitle}>{automatic ? '自动识别计时' : '手动备用计时'} · {running ? '计时中' : automatic && !receivingMatch ? '等待有效决斗画面' : '等待掉豆'}</Text>
            <Text style={styles.cardHint}>{sideText}{showBothClocks ? ' · 两边显示' : ' · 显示对面'}</Text>
            {showBothClocks ? (
              <View style={styles.clockRow}>
                {([['左侧', leftText, leftAlternate, leftDual], ['右侧', rightText, rightAlternate, rightDual]] as const).map(([label, value, alternateValue, sideDual]) => (
                  <View key={label} style={[styles.clockColumn, { flexBasis: 132 * systemFontScale }]}>
                    <Text style={styles.cardTitle}>{label}</Text>
                    <Text accessibilityLabel={`${label}替身倒计时：${valueOrWaiting(value)}`} style={[styles.fullClock, { fontSize: 52 * settings.fontScale }]}>{value}</Text>
                    {sideDual ? <Text style={styles.dualClock}>10秒 {alternateValue}</Text> : null}
                  </View>
                ))}
              </View>
            ) : (
              <>
                <Text accessibilityLabel={`对面替身倒计时：${valueOrWaiting(primaryText)}`} style={[styles.fullClock, { fontSize: 52 * settings.fontScale }]}>{primaryText}</Text>
                {opponentDual ? <Text style={styles.dualClock}>15秒 {primaryText} / 10秒 {alternate}</Text> : null}
              </>
            )}
            <Pressable accessibilityRole="button" accessibilityLabel="切换本页紧凑计时" style={styles.outlineButton} onPress={() => setSettings((current) => ({ ...current, miniMode: true }))}>
              <Text style={styles.outlineText}>本页紧凑显示</Text>
            </Pressable>
          </View>
        )}

        {!automatic ? (
          <View style={styles.actionRow}>
            <ActionButton label="手动：左侧掉豆" onPress={() => startCooldown('left')} />
            <ActionButton label="手动：右侧掉豆" onPress={() => startCooldown('right')} />
            <ActionButton label="清空备用计时" onPress={() => { setManualDetectedSide(''); setDeadlines({ left: 0, right: 0, leftAlternate: 0, rightAlternate: 0 }); }} />
          </View>
        ) : null}

        <View style={styles.infoCard}>
          <Text style={styles.cardTitle}>权限与安装</Text>
          <Text style={styles.cardHint}>录屏必须由你在系统控件中确认。跨应用显示使用系统画中画，不是任意悬浮窗；可用性受设备和系统限制。自签安装需包含主 App 与广播扩展，且签名保持有效。</Text>
        </View>
      </ScrollView>

      <Modal animationType="slide" presentationStyle="pageSheet" visible={settingsVisible} onRequestClose={() => { setCalibrationVisible(false); setSettingsVisible(false); }}>
        {/* A page sheet has its own insets; do not seed it with the main window metrics. */}
        <SafeAreaProvider>
          <SafeAreaView style={styles.modalSafeArea}>
            <ScrollView automaticallyAdjustKeyboardInsets keyboardDismissMode="interactive" keyboardShouldPersistTaps="handled" contentContainerStyle={styles.settingsPanel}>
              <View style={styles.modalHeader}>
                <Text style={styles.modalTitle}>替身设置</Text>
                <Pressable accessibilityRole="button" accessibilityLabel="完成设置" style={styles.closeButton} onPress={saveSettings}><Text style={styles.closeText}>完成</Text></Pressable>
              </View>
              <SettingRow label="我方位置">
                <View style={styles.segmented}>
                  {([['auto', '自动/双边'], ['left', '左边'], ['right', '右边']] as const).map(([value, label]) => (
                    <Pressable key={value} accessibilityRole="button" accessibilityLabel={`我方位置：${label}`} accessibilityState={{ selected: settings.playerSide === value }} onPress={() => { if (value === 'auto') setManualDetectedSide(''); setSettings((current) => ({ ...current, playerSide: value })); }} style={[styles.segment, settings.playerSide === value && styles.segmentSelected]}>
                      <Text style={styles.segmentText}>{label}</Text>
                    </Pressable>
                  ))}
                </View>
                <Text style={styles.cardHint}>手动选我方左／右最可靠。自动仅采用原生可靠证据，未确定时显示左右双边，不会根据首次掉豆猜测我方位置。</Text>
              </SettingRow>
              <SettingRow label="记住我方位置">
                <Switch accessibilityLabel="记住我方位置" style={styles.switch} hitSlop={7} value={settings.rememberSide} onValueChange={(value) => setSettings((current) => ({ ...current, rememberSide: value }))} />
              </SettingRow>
              <SettingRow label="我的游戏名字（支持多个账号）">
                <TextInput accessibilityLabel="我的游戏名字，每行一个" multiline maxLength={840} autoCorrect={false} value={settings.playerName} onChangeText={(value) => setSettings((current) => ({ ...current, playerName: value }))} placeholder="每行一个完整账号名，最多20个，每个40字" placeholderTextColor="#71819a" style={styles.input} />
                <Text style={styles.cardHint}>在本机识别账号文字；精确确认我方名字或已知对手后自动认边。遮挡或证据不足时保持未知，不根据首次掉豆猜边；不会上传截图和名字。</Text>
              </SettingRow>
              <SettingRow label="对面忍者（留空自动识别）">
                <TextInput accessibilityLabel="对面忍者" returnKeyType="done" maxLength={40} value={settings.opponentNinja} onChangeText={(value) => setSettings((current) => ({ ...current, opponentNinja: value }))} placeholder="照美冥[五代目水影]" placeholderTextColor="#71819a" style={styles.input} />
                <MiniButton label={ninjaChoicesVisible ? '收起忍者选项' : '选择常用忍者 / 自动'} onPress={() => setNinjaChoicesVisible((value) => !value)} />
                {ninjaChoicesVisible ? <View style={styles.actionRow}>{NINJA_CHOICES.map((name) => <MiniButton key={name || 'auto'} label={name || '自动识别'} onPress={() => { setSettings((current) => ({ ...current, opponentNinja: name })); setNinjaChoicesVisible(false); }} />)}</View> : null}
                <Text style={styles.cardHint}>自动识别头像和忍者标题。填完整变体名可覆盖对面；照美冥[五代目水影]和半藏[秽土转生]使用15／10秒双计时。换边未确认时不把覆盖强加给某一边。</Text>
              </SettingRow>
              <SettingRow label="两边计时（含画中画）">
                <Switch accessibilityLabel="两边计时" style={styles.switch} hitSlop={7} value={settings.showBothSides} onValueChange={(value) => setSettings((current) => ({ ...current, showBothSides: value }))} />
              </SettingRow>
              <SettingRow label="计时字号（含画中画）">
                <ValueStepper label="计时字号" value={settings.fontScale} minimum={0.55} maximum={1.2} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, fontScale: value }))} />
              </SettingRow>
              <SettingRow label="区域校准（豆 / 头像）">
                <Text style={styles.cardHint}>当前 {settings.calibration.profile === 'camp' ? '训练场' : '决斗场'} · {Object.keys(settings.calibration).filter((key) => key !== 'profile').length} 个自定义区域。特殊分辨率可载入游戏截图画框，或导入电脑版 / 安卓的校准方案。</Text>
                <ActionButton label="打开校准编辑器" disabled={!hasNativeCapture} onPress={() => setCalibrationVisible(true)} />
              </SettingRow>
              <SettingRow label="识别区域缩放">
                <ValueStepper label="识别区域缩放" value={settings.roiScale} minimum={0.7} maximum={1.4} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, roiScale: value }))} />
              </SettingRow>
              <SettingRow label="识别区域水平偏移">
                <ValueStepper label="识别区域水平偏移" value={settings.roiOffsetX} minimum={-0.15} maximum={0.15} step={0.01} onChange={(value) => setSettings((current) => ({ ...current, roiOffsetX: value }))} />
              </SettingRow>
              <SettingRow label="识别区域垂直偏移">
                <ValueStepper label="识别区域垂直偏移" value={settings.roiOffsetY} minimum={-0.15} maximum={0.15} step={0.01} onChange={(value) => setSettings((current) => ({ ...current, roiOffsetY: value }))} />
              </SettingRow>
              <SettingRow label="后台采集帧率">
                <ValueStepper label="后台采集帧率" value={settings.backgroundFPS} minimum={1} maximum={10} step={1} unit="帧/秒" onChange={(value) => setSettings((current) => ({ ...current, backgroundFPS: value }))} />
              </SettingRow>
              <Text style={styles.cardHint}>识别校准与帧率传给原生采集。仅在豆数持续识别不准时小幅调整区域；较高帧率更耗电。</Text>
              <SettingRow label="本页紧凑显示">
                <Switch accessibilityLabel="本页紧凑显示" style={styles.switch} hitSlop={7} value={settings.miniMode} onValueChange={(value) => setSettings((current) => ({ ...current, miniMode: value }))} />
              </SettingRow>
              <SettingRow label="本页完整计时卡透明度">
                <ValueStepper label="本页完整计时卡透明度" value={settings.windowOpacity} minimum={0.4} maximum={1} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, windowOpacity: value }))} />
              </SettingRow>
              <SettingRow label="本页紧凑计时透明度">
                <ValueStepper label="本页紧凑计时透明度" value={settings.miniOpacity} minimum={0.2} maximum={1} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, miniOpacity: value }))} />
              </SettingRow>
              <Text style={styles.settingsNote}>紧凑显示与透明度仅改变本页外观，不控制系统画中画的透明度或大小。画中画可用系统拖动、双指缩放及贴边隐藏；iOS 不允许应用伪造任意大小的跨应用悬浮窗。设置自动保存，字号保留系统字体缩放。</Text>
              <MiniButton label="恢复外观默认值" onPress={() => setSettings((current) => ({ ...current, miniMode: false, windowOpacity: 1, miniOpacity: 0.85, fontScale: DEFAULT_SETTINGS.fontScale }))} />
              <SettingRow label="关于与更新">
                <Text style={styles.cardHint}>v{APP_VERSION} · iOS 原生版。仅检查和提醒，不自动下载安装；更新 IPA 仍需你自行签名，主 App 与广播扩展要一起签。</Text>
                <Switch accessibilityLabel="自动检查更新" value={settings.autoCheckUpdates} onValueChange={(autoCheckUpdates) => setSettings((current) => ({ ...current, autoCheckUpdates }))} />
                <View style={styles.segmented}>
                  {(['gitee', 'github'] as const).map((source) => <MiniButton key={source} label={`${settings.updateSource === source ? '✓ ' : ''}${source === 'gitee' ? 'Gitee 国内源' : 'GitHub 源'}`} onPress={() => setSettings((current) => ({ ...current, updateSource: source }))} />)}
                </View>
                <ActionButton label={checkingUpdate ? '检查中…' : '检查更新'} disabled={checkingUpdate} onPress={() => void runUpdateCheck(true)} />
                <Text selectable style={styles.cardHint}>{updateMessage}</Text>
                {updateNotes ? <Text selectable style={styles.cardHint}>{updateNotes}</Text> : null}
                <MiniButton label="iOS IPA 云构建 / 下载" onPress={() => openURL(IOS_BUILDS_URL)} />
                <MiniButton label="正式版更新说明" onPress={() => openURL(RELEASES_URL)} />
                <MiniButton label="使用教程 / BUG反馈 QQ群 1109044204" onPress={() => openURL('https://hy.312166.xyz')} />
              </SettingRow>
              <SettingRow label="诊断与隐私">
                <Text style={styles.cardHint}>导出 JSON 包含系统版本、连接状态、帧计数、识别耗时、豆数与校准；不含账号名、截图或原帧。截图校准只在设备内处理，退出编辑器后不会进入诊断包。</Text>
                <ActionButton label="导出脱敏诊断文件" disabled={!hasNativeCapture} onPress={() => void exportDiagnostics()} />
              </SettingRow>
              {settingsError || storageError ? <Text accessibilityRole="alert" style={styles.errorText}>{settingsError || storageError}</Text> : null}
            </ScrollView>
            <Modal animationType="slide" presentationStyle="fullScreen" visible={calibrationVisible} onRequestClose={() => setCalibrationVisible(false)}>
              <SafeAreaProvider><SafeAreaView style={styles.modalSafeArea}>
                {calibrationVisible ? <CalibrationEditor value={settings.calibration}
                  onSave={(calibration) => { setSettings((current) => ({ ...current, calibration })); setCalibrationVisible(false); }}
                  onClose={() => setCalibrationVisible(false)} pickImage={pickCalibrationImage}
                  importDocument={importCalibrationDocument} shareDocument={shareDocument} /> : null}
              </SafeAreaView></SafeAreaProvider>
            </Modal>
          </SafeAreaView>
        </SafeAreaProvider>
      </Modal>
    </SafeAreaView>
  );
}

function ActionButton({ label, onPress, disabled = false }: { label: string; onPress: () => void; disabled?: boolean }) {
  return <Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{ disabled }} disabled={disabled} style={[styles.button, styles.primaryButton, disabled && styles.disabledButton]} onPress={onPress}><Text style={styles.primaryText}>{label}</Text></Pressable>;
}

const styles = StyleSheet.create({
  safeArea: { flex: 1, backgroundColor: '#10131a' },
  modalSafeArea: { flex: 1, backgroundColor: '#10131a' },
  container: { padding: 16, paddingBottom: 48, width: '100%', maxWidth: 900, alignSelf: 'center' },
  settingsPanel: { padding: 20, paddingBottom: 48, width: '100%', maxWidth: 900, alignSelf: 'center' },
  eyebrow: { color: '#7f8ba3', fontSize: 11, letterSpacing: 1.4, marginBottom: 10 },
  headerLine: { alignItems: 'flex-start', flexDirection: 'row', justifyContent: 'space-between', gap: 8 },
  headerCopy: { flex: 1, minWidth: 0 },
  statusDot: { backgroundColor: '#f0ad4e', borderRadius: 6, flexShrink: 0, height: 12, marginTop: 3, width: 12 },
  readyDot: { backgroundColor: '#69d2ad' },
  readyText: { color: '#69d2ad' },
  title: { color: '#f5f7fb', fontSize: 34, fontWeight: '700' },
  subtitle: { color: '#aeb8cb', fontSize: 15, marginTop: 8 },
  statusCard: { backgroundColor: '#1b2130', borderColor: '#303b53', borderRadius: 18, borderWidth: 1, marginTop: 24, padding: 18 },
  infoCard: { backgroundColor: '#151b28', borderColor: '#29344a', borderRadius: 18, borderWidth: 1, marginTop: 18, padding: 18 },
  cardTitle: { color: '#dbe2f0', fontSize: 15, fontWeight: '700', flexShrink: 1 },
  statusText: { color: '#f0ad4e', fontSize: 19, fontWeight: '600', marginTop: 8 },
  cardHint: { color: '#a7b4c8', fontSize: 13, lineHeight: 20, marginTop: 8 },
  errorText: { color: '#ff9aa8', fontSize: 14, lineHeight: 21, marginTop: 12 },
  stepTitle: { color: '#dbe2f0', fontSize: 15, fontWeight: '700', marginTop: 18 },
  nativePreviewWrap: { backgroundColor: '#10131a', borderRadius: 10, marginTop: 12, overflow: 'hidden' },
  nativePreview: { height: 108, width: '100%' },
  outlineButton: { borderColor: '#3a91ad', borderRadius: 11, borderWidth: 1, justifyContent: 'center', minHeight: 44, minWidth: 44, marginTop: 14, paddingHorizontal: 14, paddingVertical: 11 },
  outlineText: { color: '#91d9ec', fontSize: 14, fontWeight: '600', textAlign: 'center' },
  disabledButton: { opacity: 0.45 },
  modeSelector: { marginTop: 22 },
  pillWrap: { alignSelf: 'center', marginTop: 22, maxWidth: 360, width: '100%' },
  pill: { backgroundColor: '#0a1622', borderColor: '#388ca8', borderRadius: 24, borderWidth: 1, minHeight: 48, paddingHorizontal: 18, paddingVertical: 13 },
  pillText: { color: '#b8f4ff', fontVariant: ['tabular-nums'], fontWeight: '700', textAlign: 'center' },
  pillControls: { backgroundColor: '#061820e6', borderRadius: 12, flexDirection: 'row', flexWrap: 'wrap', gap: 6, marginTop: 6, padding: 6 },
  miniButton: { alignItems: 'center', backgroundColor: '#123044', borderRadius: 8, flex: 1, minHeight: 44, minWidth: 72, justifyContent: 'center', paddingHorizontal: 5, paddingVertical: 10 },
  miniButtonText: { color: '#dbeef4', fontSize: 12, fontWeight: '600', textAlign: 'center' },
  fullCard: { backgroundColor: '#1b2130', borderColor: '#303b53', borderRadius: 20, borderWidth: 1, marginTop: 18, padding: 22 },
  clockRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 12, marginTop: 12 },
  clockColumn: { flexGrow: 1, flexShrink: 1, maxWidth: '100%' },
  fullClock: { color: '#ffffff', fontVariant: ['tabular-nums'], fontWeight: '700', marginTop: 8 },
  dualClock: { color: '#91d9ec', fontSize: 16, marginTop: 8 },
  actionRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 10, marginTop: 16 },
  button: { alignItems: 'center', justifyContent: 'center', borderRadius: 12, flexGrow: 1, flexBasis: 110, minHeight: 44, minWidth: 44, paddingHorizontal: 10, paddingVertical: 14 },
  primaryButton: { backgroundColor: '#e45063' },
  primaryText: { color: '#ffffff', fontSize: 15, fontWeight: '600', textAlign: 'center' },
  modalHeader: { alignItems: 'center', flexDirection: 'row', flexWrap: 'wrap', gap: 12, justifyContent: 'space-between', marginBottom: 24 },
  modalTitle: { color: '#f5f7fb', flexShrink: 1, fontSize: 26, fontWeight: '700' },
  closeButton: { alignItems: 'center', justifyContent: 'center', minHeight: 44, minWidth: 44, padding: 8 },
  closeText: { color: '#91d9ec', fontSize: 16, fontWeight: '600' },
  settingRow: { borderBottomColor: '#29344a', borderBottomWidth: 1, paddingVertical: 16 },
  settingLabel: { color: '#dbe2f0', fontSize: 15, marginBottom: 10 },
  segmented: { flexDirection: 'row', flexWrap: 'wrap', gap: 8 },
  segment: { backgroundColor: '#1b2130', borderRadius: 9, flexGrow: 1, flexBasis: 76, justifyContent: 'center', minHeight: 44, minWidth: 44, paddingHorizontal: 6, paddingVertical: 11 },
  segmentSelected: { backgroundColor: '#255d73' },
  segmentText: { color: '#dbe2f0', fontSize: 13, textAlign: 'center' },
  switch: { alignSelf: 'flex-start', minHeight: 44 },
  input: { backgroundColor: '#1b2130', borderColor: '#303b53', borderRadius: 9, borderWidth: 1, color: '#f5f7fb', minHeight: 44, paddingHorizontal: 12, paddingVertical: 10 },
  settingsNote: { color: '#a7b4c8', fontSize: 13, lineHeight: 20, marginTop: 20 },
  stepper: { alignItems: 'center', flexDirection: 'row', flexWrap: 'wrap', gap: 12 },
  stepButton: { alignItems: 'center', backgroundColor: '#1b2130', borderRadius: 8, minHeight: 44, justifyContent: 'center', minWidth: 44, paddingHorizontal: 10 },
  stepText: { color: '#91d9ec', fontSize: 20 },
  stepValue: { color: '#f5f7fb', fontSize: 15, minWidth: 52, textAlign: 'center' },
});
