export const APP_VERSION = '0.3.0';
export const IOS_BUILD = 4;
export const IOS_BUILDS_URL = 'https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/actions/workflows/ios-unsigned.yml';
export const RELEASES_URL = 'https://github.com/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest';
export type UpdateSource = 'gitee' | 'github';
const LATEST_RELEASE_API = {
  github: 'https://api.github.com/repos/fangxiaobaiqaq1/naruto-substitute-timer/releases/latest',
  gitee: 'https://gitee.com/api/v5/repos/xiaobaiqaq/naruto-substitute-timer/releases/latest',
};

export function isNewerVersion(tag: string, current = APP_VERSION): boolean {
  const next = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(tag);
  const old = /^(\d+)\.(\d+)\.(\d+)$/.exec(current);
  if (!next || !old) return false;
  for (let index = 1; index <= 3; index += 1) {
    const difference = Number(next[index]) - Number(old[index]);
    if (difference) return difference > 0;
  }
  return false;
}

export async function checkForUpdate(source: UpdateSource = 'gitee'): Promise<{ version: string; newer: boolean; notes: string }> {
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(), 15000);
  try {
    const response = await fetch(LATEST_RELEASE_API[source], { signal: controller.signal, headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`更新服务器返回 HTTP ${response.status}，请稍后重试。`);
    const release = await response.json();
    if (typeof release?.tag_name !== 'string' || !/^v?\d+\.\d+\.\d+$/.test(release.tag_name)) {
      throw new Error('服务器未返回正式版版本号。');
    }
    return { version: release.tag_name, newer: isNewerVersion(release.tag_name), notes: typeof release.body === 'string' ? release.body.slice(0, 12000) : '' };
  } finally {
    clearTimeout(timeout);
  }
}

export function isDualNinja(name: string): boolean {
  const normalized = name.replace(/[\s\[\]【】()（）·・]/g, '');
  return normalized === '照美冥五代目水影' || normalized === '半藏秽土转生';
}
