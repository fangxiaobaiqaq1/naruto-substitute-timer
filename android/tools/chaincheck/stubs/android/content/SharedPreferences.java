package android.content;

/** Only the Android platform API surface consumed by production Settings.kt. */
public interface SharedPreferences {
    String getString(String key, String fallback);
    boolean getBoolean(String key, boolean fallback);
    float getFloat(String key, float fallback);
    int getInt(String key, int fallback);
    Editor edit();

    interface Editor {
        Editor putString(String key, String value);
        Editor putBoolean(String key, boolean value);
        Editor putFloat(String key, float value);
        Editor putInt(String key, int value);
        void apply();
    }
}
