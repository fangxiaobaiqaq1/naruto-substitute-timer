import { useEffect, useState } from 'react';
import { AppState } from 'react-native';

/** Wake only while a deadline is pending and the app is active. */
export function useCountdownNow(latestDeadline: number): number {
  const [, setNow] = useState(Date.now);

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const stop = () => {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
    };
    const refresh = (state = AppState.currentState) => {
      stop();
      if (state !== 'active') return;
      const time = Date.now();
      setNow(time);
      if (time < latestDeadline) {
        timer = setTimeout(refresh, Math.min(100, latestDeadline - time));
      }
    };

    const subscription = AppState.addEventListener('change', refresh);
    refresh();
    return () => {
      stop();
      subscription.remove();
    };
  }, [latestDeadline]);

  // Starting after idle must render wall time, not the timestamp of the last tick.
  return Date.now();
}
