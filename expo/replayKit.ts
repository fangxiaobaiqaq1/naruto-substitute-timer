import { NativeModules, Platform } from 'react-native';

export type ReplayKitStatus = 'available' | 'placeholder' | 'unavailable';

export type ReplayKitState = {
  status: ReplayKitStatus;
  message: string;
};

type ReplayKitNativeModule = {
  getStatus?: () => ReplayKitState;
  prepareBroadcast?: () => ReplayKitState | Promise<ReplayKitState>;
};

const nativeModule: ReplayKitNativeModule | undefined =
  Platform.OS === 'ios' ? NativeModules.NarutoReplayKit : undefined;

const PLACEHOLDER_STATE: ReplayKitState = {
  status: 'placeholder',
  message:
    'ReplayKit Broadcast Extension 尚未接入；当前构建只能运行前台演示计时。',
};

export function getReplayKitState(): ReplayKitState {
  if (!nativeModule?.getStatus) {
    return Platform.OS === 'ios'
      ? PLACEHOLDER_STATE
      : { status: 'unavailable', message: 'ReplayKit 仅支持 iOS。' };
  }

  try {
    return nativeModule.getStatus();
  } catch {
    return {
      status: 'unavailable',
      message: 'ReplayKit 原生桥接状态读取失败。',
    };
  }
}

export async function prepareReplayKitBroadcast(): Promise<ReplayKitState> {
  if (!nativeModule?.prepareBroadcast) return getReplayKitState();
  try {
    return await nativeModule.prepareBroadcast();
  } catch {
    return {
      status: 'unavailable',
      message: 'ReplayKit 原生桥接调用失败，请检查开发构建中的 Broadcast Extension。',
    };
  }
}
