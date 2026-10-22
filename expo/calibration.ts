/** Shared Windows/Android scheme format; all coordinates refer to the WHOLE screenshot. */
export type CalibrationRect = { x: number; y: number; width: number; height: number };
export type Calibration = {
  profile: 'camp' | 'duel';
  leftBeads?: CalibrationRect;
  rightBeads?: CalibrationRect;
  leftAvatar?: CalibrationRect;
  rightAvatar?: CalibrationRect;
};
export type CalibrationScheme = { calibration: Calibration; frameWidth: number; frameHeight: number };
export const CALIBRATION_KEYS = ['leftBeads', 'rightBeads', 'leftAvatar', 'rightAvatar'] as const;
export type CalibrationKey = typeof CALIBRATION_KEYS[number];
export const CALIBRATION_NAMES: Record<CalibrationKey, string> = {
  leftBeads: '左侧豆', rightBeads: '右侧豆', leftAvatar: '左侧头像', rightAvatar: '右侧头像',
};
const KIND = 'naruto-timer-calibration';
const MAX_BYTES = 64 * 1024;
const EDGE_SLACK = 1e-6;
const object = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const finite = (value: unknown): value is number => typeof value === 'number' && Number.isFinite(value);

/** Same double arithmetic / inclusive size boundaries as Android CalibrationLimits. */
function checkedRect(key: CalibrationKey, value: unknown): CalibrationRect {
  const name = CALIBRATION_NAMES[key];
  if (!object(value)) throw new Error(`${name}不是 {x, y, width, height}。`);
  const { x, y, width, height } = value;
  if (!finite(x) || !finite(y) || !finite(width) || !finite(height)) {
    throw new Error(`${name}的 x、y、width、height 必须是有限数字。`);
  }
  if (width <= 0 || height <= 0) throw new Error(`${name}的宽高必须大于 0。`);
  if (x < -EDGE_SLACK || y < -EDGE_SLACK || x + width > 1 + EDGE_SLACK || y + height > 1 + EDGE_SLACK) {
    throw new Error(`${name}超出画面：坐标是整帧的 0–1 比例。`);
  }
  // Accommodate cross-platform edge rounding, but never relax the size limits.
  const inFrame = x >= 0 && y >= 0 && x + width <= 1 && y + height <= 1;
  const rx = inFrame ? x : Math.max(0, Math.min(1, x));
  const ry = inFrame ? y : Math.max(0, Math.min(1, y));
  const w = inFrame ? width : Math.min(width, 1 - rx);
  const h = inFrame ? height : Math.min(height, 1 - ry);
  if (w <= 0 || h <= 0) throw new Error(`${name}的宽高必须大于 0。`);
  const avatar = key.endsWith('Avatar');
  const u = avatar ? w * 960 / 68.5 : w * 16;
  const v = avatar ? h * 540 / 68.5 : h * 36;
  const k = Math.max(u, v);
  if (!(k <= 2)) throw new Error(`${name}太大：是自动框的 ${k.toFixed(2)} 倍（只用 0.5–2 倍）。`);
  if (!(k >= 0.5)) throw new Error(`${name}太小：是自动框的 ${k.toFixed(2)} 倍（只用 0.5–2 倍）。`);
  if (!(Math.min(u, v) >= k * 0.25)) throw new Error(`${name}太扁或太窄：按自动框折算的宽高相差不能超过 4 倍。`);
  return { x: rx, y: ry, width: w, height: h };
}

export function calibrationRectIssue(key: CalibrationKey, value: unknown): string | null {
  try { checkedRect(key, value); return null; }
  catch (error) { return error instanceof Error ? error.message : '无效的校准框。'; }
}

/** Strict validation for save/export; no unknown fields (including runtime geometry) survive. */
export function validateCalibration(value: unknown): Calibration {
  if (!object(value) || (value.profile !== 'camp' && value.profile !== 'duel')) {
    throw new Error('profile 必须是 camp（训练场）或 duel（决斗场）。');
  }
  const result: Calibration = { profile: value.profile };
  for (const key of CALIBRATION_KEYS) {
    if (value[key] != null) result[key] = checkedRect(key, value[key]);
  }
  return result;
}

/** Persisted settings are untrusted. Invalid boxes independently fall back to automatic. */
export function normalizeCalibration(value: unknown): Calibration {
  if (!object(value) || (value.profile !== 'camp' && value.profile !== 'duel')) return { profile: 'duel' };
  const result: Calibration = { profile: value.profile };
  for (const key of CALIBRATION_KEYS) {
    if (value[key] == null) continue;
    try { result[key] = checkedRect(key, value[key]); } catch { /* This region remains automatic. */ }
  }
  return result;
}

// Frame dimensions are informational, just like CalibrationScheme.frameSide on Android.
function frameSide(value: unknown): number {
  return finite(value) && Number.isInteger(value) && value >= 1 && value <= 100_000 ? value : 0;
}

export function parseCalibration(text: string): CalibrationScheme {
  if (typeof text !== 'string' || text.length > MAX_BYTES) throw new Error('校准方案不能超过 64 KiB。');
  // RN does not require TextEncoder/Buffer. Count UTF-8 before parsing (including BOM).
  let bytes = 0;
  for (const char of text) {
    const code = char.codePointAt(0)!;
    bytes += code <= 0x7f ? 1 : code <= 0x7ff ? 2 : code <= 0xffff ? 3 : 4;
    if (bytes > MAX_BYTES) throw new Error('校准方案不能超过 64 KiB。');
  }
  let root: unknown;
  try { root = JSON.parse(text.replace(/^\uFEFF/, '')); }
  catch { throw new Error('不是校准方案文件（不是有效的 JSON）。'); }
  if (!object(root) || root.kind !== KIND) throw new Error(`不是校准方案文件（kind 必须是 ${KIND}）。`);
  if (root.version !== 1) throw new Error('校准方案 version 必须是数字 1，请检查文件或更新计时器。');
  return {
    calibration: validateCalibration(root),
    frameWidth: frameSide(root.frameWidth),
    frameHeight: frameSide(root.frameHeight),
  };
}

export function encodeCalibration(calibration: Calibration, width: number, height: number): string {
  return JSON.stringify({ kind: KIND, version: 1, frameWidth: frameSide(width), frameHeight: frameSide(height),
    ...validateCalibration(calibration) }, null, 2) + '\n';
}

export function calibrationAspectDifference(scheme: CalibrationScheme, width: number, height: number): number | null {
  if (scheme.frameWidth <= 0 || scheme.frameHeight <= 0 || width <= 0 || height <= 0) return null;
  const current = width / height;
  return Math.abs(scheme.frameWidth / scheme.frameHeight - current) / current;
}
