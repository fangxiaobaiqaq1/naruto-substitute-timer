import { requireNativeView as requireNativeViewManager, requireOptionalNativeModule, type EventSubscription } from 'expo';
import { AppState, Platform, type ViewProps } from 'react-native';
import type { ComponentType } from 'react';
import { normalizeCalibration, type Calibration } from './calibration';

export type CaptureSettings = {
  fontScale: number;
  roiScale: number;
  roiOffsetX: number;
  roiOffsetY: number;
  backgroundFPS: number;
  calibration: Calibration;
};

export type CaptureState = {
  available: boolean;
  prepared: boolean;
  broadcastActive: boolean;
  pipSupported: boolean;
  pipActive: boolean;
  pipPossible: boolean;
  pipPending: boolean;
  connected: boolean;
  message: string;
  leftDeadlineMs: number;
  rightDeadlineMs: number;
  leftAlternateDeadlineMs: number;
  rightAlternateDeadlineMs: number;
  leftCount: number;
  rightCount: number;
  match: boolean;
  frames: number;
  lastFrameAtMs: number;
  leftNinja: string;
  rightNinja: string;
  leftSubstitutions: number;
  rightSubstitutions: number;
  scene: string;
  latencyMs: number;
  deliveryLatencyMs: number;
  frameWidth: number;
  frameHeight: number;
  recognitionError: string;
};

export type CalibrationImage = { uri: string; width: number; height: number };

type NarutoCaptureModule = {
  getState: () => unknown | Promise<unknown>;
  prepare: () => Promise<unknown>;
  startPictureInPicture: () => Promise<unknown>;
  stop: () => Promise<unknown>;
  setSettings: (settings: CaptureSettings) => Promise<unknown>;
  pickCalibrationImage?: () => Promise<unknown>;
  importCalibration?: () => Promise<string>;
  shareDocument?: (name: string, text: string) => Promise<void>;
  addListener?: (event: 'onState', listener: (state: unknown) => void) => EventSubscription;
};

export const DEFAULT_CAPTURE_SETTINGS: CaptureSettings = {
  fontScale: 0.75,
  roiScale: 1,
  roiOffsetX: 0,
  roiOffsetY: 0,
  backgroundFPS: 8,
  calibration: { profile: 'duel' },
};

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

function clamp(value: unknown, minimum: number, maximum: number, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.min(maximum, Math.max(minimum, value))
    : fallback;
}

/** Only these validated fields cross the native boundary; UI opacity is not a PiP setting. */
export function normalizeCaptureSettings(value: unknown): CaptureSettings {
  const source = record(value);
  // iOS always shows both sides: no side selection, account names or opponent override.
  return {
    calibration: normalizeCalibration(source.calibration),
    fontScale: clamp(source.fontScale, 0.55, 1.2, DEFAULT_CAPTURE_SETTINGS.fontScale),
    roiScale: clamp(source.roiScale, 0.7, 1.4, 1),
    roiOffsetX: clamp(source.roiOffsetX, -0.15, 0.15, 0),
    roiOffsetY: clamp(source.roiOffsetY, -0.15, 0.15, 0),
    backgroundFPS: Math.round(clamp(source.backgroundFPS, 1, 10, 8)),
  };
}

function loadNativeModule(): NarutoCaptureModule | null {
  if (Platform.OS !== 'ios') return null;
  try {
    return requireOptionalNativeModule<NarutoCaptureModule>('NarutoCapture');
  } catch {
    return null;
  }
}

const nativeModule = loadNativeModule();
export const hasNativeCapture = nativeModule !== null &&
  (['getState', 'prepare', 'startPictureInPicture', 'stop', 'setSettings'] as const)
    .every((method) => typeof nativeModule[method] === 'function');

function loadNativeView(): ComponentType<ViewProps> | null {
  if (!hasNativeCapture) return null;
  try {
    // Expo SDK 58 exposes requireNativeViewManager as requireNativeView from `expo`.
    return requireNativeViewManager<ViewProps>('NarutoCapture');
  } catch {
    return null;
  }
}

export const NativeCaptureView = loadNativeView();
const unavailableReason = Platform.OS !== 'ios'
  ? '自动采集与画中画仅支持 iOS 原生安装包。'
  : !hasNativeCapture
    ? '此构建未包含完整的 NarutoCapture 原生模块。请安装包含广播扩展的 iOS 安装包；Expo Go 不支持自动采集。'
    : !NativeCaptureView
      ? '系统录屏控件加载失败，请使用包含 NarutoCapture 原生视图的安装包。'
      : '';

export const INITIAL_CAPTURE_STATE: CaptureState = {
  available: false,
  prepared: false,
  broadcastActive: false,
  pipSupported: false,
  pipActive: false,
  pipPossible: false,
  pipPending: false,
  connected: false,
  message: unavailableReason || '正在读取原生采集状态…',
  leftDeadlineMs: 0,
  rightDeadlineMs: 0,
  leftAlternateDeadlineMs: 0,
  rightAlternateDeadlineMs: 0,
  leftCount: -1,
  rightCount: -1,
  match: false,
  frames: 0,
  lastFrameAtMs: 0,
  leftNinja: '',
  rightNinja: '',
  leftSubstitutions: 0,
  rightSubstitutions: 0,
  scene: '',
  latencyMs: 0,
  deliveryLatencyMs: 0,
  frameWidth: 0,
  frameHeight: 0,
  recognitionError: '',
};

function timestamp(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= Number.MAX_SAFE_INTEGER ? value : 0;
}

function beanCount(value: unknown): number {
  return typeof value === 'number' && Number.isInteger(value) && value >= -1 && value <= 6 ? value : -1;
}

export function normalizeCaptureState(value: unknown): CaptureState {
  if (unavailableReason) return { ...INITIAL_CAPTURE_STATE };
  const source = record(value);
  const available = source.available === true;
  const pipSupported = available && source.pipSupported === true;
  return {
    available,
    prepared: available && source.prepared === true,
    broadcastActive: available && source.broadcastActive === true,
    pipSupported,
    pipActive: pipSupported && source.pipActive === true,
    pipPossible: pipSupported && source.pipPossible === true,
    pipPending: pipSupported && source.pipPending === true,
    connected: available && source.connected === true,
    message: typeof source.message === 'string' ? source.message.slice(0, 500) : '原生未返回有效状态说明。',
    leftDeadlineMs: timestamp(source.leftDeadlineMs),
    rightDeadlineMs: timestamp(source.rightDeadlineMs),
    leftAlternateDeadlineMs: timestamp(source.leftAlternateDeadlineMs),
    rightAlternateDeadlineMs: timestamp(source.rightAlternateDeadlineMs),
    leftCount: beanCount(source.leftCount),
    rightCount: beanCount(source.rightCount),
    match: available && source.match === true,
    frames: Number.isSafeInteger(source.frames) ? timestamp(source.frames) : 0,
    lastFrameAtMs: timestamp(source.lastFrameAtMs),
    leftNinja: typeof source.leftNinja === 'string' ? source.leftNinja.slice(0, 80) : '',
    rightNinja: typeof source.rightNinja === 'string' ? source.rightNinja.slice(0, 80) : '',
    leftSubstitutions: Math.floor(clamp(source.leftSubstitutions, 0, 100000, 0)),
    rightSubstitutions: Math.floor(clamp(source.rightSubstitutions, 0, 100000, 0)),
    scene: typeof source.scene === 'string' ? source.scene.slice(0, 40) : '',
    latencyMs: clamp(source.latencyMs, 0, 60000, 0),
    deliveryLatencyMs: clamp(source.deliveryLatencyMs, 0, 60000, 0),
    frameWidth: Math.floor(clamp(source.frameWidth, 0, 16384, 0)),
    frameHeight: Math.floor(clamp(source.frameHeight, 0, 16384, 0)),
    recognitionError: typeof source.recognitionError === 'string' ? source.recognitionError.slice(0, 500) : '',
  };
}

export async function getCaptureState(): Promise<CaptureState> {
  if (unavailableReason) return { ...INITIAL_CAPTURE_STATE };
  try {
    // await accepts both Expo Function and AsyncFunction implementations.
    return normalizeCaptureState(await nativeModule!.getState());
  } catch {
    return { ...INITIAL_CAPTURE_STATE, message: '原生采集状态读取失败，当前无法确认采集或画中画是否运行。' };
  }
}

function requireCapture(): NarutoCaptureModule {
  if (!hasNativeCapture) throw new Error(unavailableReason);
  return nativeModule!;
}

export async function prepareCapture(): Promise<CaptureState> {
  return normalizeCaptureState(await requireCapture().prepare());
}

export async function startCapturePictureInPicture(): Promise<CaptureState> {
  await requireCapture().startPictureInPicture();
  return getCaptureState();
}

export async function stopCapture(): Promise<CaptureState> {
  await requireCapture().stop();
  return getCaptureState();
}

export async function setCaptureSettings(settings: CaptureSettings): Promise<void> {
  await requireCapture().setSettings(normalizeCaptureSettings(settings));
}

export async function pickCalibrationImage(): Promise<CalibrationImage> {
  const module = requireCapture();
  if (!module.pickCalibrationImage) throw new Error('当前原生包未包含截图文件选择器。');
  const image = record(await module.pickCalibrationImage());
  if (typeof image.uri !== 'string' || !image.uri.startsWith('file://') ||
      typeof image.width !== 'number' || typeof image.height !== 'number' ||
      !Number.isFinite(image.width) || !Number.isFinite(image.height) ||
      image.width <= 0 || image.height <= 0 || image.width > 16384 || image.height > 16384) {
    throw new Error('截图文件尺寸或路径无效。');
  }
  return image as CalibrationImage;
}

export async function importCalibrationDocument(): Promise<string> {
  const module = requireCapture();
  if (!module.importCalibration) throw new Error('当前原生包未包含方案文件选择器。');
  return module.importCalibration();
}

export async function shareDocument(name: string, text: string): Promise<void> {
  const module = requireCapture();
  if (!module.shareDocument) throw new Error('当前原生包未包含文件分享功能。');
  return module.shareDocument(name, text);
}

/** Events first; a foreground watchdog also supports builds without working events. */
export function subscribeCaptureState(listener: (state: CaptureState) => void): () => void {
  if (unavailableReason) {
    listener({ ...INITIAL_CAPTURE_STATE });
    return () => undefined;
  }
  let disposed = false;
  let pending = false;
  let revision = 0;
  let lastEventAt = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let subscription: EventSubscription | undefined;
  const refresh = async () => {
    if (disposed || pending || AppState.currentState !== 'active') return;
    pending = true;
    const startedAtRevision = revision;
    try {
      const state = await getCaptureState();
      if (!disposed && AppState.currentState === 'active' && revision === startedAtRevision) listener(state);
    } finally {
      pending = false;
    }
  };
  try {
    subscription = nativeModule!.addListener?.('onState', (value) => {
      if (disposed) return;
      revision += 1;
      lastEventAt = Date.now();
      if (AppState.currentState === 'active') listener(normalizeCaptureState(value));
    });
  } catch {
    // Older native builds can still supply truthful state through getState().
  }
  const poll = () => {
    if (disposed || AppState.currentState !== 'active') return;
    if (!subscription || Date.now() - lastEventAt >= 1500) void refresh();
    timer = setTimeout(poll, 1000);
  };
  const onAppState = () => {
    revision += 1;
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    if (AppState.currentState === 'active') {
      void refresh();
      timer = setTimeout(poll, 1000);
    }
  };
  const appSubscription = AppState.addEventListener('change', onAppState);
  onAppState();
  return () => {
    disposed = true;
    if (timer !== undefined) clearTimeout(timer);
    subscription?.remove();
    appSubscription.remove();
  };
}
