import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Image, PanResponder, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from 'react-native';
import { SafeAreaProvider, SafeAreaView } from 'react-native-safe-area-context';
import {
  CALIBRATION_KEYS, CALIBRATION_NAMES, calibrationAspectDifference, calibrationRectIssue,
  encodeCalibration, normalizeCalibration, parseCalibration, validateCalibration,
  type Calibration, type CalibrationKey, type CalibrationRect, type CalibrationScheme,
} from './calibration';

export type CalibrationEditorProps = {
  value: Calibration;
  onSave: (calibration: Calibration) => void;
  onClose: () => void;
  pickImage: () => Promise<{ uri: string; width: number; height: number }>;
  importDocument: () => Promise<string>;
  shareDocument: (name: string, text: string) => Promise<void>;
};
type Screenshot = Awaited<ReturnType<CalibrationEditorProps['pickImage']>>;
type Mode = 'draw' | 'move' | 'resize';
type Drag = {
  key: CalibrationKey; mode: Mode; x: number; y: number;
  width: number; height: number; rect?: CalibrationRect; before: Calibration;
};
const COLORS = ['#67e8f9', '#fbbf24', '#c4b5fd', '#f9a8d4'];
const clamp = (n: number, min: number, max: number) => Math.max(min, Math.min(max, n));
const equal = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const message = (error: unknown) => error instanceof Error ? error.message : String(error);
function confirm(title: string, text: string): Promise<boolean> {
  return new Promise(resolve => Alert.alert(title, text, [
    { text: '取消', style: 'cancel', onPress: () => resolve(false) },
    { text: '继续', onPress: () => resolve(true) },
  ], { cancelable: true, onDismiss: () => resolve(false) }));
}
function Button({ label, onPress, selected, disabled, accessibilityLabel }: {
  label: string; onPress: () => void; selected?: boolean; disabled?: boolean; accessibilityLabel?: string;
}) {
  return <Pressable accessibilityRole="button" accessibilityLabel={accessibilityLabel || label}
    accessibilityState={{ selected, disabled }} disabled={disabled} onPress={onPress}
    style={[styles.button, selected && styles.selected, disabled && styles.disabled]}>
    <Text style={styles.buttonText}>{label}</Text>
  </Pressable>;
}

/** Mount afresh inside the parent's Modal for every editing session. No native capture/recognition. */
export function CalibrationEditor(props: CalibrationEditorProps) {
  // A provider local to the Modal measures its own safe areas, including iPad rotation/split view.
  return <SafeAreaProvider><Editor {...props} /></SafeAreaProvider>;
}
export default CalibrationEditor;

function Editor({ value, onSave, onClose, pickImage, importDocument, shareDocument }: CalibrationEditorProps) {
  const [original] = useState(() => normalizeCalibration(value));
  const [draft, setDraft] = useState(original);
  const [image, setImage] = useState<Screenshot | null>(null);
  const [imageReady, setImageReady] = useState(false);
  const [imageVersion, setImageVersion] = useState(0);
  const [selected, setSelected] = useState<CalibrationKey>('leftBeads');
  const [mode, setMode] = useState<Mode>('draw');
  const [step, setStep] = useState(1);
  const [pending, setPending] = useState<CalibrationScheme | null>(null);
  const [sourceHint, setSourceHint] = useState<CalibrationScheme | null>(null);
  const [busy, setBusy] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [availableWidth, setAvailableWidth] = useState(0);
  const window = useWindowDimensions();
  const alive = useRef(true);
  const busyRef = useRef(false);
  const drag = useRef<Drag | null>(null);
  const scale = image && availableWidth > 0
    ? Math.min(availableWidth / image.width, Math.max(180, window.height * 0.48) / image.height) : 0;
  const canvasWidth = image ? image.width * scale : 0;
  const canvasHeight = image ? image.height * scale : 0;
  const current = useRef({ draft, selected, mode, image, imageReady, canvasWidth, canvasHeight, busy });
  current.current = { draft, selected, mode, image, imageReady, canvasWidth, canvasHeight, busy };
  const update = (next: Calibration) => {
    current.current.draft = next;
    setDraft(next);
  };
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  // A rotation during a gesture must not reinterpret its old coordinates at the new scale.
  useEffect(() => {
    if (drag.current) update(drag.current.before);
    drag.current = null;
    setDragging(false);
  }, [canvasWidth, canvasHeight]);

  const responder = useMemo(() => PanResponder.create({
    onStartShouldSetPanResponder: () => current.current.imageReady && !busyRef.current,
    onMoveShouldSetPanResponder: () => current.current.imageReady && !busyRef.current,
    onPanResponderTerminationRequest: () => false,
    onPanResponderGrant: event => {
      const c = current.current;
      if (!c.image || !c.imageReady || !c.canvasWidth || !c.canvasHeight || busyRef.current) return;
      const x = clamp(event.nativeEvent.locationX / c.canvasWidth, 0, 1);
      const y = clamp(event.nativeEvent.locationY / c.canvasHeight, 0, 1);
      let key = c.selected;
      if (c.mode !== 'draw') {
        const hit = [c.selected, ...CALIBRATION_KEYS.filter(k => k !== c.selected)].find(k => {
          const b = c.draft[k];
          return b && x >= b.x && x <= b.x + b.width && y >= b.y && y <= b.y + b.height;
        });
        if (hit) { key = hit; setSelected(hit); }
        if (!c.draft[key]) return;
      }
      drag.current = { key, mode: c.mode, x, y, width: c.canvasWidth, height: c.canvasHeight,
        rect: c.draft[key], before: c.draft };
      setDragging(true);
    },
    onPanResponderMove: (_event, gesture) => {
      const d = drag.current;
      const img = current.current.image;
      if (!d || !img) return;
      const dx = Math.round(gesture.dx / d.width * img.width) / img.width;
      const dy = Math.round(gesture.dy / d.height * img.height) / img.height;
      let rect: CalibrationRect;
      if (d.mode === 'draw') {
        if (Math.abs(gesture.dx) + Math.abs(gesture.dy) < 3) return;
        const x = clamp(d.x + dx, 0, 1), y = clamp(d.y + dy, 0, 1);
        rect = { x: Math.min(d.x, x), y: Math.min(d.y, y), width: Math.abs(x - d.x), height: Math.abs(y - d.y) };
      } else if (d.rect) {
        const b = d.rect;
        rect = d.mode === 'move'
          ? { ...b, x: clamp(b.x + dx, 0, 1 - b.width), y: clamp(b.y + dy, 0, 1 - b.height) }
          : { ...b, width: clamp(b.width + dx, 1 / img.width, 1 - b.x), height: clamp(b.height + dy, 1 / img.height, 1 - b.y) };
      } else return;
      update({ ...current.current.draft, [d.key]: rect });
    },
    onPanResponderRelease: () => { drag.current = null; setDragging(false); },
    onPanResponderTerminate: () => {
      if (drag.current) update(drag.current.before);
      drag.current = null;
      setDragging(false);
    },
  }), []);

  const dirty = !equal(draft, original);
  const rect = draft[selected];
  const issues = CALIBRATION_KEYS.flatMap(key => draft[key] ? [calibrationRectIssue(key, draft[key])].filter(Boolean) : []);
  const run = async (operation: () => Promise<void>) => {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    try { await operation(); }
    catch (error) { if (alive.current) Alert.alert('操作未完成', message(error)); }
    finally { busyRef.current = false; if (alive.current) setBusy(false); }
  };
  const chooseImage = () => void run(async () => {
    const next = await pickImage();
    if (!alive.current) return;
    if (!next || typeof next.uri !== 'string' || !next.uri || !Number.isInteger(next.width) ||
        !Number.isInteger(next.height) || next.width <= 0 || next.height <= 0 || next.width > 100_000 || next.height > 100_000) {
      throw new Error('截图必须有有效的原始宽高，请重新从“文件”选择图片。');
    }
    const difference = sourceHint && calibrationAspectDifference(sourceHint, next.width, next.height);
    if (difference != null && difference > 0.02 && !await confirm('画面比例不一致',
      '截图与已应用方案的宽高比相差超过 2%，校准可能不准。仍然使用这张截图？')) return;
    if (!alive.current) return;
    setImageReady(false);
    setImageVersion(version => version + 1);
    setImage(next);
  });
  const loadDocument = () => void run(async () => {
    const text = await importDocument();
    if (alive.current) setPending(parseCalibration(text));
  });
  const applyImport = () => void run(async () => {
    if (!pending) return;
    const warnings = ['整套替换编辑器中的框；文件里没有的框将恢复自动。应用后仍须点“保存”。'];
    if (dirty) warnings.push('当前未保存的改动将被替换。');
    const difference = image && calibrationAspectDifference(pending, image.width, image.height);
    if (difference != null && difference > 0.02) warnings.push('注意：方案与截图的宽高比相差超过 2%，校准可能不准。');
    if (pending.calibration.profile !== draft.profile) warnings.push('方案布局与当前选择不同，将切换为文件中的布局。');
    if (!image) warnings.push('尚未选择截图，无法核对画面比例。');
    if (!await confirm('应用导入方案？', warnings.join('\n\n')) || !alive.current) return;
    update(pending.calibration);
    setSourceHint(pending);
    setPending(null);
  });
  const save = () => {
    try {
      const checked = validateCalibration(current.current.draft);
      if (!equal(checked, original)) onSave(checked);
      onClose();
    } catch (error) { Alert.alert('不能保存', message(error)); }
  };
  const cancel = () => void run(async () => {
    if ((!dirty && !pending || await confirm('放弃校准？', '未保存的改动和待应用方案都会丢弃。')) && alive.current) onClose();
  });
  const exportDocument = () => void run(async () => {
    const width = image?.width || sourceHint?.frameWidth || 0;
    const height = image?.height || sourceHint?.frameHeight || 0;
    const text = encodeCalibration(current.current.draft, width, height);
    await shareDocument(width && height ? `校准方案-${width}x${height}.json` : '校准方案.json', text);
  });
  const reset = (all: boolean) => void run(async () => {
    if (!await confirm(all ? '全部恢复自动？' : `清除${CALIBRATION_NAMES[selected]}？`,
      '仅改变编辑器内容，点“保存”后生效。') || !alive.current) return;
    const next = { ...current.current.draft };
    for (const key of all ? CALIBRATION_KEYS : [selected]) delete next[key];
    update(next);
  });
  const nudge = (field: keyof CalibrationRect, direction: number) => {
    const b = current.current.draft[selected];
    if (!b || !image) return;
    const axis = field === 'x' || field === 'width' ? image.width : image.height;
    const max = field === 'x' ? 1 - b.width : field === 'y' ? 1 - b.height : field === 'width' ? 1 - b.x : 1 - b.y;
    const min = field === 'width' || field === 'height' ? 1 / axis : 0;
    update({ ...current.current.draft, [selected]: { ...b, [field]: clamp(b[field] + direction * step / axis, min, max) } });
  };
  const status = (key: CalibrationKey) => !equal(draft[key], original[key])
    ? draft[key] ? '未保存' : '将恢复自动' : draft[key] ? '已保存' : '自动（无默认框）';

  return <SafeAreaView style={styles.root}>
    <View style={styles.header}>
      <Text accessibilityRole="header" style={styles.title}>区域校准</Text>
      <Button label="取消" onPress={cancel} disabled={busy || dragging} />
      <Button label="保存" onPress={save} disabled={busy || dragging} />
    </View>
    <ScrollView contentContainerStyle={styles.content} scrollEnabled={!dragging}>
      <Text style={styles.text}>从“文件”选择完整游戏截图；不截取直播帧、不运行识别，也不会凭空生成自动位置。未画的区域保持自动。</Text>
      <View style={styles.row}>
        <Button label={image ? '更换截图（文件）' : '选择截图（文件）'} onPress={chooseImage} disabled={busy} />
        <Button label="导入方案" onPress={loadDocument} disabled={busy} />
        <Button label="导出方案" onPress={exportDocument} disabled={busy} />
      </View>
      {busy && <Text accessibilityLiveRegion="polite" style={styles.text}>正在处理，请稍候…</Text>}
      {pending && <View style={styles.notice}>
        <Text style={styles.text}>待应用方案：{pending.calibration.profile === 'camp' ? '训练场' : '决斗场'}，
          {pending.frameWidth} × {pending.frameHeight}（0 表示未知），{CALIBRATION_KEYS.filter(key => pending.calibration[key]).length} 个框。尚未生效。</Text>
        <View style={styles.row}>
          <Button label="应用到编辑器" onPress={applyImport} disabled={busy} />
          <Button label="丢弃导入" onPress={() => setPending(null)} disabled={busy} />
        </View>
      </View>}
      <Text style={styles.text}>截图布局（请手动选）：</Text>
      <View style={styles.row}>
        <Button label="训练场" selected={draft.profile === 'camp'} onPress={() => update({ ...draft, profile: 'camp' })} disabled={busy} />
        <Button label="决斗场 / 对局" selected={draft.profile === 'duel'} onPress={() => update({ ...draft, profile: 'duel' })} disabled={busy} />
      </View>
      <Text style={styles.warning}>豆框必须是普通忍者的标准豆行，只框 4 颗普通豆，不含柱间第 5、6 颗。文件与截图不提供本帧识别几何，不能自动换算特殊忍者的下移；九喇嘛水门、侠隐佐助、百战鼬请换普通忍者截图再框。</Text>
      <Text style={styles.text}>头像框贴紧菱形四个顶点的外接矩形（含细边框），不含名字、血条及伸出的头发。豆框的四个点应对准豆心。</Text>
      <View style={styles.row}>{CALIBRATION_KEYS.map(key => <Button key={key}
        label={`${CALIBRATION_NAMES[key]} · ${status(key)}`} selected={selected === key}
        onPress={() => setSelected(key)} disabled={busy} />)}</View>
      <View style={styles.row}>
        <Button label="重画" selected={mode === 'draw'} onPress={() => setMode('draw')} disabled={busy} />
        <Button label="移动" selected={mode === 'move'} onPress={() => setMode('move')} disabled={busy} />
        <Button label="调大小" selected={mode === 'resize'} onPress={() => setMode('resize')} disabled={busy} />
      </View>
      <Text style={styles.text}>{mode === 'draw' ? '拖动截图重画所选框。' : mode === 'move'
        ? '点中任一已有框并拖动，或在空白处拖动所选框。' : '点中已有框并拖动调整右边、下边（左上角不动）；也可用下方像素微调。'}</Text>
      <View style={styles.canvasContainer} onLayout={event => setAvailableWidth(event.nativeEvent.layout.width)}>
        {image && canvasWidth > 0 ? <View style={{ width: canvasWidth, height: canvasHeight }} {...responder.panHandlers}>
          <Image key={`${imageVersion}:${image.uri}`} source={{ uri: image.uri }} resizeMode="contain" pointerEvents="none"
            accessibilityLabel={`完整游戏截图，${image.width} × ${image.height}`}
            onLoad={() => setImageReady(true)} onError={() => {
              setImageReady(false); Alert.alert('截图无法显示', '请重新从“文件”选择可读取的图片。');
            }} style={StyleSheet.absoluteFill} />
          <View pointerEvents="none" style={StyleSheet.absoluteFill}>
            {CALIBRATION_KEYS.map((key, index) => {
              const b = draft[key];
              if (!b) return null;
              return <View key={key} style={{ position: 'absolute', left: b.x * canvasWidth, top: b.y * canvasHeight,
                width: b.width * canvasWidth, height: b.height * canvasHeight,
                borderWidth: selected === key ? 2 : 1, borderColor: COLORS[index] }}>
                <Text numberOfLines={1} style={[styles.boxLabel, { color: COLORS[index] }]}>{CALIBRATION_NAMES[key]}</Text>
                {key.endsWith('Beads') && [0, 1, 2, 3].map(i => <View key={i} style={{ position: 'absolute',
                  left: `${(i + 0.5) * 25}%`, top: '50%', width: 4, height: 4,
                  marginLeft: -2, marginTop: -2, borderRadius: 2, backgroundColor: COLORS[index] }} />)}
              </View>;
            })}
          </View>
        </View> : <Text style={styles.placeholder}>先选择截图，再直接画框。没有识别几何，不显示自动框。</Text>}
      </View>
      {image && <Text style={styles.text}>{image.width} × {image.height} · 完整截图等比显示 · {imageReady ? '可编辑' : '图片未就绪'}</Text>}
      <Text accessibilityLiveRegion="polite" style={issues.length ? styles.warning : styles.text}>
        {issues.length ? issues.join('\n') : dirty ? '有未保存的改动。框的范围和大小检查通过。' : '尚无改动。未校准的框仍由识别自动决定。'}
      </Text>
      <Text accessibilityRole="header" style={styles.subtitle}>{CALIBRATION_NAMES[selected]} · 像素精确调整</Text>
      <View style={styles.row}>
        <Button label="步长 1 像素" selected={step === 1} onPress={() => setStep(1)} disabled={busy} />
        <Button label="步长 10 像素" selected={step === 10} onPress={() => setStep(10)} disabled={busy} />
      </View>
      {(['x', 'y', 'width', 'height'] as const).map((field, index) => {
        const label = ['左边 x', '上边 y', '宽度', '高度'][index];
        const axis = image ? field === 'x' || field === 'width' ? image.width : image.height : 0;
        return <View key={field} style={styles.numericRow}>
          <Text style={[styles.text, styles.numericLabel]}>{label}：{rect && axis ? `${(rect[field] * axis).toFixed(2)} px` : '—'}</Text>
          <Button label="−" accessibilityLabel={`${CALIBRATION_NAMES[selected]}${label}减少 ${step} 像素`}
            onPress={() => nudge(field, -1)} disabled={busy || !rect || !imageReady} />
          <Button label="＋" accessibilityLabel={`${CALIBRATION_NAMES[selected]}${label}增加 ${step} 像素`}
            onPress={() => nudge(field, 1)} disabled={busy || !rect || !imageReady} />
        </View>;
      })}
      <View style={styles.row}>
        <Button label="清除所选" onPress={() => reset(false)} disabled={busy || !rect} />
        <Button label="全部恢复自动" onPress={() => reset(true)} disabled={busy || !CALIBRATION_KEYS.some(key => draft[key])} />
      </View>
      <Text style={styles.text}>恢复自动、导入和编辑都只在本页暂存。“保存”提交已保存的框与本次改动，不写入任何未编辑的自动框；“取消”丢弃本次修改。导出的是编辑器当前方案，不会保存设置。</Text>
    </ScrollView>
  </SafeAreaView>;
}

const styles = StyleSheet.create({
  root: { flex: 1, backgroundColor: '#101827' },
  header: { flexDirection: 'row', alignItems: 'center', flexWrap: 'wrap', padding: 12, gap: 8 },
  title: { color: '#fff', fontSize: 22, fontWeight: '700', flexGrow: 1 },
  subtitle: { color: '#fff', fontSize: 17, fontWeight: '600' },
  content: { padding: 12, gap: 12, paddingBottom: 32 },
  row: { flexDirection: 'row', flexWrap: 'wrap', gap: 8 },
  button: { minWidth: 44, minHeight: 44, justifyContent: 'center', alignItems: 'center',
    paddingHorizontal: 12, paddingVertical: 10, borderRadius: 8, borderWidth: 1, borderColor: '#64748b', backgroundColor: '#243247' },
  selected: { borderColor: '#67e8f9', backgroundColor: '#164e63' },
  disabled: { opacity: 0.45 },
  buttonText: { color: '#fff', fontSize: 15, textAlign: 'center' },
  text: { color: '#dbe4ef', fontSize: 14, lineHeight: 21 },
  warning: { color: '#fcd34d', fontSize: 14, lineHeight: 22 },
  notice: { padding: 12, gap: 10, backgroundColor: '#29354a', borderRadius: 8 },
  canvasContainer: { alignItems: 'center', justifyContent: 'center', backgroundColor: '#000' },
  placeholder: { color: '#cbd5e1', padding: 28, minHeight: 140, textAlign: 'center' },
  boxLabel: { position: 'absolute', top: -19, left: 0, fontSize: 11, backgroundColor: '#000b' },
  numericRow: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', gap: 8 },
  numericLabel: { flexGrow: 1, minWidth: 140, fontVariant: ['tabular-nums'] },
});
