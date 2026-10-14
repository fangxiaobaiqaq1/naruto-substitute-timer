package android.content;

import java.util.HashMap;
import java.util.Map;

/** Isolated in-memory Android preferences; no tracker/settings logic is replaced. */
public class Context {
    public static final int MODE_PRIVATE = 0;
    private final Map<String, SharedPreferences> files = new HashMap<>();
    public Context getApplicationContext() { return this; }
    public synchronized SharedPreferences getSharedPreferences(String name, int mode) {
        return files.computeIfAbsent(name, ignored -> new MemoryPreferences());
    }

    private static final class MemoryPreferences implements SharedPreferences {
        private final Map<String, Object> values = new HashMap<>();
        public synchronized String getString(String key, String fallback) {
            return (String) values.getOrDefault(key, fallback);
        }
        public synchronized boolean getBoolean(String key, boolean fallback) {
            return (Boolean) values.getOrDefault(key, fallback);
        }
        public synchronized float getFloat(String key, float fallback) {
            return (Float) values.getOrDefault(key, fallback);
        }
        public synchronized int getInt(String key, int fallback) {
            return (Integer) values.getOrDefault(key, fallback);
        }
        public Editor edit() {
            return new Editor() {
                private final Map<String, Object> pending = new HashMap<>();
                public Editor putString(String k, String v) { pending.put(k, v); return this; }
                public Editor putBoolean(String k, boolean v) { pending.put(k, v); return this; }
                public Editor putFloat(String k, float v) { pending.put(k, v); return this; }
                public Editor putInt(String k, int v) { pending.put(k, v); return this; }
                public void apply() {
                    synchronized (MemoryPreferences.this) { values.putAll(pending); }
                }
            };
        }
    }
}
