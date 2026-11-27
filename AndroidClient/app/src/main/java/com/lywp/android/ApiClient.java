package com.lywp.android;

import java.io.BufferedReader;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

/** Minimal HTTP helper used for the native login / theme calls. */
final class ApiClient {

    static final class Result {
        final int code;
        final String body;
        final String cookie;

        Result(int code, String body, String cookie) {
            this.code = code;
            this.body = body;
            this.cookie = cookie;
        }

        boolean ok() {
            return code >= 200 && code < 300;
        }
    }

    /** Login with the password, returning the session cookie on success. */
    static Result login(String server, String password) throws IOException {
        String url = server + "/auth.php?action=login";
        String payload = "{\"password\":" + quote(password) + "}";
        return post(url, payload, null, 10000);
    }

    /** Save a site-wide skin through the compatible API. */
    static Result setTheme(String server, String cookie, String theme) throws IOException {
        String url = server + "/api.php?action=theme";
        String payload = "{\"theme\":" + quote(theme) + "}";
        return post(url, payload, cookie, 10000);
    }

    static Result check(String server, String cookie) throws IOException {
        String url = server + "/auth.php?action=check";
        return get(url, cookie, 8000);
    }

    static Result get(String urlStr, String cookie, int timeoutMs) throws IOException {
        HttpURLConnection conn = (HttpURLConnection) new URL(urlStr).openConnection();
        conn.setRequestMethod("GET");
        conn.setConnectTimeout(timeoutMs);
        conn.setReadTimeout(timeoutMs);
        conn.setInstanceFollowRedirects(true);
        if (cookie != null && !cookie.isEmpty()) conn.setRequestProperty("Cookie", cookie);
        try {
            int code = conn.getResponseCode();
            String body = read(conn.getInputStream());
            return new Result(code, body, headerCookie(conn));
        } finally {
            conn.disconnect();
        }
    }

    private static Result post(String urlStr, String payload, String cookie, int timeoutMs) throws IOException {
        HttpURLConnection conn = (HttpURLConnection) new URL(urlStr).openConnection();
        conn.setRequestMethod("POST");
        conn.setConnectTimeout(timeoutMs);
        conn.setReadTimeout(timeoutMs);
        conn.setDoOutput(true);
        conn.setRequestProperty("Content-Type", "application/json; charset=utf-8");
        if (cookie != null && !cookie.isEmpty()) conn.setRequestProperty("Cookie", cookie);
        try {
            DataOutputStream out = new DataOutputStream(conn.getOutputStream());
            out.write(payload.getBytes(StandardCharsets.UTF_8));
            out.flush();
            out.close();
            int code = conn.getResponseCode();
            InputStream stream = code < 400 ? conn.getInputStream() : conn.getErrorStream();
            String body = stream == null ? "" : read(stream);
            String setCookie = headerCookie(conn);
            return new Result(code, body, cookieFromSet(setCookie, cookie));
        } finally {
            conn.disconnect();
        }
    }

    private static String headerCookie(HttpURLConnection conn) {
        return conn.getHeaderField("Set-Cookie");
    }

    /** Keep only the lywp_session name=value pair, dropping attributes. */
    static String cookieFromSet(String setCookie, String existing) {
        if (setCookie == null || setCookie.isEmpty()) return existing == null ? "" : existing;
        String token = setCookie.split(";", 2)[0].trim();
        if (token.startsWith("lywp_session=")) {
            if (token.endsWith("lywp_session=")) return ""; // logout
            return token;
        }
        return existing == null ? "" : existing;
    }

    private static String read(InputStream in) throws IOException {
        BufferedReader reader = new BufferedReader(new InputStreamReader(in, StandardCharsets.UTF_8));
        StringBuilder sb = new StringBuilder();
        String line;
        while ((line = reader.readLine()) != null) sb.append(line).append('\n');
        reader.close();
        return sb.toString().trim();
    }

    private static String quote(String value) {
        if (value == null) value = "";
        StringBuilder sb = new StringBuilder("\"");
        for (int i = 0; i < value.length(); i++) {
            char c = value.charAt(i);
            switch (c) {
                case '"': sb.append("\\\""); break;
                case '\\': sb.append("\\\\"); break;
                case '\n': sb.append("\\n"); break;
                case '\r': sb.append("\\r"); break;
                case '\t': sb.append("\\t"); break;
                default:
                    if (c < 0x20) sb.append(String.format("\\u%04x", (int) c));
                    else sb.append(c);
            }
        }
        return sb.append('"').toString();
    }
}
