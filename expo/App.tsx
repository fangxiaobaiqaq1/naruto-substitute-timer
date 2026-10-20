import { StatusBar } from 'expo-status-bar';
import { useEffect, useState, type ReactNode } from 'react';
import {
  Alert,
  Modal,
  Pressable,
  SafeAreaView,
  ScrollView,
  StyleSheet,
  Switch,
  Text,
  TextInput,
  useWindowDimensions,
  View,
} from 'react-native';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { getReplayKitState, prepareReplayKitBroadcast } from './replayKit';
import { useCountdownNow } from './ios-compat/useCountdownNow';

type SideMode = 'auto' | 'left' | 'right';
type TimerSide = 'left' | 'right';

type MobileSettings = {
  playerSide: SideMode;
  rememberSide: boolean;
  playerName: string;
  opponentNinja: string;
  showBothSides: boolean;
  miniMode: boolean;
  windowOpacity: number;
  miniOpacity: number;
  fontScale: number;
};

const SETTINGS_KEY = 'naruto-timer-ios-settings-v1';
const DEFAULT_SETTINGS: MobileSettings = {
  playerSide: 'auto',
  rememberSide: true,
  playerName: '',
  opponentNinja: '',
  showBothSides: false,
  miniMode: false,
  windowOpacity: 1,
  miniOpacity: 0.85,
  fontScale: 0.75,
};
const PRIMARY_SECONDS = 15;
const ALTERNATE_SECONDS = 10;

function formatSeconds(value: number): string {
  if (value <= 0) return '—';
  return value.toFixed(1);
}

function isDualNinja(name: string): boolean {
  const normalized = name.replace(/[\s\[\]【】()（）·・]/g, '');
  return normalized === '照美冥五代目水影';
}

function normalizeSettings(value: unknown): MobileSettings {
  const source = value && typeof value === 'object' ? value as Partial<MobileSettings> : {};
  const playerSide: SideMode = source.playerSide === 'left' || source.playerSide === 'right' ? source.playerSide : 'auto';
  const miniOpacity = typeof source.miniOpacity === 'number' && Number.isFinite(source.miniOpacity)
    ? Math.min(1, Math.max(0.2, source.miniOpacity))
    : DEFAULT_SETTINGS.miniOpacity;
  const windowOpacity = typeof source.windowOpacity === 'number' && Number.isFinite(source.windowOpacity)
    ? Math.min(1, Math.max(0.4, source.windowOpacity))
    : DEFAULT_SETTINGS.windowOpacity;
  const fontScale = typeof source.fontScale === 'number' && Number.isFinite(source.fontScale)
    ? Math.min(1.2, Math.max(0.55, source.fontScale))
    : DEFAULT_SETTINGS.fontScale;
  const rememberSide = typeof source.rememberSide === 'boolean' ? source.rememberSide : DEFAULT_SETTINGS.rememberSide;
  return {
    playerSide: rememberSide ? playerSide : 'auto',
    rememberSide,
    playerName: typeof source.playerName === 'string' ? source.playerName : '',
    opponentNinja: typeof source.opponentNinja === 'string' ? source.opponentNinja : '',
    showBothSides: typeof source.showBothSides === 'boolean' ? source.showBothSides : DEFAULT_SETTINGS.showBothSides,
    miniMode: typeof source.miniMode === 'boolean' ? source.miniMode : DEFAULT_SETTINGS.miniMode,
    windowOpacity,
    miniOpacity,
    fontScale,
  };
}

function sideModeText(settings: MobileSettings, detectedSide: TimerSide | ''): string {
  if (settings.playerSide === 'left') return '左';
  if (settings.playerSide === 'right') return '右';
  if (detectedSide === 'left') return '自动·左';
  if (detectedSide === 'right') return '自动·右';
  return '自动';
}

function valueOrWaiting(value: string): string {
  return value === '—' ? '等待决斗' : value;
}

function MiniPill({
  settings,
  left,
  right,
  alternate,
  detectedSide,
  controlsVisible,
  onToggleControls,
  onSwap,
  onToggleBoth,
  onOpenSettings,
  onExitMini,
}: {
  settings: MobileSettings;
  left: string;
  right: string;
  alternate: string;
  detectedSide: TimerSide | '';
  controlsVisible: boolean;
  onToggleControls: () => void;
  onSwap: () => void;
  onToggleBoth: () => void;
  onOpenSettings: () => void;
  onExitMini: () => void;
}) {
  const mode = sideModeText(settings, detectedSide);
  const opponentSide = settings.playerSide === 'left'
    ? 'right'
    : settings.playerSide === 'right'
      ? 'left'
      : detectedSide === 'left'
        ? 'right'
        : detectedSide === 'right'
          ? 'left'
          : '';
  const primary = opponentSide === 'left' ? left : opponentSide === 'right' ? right : '—';
  const dual = isDualNinja(settings.opponentNinja);
  const text = settings.showBothSides
    ? `替身计时·${mode}：左 ${valueOrWaiting(left)} / 右 ${valueOrWaiting(right)}`
    : dual
      ? `替身计时·${mode}：15秒 ${valueOrWaiting(primary)} / 10秒 ${alternate}`
      : `替身计时·${mode}：${valueOrWaiting(primary)}`;

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
          <MiniButton label="退出" danger onPress={onExitMini} />
        </View>
      ) : null}
    </View>
  );
}

function MiniButton({ label, onPress, danger = false }: { label: string; onPress: () => void; danger?: boolean }) {
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} style={[styles.miniButton, danger && styles.dangerButton]}>
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

function ValueStepper({ label, value, minimum, maximum, step, onChange }: { label: string; value: number; minimum: number; maximum: number; step: number; onChange: (value: number) => void }) {
  const change = (delta: number) => onChange(Math.min(maximum, Math.max(minimum, Math.round((value + delta) * 100) / 100)));
  return (
    <View style={styles.stepper}>
      <Pressable accessibilityRole="button" accessibilityLabel={`减小${label}`} accessibilityState={{ disabled: value <= minimum }} disabled={value <= minimum} style={styles.stepButton} onPress={() => change(-step)}><Text style={styles.stepText}>−</Text></Pressable>
      <Text accessibilityLabel={`${label} ${Math.round(value * 100)}%`} style={styles.stepValue}>{Math.round(value * 100)}%</Text>
      <Pressable accessibilityRole="button" accessibilityLabel={`增大${label}`} accessibilityState={{ disabled: value >= maximum }} disabled={value >= maximum} style={styles.stepButton} onPress={() => change(step)}><Text style={styles.stepText}>＋</Text></Pressable>
    </View>
  );
}

export default function App() {
  const { fontScale: systemFontScale } = useWindowDimensions();
  const [settings, setSettings] = useState<MobileSettings>(DEFAULT_SETTINGS);
  const [settingsVisible, setSettingsVisible] = useState(false);
  const [controlsVisible, setControlsVisible] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [replayKit, setReplayKit] = useState(getReplayKitState);
  const [detectedSide, setDetectedSide] = useState<TimerSide | ''>('');
  const [deadlines, setDeadlines] = useState({ left: 0, right: 0, leftAlternate: 0, rightAlternate: 0 });
  const now = useCountdownNow(Math.max(deadlines.left, deadlines.right, deadlines.leftAlternate, deadlines.rightAlternate));

  useEffect(() => {
    AsyncStorage.getItem(SETTINGS_KEY)
      .then((stored) => {
        if (stored) setSettings(normalizeSettings(JSON.parse(stored)));
      })
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, []);

  useEffect(() => {
    if (loaded) void AsyncStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)).catch(() => undefined);
  }, [loaded, settings]);

  useEffect(() => {
    if (!controlsVisible) return;
    const timer = setTimeout(() => setControlsVisible(false), 5000);
    return () => clearTimeout(timer);
  }, [controlsVisible]);

  const dual = isDualNinja(settings.opponentNinja);
  const leftRemaining = Math.max(0, (deadlines.left - now) / 1000);
  const rightRemaining = Math.max(0, (deadlines.right - now) / 1000);
  const leftText = formatSeconds(leftRemaining);
  const rightText = formatSeconds(rightRemaining);
  const opponentSide = settings.playerSide === 'left'
    ? 'right'
    : settings.playerSide === 'right'
      ? 'left'
      : detectedSide === 'left'
        ? 'right'
        : detectedSide === 'right'
          ? 'left'
          : '';
  const alternateDeadline = opponentSide === 'left' ? deadlines.leftAlternate : opponentSide === 'right' ? deadlines.rightAlternate : 0;
  const alternate = alternateDeadline === 0 ? '—' : alternateDeadline <= now ? '就绪' : formatSeconds((alternateDeadline - now) / 1000);
  const primaryText = opponentSide === 'left' ? leftText : opponentSide === 'right' ? rightText : '—';
  const statusText = replayKit.status === 'available' ? '广播组件可用（未确认采集）' : '前台演示模式';
  const running = leftRemaining > 0 || rightRemaining > 0;

  const startCooldown = (side: TimerSide) => {
    // The tapped side is the observed cooldown side; auto mode stores our side.
    setDetectedSide(side === 'left' ? 'right' : 'left');
    const startedAt = Date.now();
    const primaryDeadline = startedAt + PRIMARY_SECONDS * 1000;
    const alternateDeadline = startedAt + ALTERNATE_SECONDS * 1000;
    if (side === 'left') {
      setDeadlines((current) => ({ ...current, left: primaryDeadline, leftAlternate: dual ? alternateDeadline : 0 }));
    } else {
      setDeadlines((current) => ({ ...current, right: primaryDeadline, rightAlternate: dual ? alternateDeadline : 0 }));
    }
  };

  const swapSide = () => {
    const playerSide = settings.playerSide === 'auto' ? 'left' : settings.playerSide === 'left' ? 'right' : 'auto';
    if (playerSide === 'auto') setDetectedSide('');
    setSettings((current) => ({ ...current, playerSide }));
  };

  const requestReplayKit = async () => {
    const state = await prepareReplayKitBroadcast();
    setReplayKit(state);
    Alert.alert(
      'ReplayKit 状态',
      `${state.message}\n\n${state.status === 'available'
        ? '组件可用不代表广播已启动；此界面尚未确认任何画面采集。'
        : '当前仅支持前台手动计时，无可用的广播扩展，请勿在系统录屏中寻找替身计时器。'}`,
    );
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
            <Text style={styles.subtitle}>基于 Android 计时语义的 iOS 前台版</Text>
          </View>
          <View style={styles.statusDot} />
        </View>

        <View style={styles.statusCard}>
          <Text style={styles.cardTitle}>采集状态</Text>
          <Text style={styles.statusText}>{statusText}</Text>
          <Text style={styles.cardHint}>{replayKit.message}</Text>
          <Pressable accessibilityRole="button" accessibilityLabel="检查 ReplayKit 状态" style={styles.outlineButton} onPress={requestReplayKit}>
            <Text style={styles.outlineText}>检查 ReplayKit 状态</Text>
          </Pressable>
        </View>

        {settings.miniMode ? (
          <MiniPill
            settings={settings}
            left={leftText}
            right={rightText}
            alternate={alternate}
            detectedSide={detectedSide}
            controlsVisible={controlsVisible}
            onToggleControls={() => setControlsVisible((visible) => !visible)}
            onSwap={swapSide}
            onToggleBoth={() => setSettings((current) => ({ ...current, showBothSides: !current.showBothSides }))}
            onOpenSettings={() => setSettingsVisible(true)}
            onExitMini={() => { setControlsVisible(false); setSettings((current) => ({ ...current, miniMode: false })); }}
          />
        ) : (
          <View style={[styles.fullCard, { opacity: settings.windowOpacity }]}>
            <Text style={styles.cardTitle}>{running ? '计时中' : '等待决斗'}</Text>
            {settings.showBothSides ? (
              <View style={styles.clockRow}>
                {([['左侧', leftText], ['右侧', rightText]] as const).map(([label, value]) => (
                  <View key={label} style={[styles.clockColumn, { flexBasis: 132 * systemFontScale }]}>
                    <Text style={styles.cardTitle}>{label}</Text>
                    <Text accessibilityLabel={`${label}替身倒计时：${valueOrWaiting(value)}`} style={styles.fullClock}>{value}</Text>
                  </View>
                ))}
              </View>
            ) : <Text accessibilityLabel={`对面替身倒计时：${valueOrWaiting(primaryText)}`} style={styles.fullClock}>{primaryText}</Text>}
            {dual ? <Text style={styles.dualClock}>15秒 {primaryText}  /  10秒 {alternate}</Text> : null}
            <View style={styles.actionRow}>
              <ActionButton label="左侧掉落" onPress={() => startCooldown('left')} />
              <ActionButton label="右侧掉落" onPress={() => startCooldown('right')} />
            </View>
            <Pressable accessibilityRole="button" accessibilityLabel="切换迷你模式" style={styles.outlineButton} onPress={() => setSettings((current) => ({ ...current, miniMode: true }))}>
              <Text style={styles.outlineText}>切换迷你 pill</Text>
            </Pressable>
          </View>
        )}

        {settings.miniMode ? (
          <View style={styles.actionRow}>
            <ActionButton label="左侧掉落" onPress={() => startCooldown('left')} />
            <ActionButton label="右侧掉落" onPress={() => startCooldown('right')} />
          </View>
        ) : null}

        <View style={styles.infoCard}>
          <Text style={styles.cardTitle}>iOS 使用边界</Text>
          <Text style={styles.cardHint}>此界面用于手动前台计时，iOS 第三方 App 不能在游戏上方常驻绘制。后台暂停刷新，倒计时仍按实际时间计算；回到前台立即更新。未接入 ReplayKit 扩展时不会采集游戏画面。</Text>
        </View>
      </ScrollView>

      <Modal animationType="slide" presentationStyle="pageSheet" visible={settingsVisible} onRequestClose={() => setSettingsVisible(false)}>
        <SafeAreaView style={styles.modalSafeArea}>
          <ScrollView automaticallyAdjustKeyboardInsets keyboardDismissMode="interactive" keyboardShouldPersistTaps="handled" contentContainerStyle={styles.settingsPanel}>
            <View style={styles.modalHeader}>
              <Text style={styles.modalTitle}>替身设置</Text>
              <Pressable accessibilityRole="button" accessibilityLabel="完成设置" style={styles.closeButton} onPress={saveSettings}><Text style={styles.closeText}>完成</Text></Pressable>
            </View>
            <SettingRow label="我方边">
              <View style={styles.segmented}>
                {([['auto', '自动认边'], ['left', '左边'], ['right', '右边']] as const).map(([value, label]) => (
                  <Pressable key={value} accessibilityRole="button" accessibilityLabel={`我方边：${label}`} accessibilityState={{ selected: settings.playerSide === value }} onPress={() => { if (value === 'auto') setDetectedSide(''); setSettings((current) => ({ ...current, playerSide: value })); }} style={[styles.segment, settings.playerSide === value && styles.segmentSelected]}>
                    <Text style={styles.segmentText}>{label}</Text>
                  </Pressable>
                ))}
              </View>
            </SettingRow>
            <SettingRow label="记住认边">
              <Switch accessibilityLabel="记住认边" style={styles.switch} hitSlop={7} value={settings.rememberSide} onValueChange={(value) => { if (!value) setDetectedSide(''); setSettings((current) => ({ ...current, rememberSide: value, playerSide: value ? current.playerSide : 'auto' })); }} />
            </SettingRow>
            <SettingRow label="我的名字">
              <TextInput accessibilityLabel="我的名字" returnKeyType="done" value={settings.playerName} onChangeText={(value) => setSettings((current) => ({ ...current, playerName: value }))} placeholder="可留空自动识别" placeholderTextColor="#71819a" style={styles.input} />
            </SettingRow>
            <SettingRow label="对面忍者">
              <TextInput accessibilityLabel="对面忍者" returnKeyType="done" value={settings.opponentNinja} onChangeText={(value) => setSettings((current) => ({ ...current, opponentNinja: value }))} placeholder="如：照美冥" placeholderTextColor="#71819a" style={styles.input} />
            </SettingRow>
            <SettingRow label="两边计时">
              <Switch accessibilityLabel="两边计时" style={styles.switch} hitSlop={7} value={settings.showBothSides} onValueChange={(value) => setSettings((current) => ({ ...current, showBothSides: value }))} />
            </SettingRow>
            <SettingRow label="迷你模式">
              <Switch accessibilityLabel="迷你模式" style={styles.switch} hitSlop={7} value={settings.miniMode} onValueChange={(value) => setSettings((current) => ({ ...current, miniMode: value }))} />
            </SettingRow>
            <SettingRow label="完整浮窗透明度">
              <ValueStepper label="完整浮窗透明度" value={settings.windowOpacity} minimum={0.4} maximum={1} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, windowOpacity: value }))} />
            </SettingRow>
            <SettingRow label="迷你浮窗透明度">
              <ValueStepper label="迷你浮窗透明度" value={settings.miniOpacity} minimum={0.2} maximum={1} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, miniOpacity: value }))} />
            </SettingRow>
            <SettingRow label="字号">
              <ValueStepper label="字号" value={settings.fontScale} minimum={0.55} maximum={1.2} step={0.05} onChange={(value) => setSettings((current) => ({ ...current, fontScale: value }))} />
            </SettingRow>
            <Text style={styles.settingsNote}>透明度和字号沿用 Android 的语义，并保留系统字体缩放。这些设置仅影响前台界面，不会启用跨 App 悬浮窗或系统录屏扩展。</Text>
          </ScrollView>
        </SafeAreaView>
      </Modal>
    </SafeAreaView>
  );
}

function ActionButton({ label, onPress }: { label: string; onPress: () => void }) {
  return <Pressable accessibilityRole="button" accessibilityLabel={label} style={[styles.button, styles.primaryButton]} onPress={onPress}><Text style={styles.primaryText}>{label}</Text></Pressable>;
}

const styles = StyleSheet.create({
  safeArea: { flex: 1, backgroundColor: '#10131a' },
  modalSafeArea: { flex: 1, backgroundColor: '#10131a' },
  container: { padding: 16, paddingBottom: 48 },
  settingsPanel: { padding: 20, paddingBottom: 48 },
  eyebrow: { color: '#7f8ba3', fontSize: 11, letterSpacing: 1.4, marginBottom: 10 },
  headerLine: { alignItems: 'flex-start', flexDirection: 'row', justifyContent: 'space-between' },
  headerCopy: { flex: 1, minWidth: 0, marginRight: 12 },
  statusDot: { backgroundColor: '#f0ad4e', borderRadius: 6, flexShrink: 0, height: 12, marginTop: 12, width: 12 },
  title: { color: '#f5f7fb', fontSize: 34, fontWeight: '700' },
  subtitle: { color: '#aeb8cb', fontSize: 15, marginTop: 8 },
  statusCard: { backgroundColor: '#1b2130', borderColor: '#303b53', borderRadius: 18, borderWidth: 1, marginTop: 28, padding: 18 },
  infoCard: { backgroundColor: '#151b28', borderColor: '#29344a', borderRadius: 18, borderWidth: 1, marginTop: 18, padding: 18 },
  cardTitle: { color: '#dbe2f0', fontSize: 15, fontWeight: '700' },
  statusText: { color: '#f0ad4e', fontSize: 19, fontWeight: '600', marginTop: 8 },
  cardHint: { color: '#8997ad', fontSize: 13, lineHeight: 19, marginTop: 8 },
  outlineButton: { borderColor: '#3a91ad', borderRadius: 11, borderWidth: 1, justifyContent: 'center', minHeight: 44, minWidth: 44, marginTop: 14, paddingHorizontal: 14, paddingVertical: 11 },
  outlineText: { color: '#91d9ec', fontSize: 14, fontWeight: '600', textAlign: 'center' },
  pillWrap: { alignSelf: 'center', marginTop: 22, maxWidth: 360, width: '100%' },
  pill: { backgroundColor: '#0a1622', borderColor: '#388ca8', borderRadius: 24, borderWidth: 1, minHeight: 48, paddingHorizontal: 18, paddingVertical: 13 },
  pillText: { color: '#b8f4ff', fontVariant: ['tabular-nums'], fontWeight: '700', textAlign: 'center' },
  pillControls: { backgroundColor: '#061820e6', borderRadius: 12, flexDirection: 'row', flexWrap: 'wrap', gap: 6, marginTop: 6, padding: 6 },
  miniButton: { alignItems: 'center', backgroundColor: '#123044', borderRadius: 8, flex: 1, minHeight: 44, minWidth: 72, justifyContent: 'center', paddingHorizontal: 5, paddingVertical: 10 },
  dangerButton: { backgroundColor: '#7b3040' },
  miniButtonText: { color: '#dbeef4', fontSize: 12, fontWeight: '600', textAlign: 'center' },
  fullCard: { backgroundColor: '#1b2130', borderColor: '#303b53', borderRadius: 20, borderWidth: 1, marginTop: 22, padding: 22 },
  clockRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 12, marginTop: 12 },
  clockColumn: { flexGrow: 1, flexShrink: 1, maxWidth: '100%' },
  fullClock: { color: '#ffffff', fontSize: 52, fontVariant: ['tabular-nums'], fontWeight: '700', marginTop: 8 },
  dualClock: { color: '#91d9ec', fontSize: 16, marginTop: 8 },
  actionRow: { flexDirection: 'row', flexWrap: 'wrap', gap: 10, marginTop: 16 },
  button: { alignItems: 'center', justifyContent: 'center', borderRadius: 12, flexGrow: 1, flexBasis: 100, minHeight: 44, minWidth: 44, paddingHorizontal: 10, paddingVertical: 14 },
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
  segmentText: { color: '#dbe2f0', fontSize: 12, textAlign: 'center' },
  switch: { alignSelf: 'flex-start', minHeight: 44 },
  input: { backgroundColor: '#1b2130', borderColor: '#303b53', borderRadius: 9, borderWidth: 1, color: '#f5f7fb', minHeight: 44, paddingHorizontal: 12, paddingVertical: 10 },
  settingsNote: { color: '#8997ad', fontSize: 13, lineHeight: 19, marginTop: 20 },
  stepper: { alignItems: 'center', flexDirection: 'row', flexWrap: 'wrap', gap: 12 },
  stepButton: { alignItems: 'center', backgroundColor: '#1b2130', borderRadius: 8, minHeight: 44, justifyContent: 'center', minWidth: 44, paddingHorizontal: 10 },
  stepText: { color: '#91d9ec', fontSize: 20 },
  stepValue: { color: '#f5f7fb', fontSize: 15, minWidth: 52, textAlign: 'center' },
});
