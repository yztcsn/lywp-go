package com.lywp.android;

import android.content.Context;
import android.content.SharedPreferences;

import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;

/** Stores the server address, session cookie and UI preferences. */
final class Config {
    private static final String PREFS = "lywp_config";
    private static final String KEY_SERVER = "server";
    private static final String KEY_COOKIE = "cookie";
    private static final String KEY_HISTORY = "history";
    private static final String KEY_THEME = "theme";
    private static final String KEY_REMEMBER = "remember";
    private static final String KEY_PASSWORD = "password";
    private static final String KEY_LOGIN_EXPIRES = "login_expires_at";
    static final long REMEMBER_DURATION_MS = 30L * 24 * 60 * 60 * 1000;

    private final SharedPreferences prefs;

    Config(Context ctx) {
        prefs = ctx.getApplicationContext().getSharedPreferences(PREFS, Context.MODE_PRIVATE);
    }

    String getServer() {
        return prefs.getString(KEY_SERVER, "");
    }

    void setServer(String server) {
        prefs.edit().putString(KEY_SERVER, normalize(server)).apply();
        addHistory(server);
    }

    String getCookie() {
        return prefs.getString(KEY_COOKIE, "");
    }

    void setCookie(String cookie) {
        prefs.edit().putString(KEY_COOKIE, cookie == null ? "" : cookie).apply();
    }

    /** Login details are remembered by default. */
    boolean isRememberEnabled() {
        return prefs.getBoolean(KEY_REMEMBER, true);
    }

    String getPassword() {
        return prefs.getString(KEY_PASSWORD, "");
    }

    long getLoginExpiresAt() {
        return prefs.getLong(KEY_LOGIN_EXPIRES, 0L);
    }

    boolean isLoginExpired() {
        long expires = getLoginExpiresAt();
        return expires > 0 && System.currentTimeMillis() >= expires;
    }

    /** True only when a still-valid remembered login can be resumed. */
    boolean canResumeSession() {
        return isRememberEnabled() && !isLoginExpired() && !getCookie().isEmpty()
                && !getPassword().isEmpty() && !getServer().isEmpty();
    }

    /** Persist login details for 30 days, or keep only the current cookie. */
    void saveLogin(String server, String password, String cookie, boolean remember) {
        SharedPreferences.Editor editor = prefs.edit();
        editor.putString(KEY_SERVER, normalize(server));
        editor.putString(KEY_COOKIE, cookie == null ? "" : cookie);
        editor.putBoolean(KEY_REMEMBER, remember);
        if (remember) {
            editor.putString(KEY_PASSWORD, password == null ? "" : password);
            editor.putLong(KEY_LOGIN_EXPIRES, System.currentTimeMillis() + REMEMBER_DURATION_MS);
        } else {
            editor.putString(KEY_PASSWORD, "");
            editor.putLong(KEY_LOGIN_EXPIRES, 0L);
        }
        editor.apply();
        addHistory(server);
    }

    /** Drop all credentials; keep the user's remember preference. */
    void clearSession() {
        prefs.edit()
                .putString(KEY_COOKIE, "")
                .putString(KEY_PASSWORD, "")
                .putLong(KEY_LOGIN_EXPIRES, 0L)
                .apply();
    }

    String getTheme() {
        return prefs.getString(KEY_THEME, "ocean");
    }

    void setTheme(String theme) {
        prefs.edit().putString(KEY_THEME, theme).apply();
    }

    List<String> getHistory() {
        Set<String> set = prefs.getStringSet(KEY_HISTORY, new LinkedHashSet<>());
        return new ArrayList<>(set);
    }

    private void addHistory(String raw) {
        String server = normalize(raw);
        if (server.isEmpty()) return;
        Set<String> set = new LinkedHashSet<>(prefs.getStringSet(KEY_HISTORY, new LinkedHashSet<>()));
        set.remove(server);
        set.add(server);
        List<String> list = new ArrayList<>(set);
        while (list.size() > 6) list.remove(0);
        prefs.edit().putStringSet(KEY_HISTORY, new LinkedHashSet<>(list)).apply();
    }

    /** Strip trailing slashes so URL joining is predictable. */
    static String normalize(String server) {
        if (server == null) return "";
        String s = server.trim();
        while (s.endsWith("/")) s = s.substring(0, s.length() - 1);
        return s;
    }
}
