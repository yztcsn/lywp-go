package com.lywp.android;

import android.app.Activity;
import android.app.AlertDialog;
import android.app.DownloadManager;
import android.content.Context;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.database.Cursor;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Handler;
import android.os.Looper;
import android.provider.OpenableColumns;
import android.util.Log;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.DownloadListener;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.ArrayAdapter;
import android.widget.GridView;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;

import java.io.File;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

import androidx.core.app.ActivityCompat;
import androidx.core.content.ContextCompat;
import androidx.core.content.FileProvider;
import androidx.documentfile.provider.DocumentFile;

public class MainActivity extends Activity {
    private static final String TAG = "LYWP";
    private static final int REQ_FILE = 1001;
    private static final int REQ_PERM = 1002;

    private WebView web;
    private Config config;
    private TextView textLoading;
    private ProgressBar progress;
    private ValueCallback<Uri[]> filePathCallback;
    private boolean choosingDirectory;
    private final ExecutorService executor = Executors.newSingleThreadExecutor();
    private final Handler main = new Handler(Looper.getMainLooper());
    private String server;
    private long lastBack = 0;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        config = new Config(this);
        server = config.getServer();
        boolean resumed = config.canResumeSession() || ConnectActivity.freshLogin;
        ConnectActivity.freshLogin = false;
        if (server.isEmpty() || !resumed) {
            config.clearSession();
            startActivity(new Intent(this, ConnectActivity.class));
            finish();
            return;
        }

        web = findViewById(R.id.webView);
        textLoading = findViewById(R.id.textLoading);
        progress = findViewById(R.id.progress);

        setupWebView();
        syncCookieAndLoad();
        requestStoragePermission();
    }

    private void setupWebView() {
        WebSettings ws = web.getSettings();
        ws.setJavaScriptEnabled(true);
        ws.setDomStorageEnabled(true);
        ws.setDatabaseEnabled(true);
        ws.setAllowFileAccess(true);
        ws.setAllowContentAccess(true);
        ws.setLoadWithOverviewMode(true);
        ws.setUseWideViewPort(true);
        ws.setBuiltInZoomControls(false);
        ws.setDisplayZoomControls(false);
        ws.setMediaPlaybackRequiresUserGesture(false);
        ws.setCacheMode(WebSettings.LOAD_DEFAULT);
        ws.setUserAgentString(ws.getUserAgentString() + " LYWPAndroid/" +
                (Device.isTablet(this) ? "Tablet" : "Phone"));

        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri uri = request.getUrl();
                String url = uri.toString();
                // Keep navigation inside the app for the netdisk host.
                if (url.startsWith(server) || url.startsWith("about:") || url.startsWith("data:")) {
                    return false;
                }
                // Share links / external resources open in the browser.
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, uri));
                } catch (Exception ignored) {}
                return true;
            }

            @Override
            public android.webkit.WebResourceResponse shouldInterceptRequest(
                    WebView view, WebResourceRequest request) {
                Uri uri = request.getUrl();
                if ("/auth.php".equals(uri.getPath())
                        && "logout".equals(uri.getQueryParameter("action"))) {
                    main.post(MainActivity.this::clearSessionAndGoLogin);
                }
                return super.shouldInterceptRequest(view, request);
            }

            @Override
            public void onPageStarted(WebView view, String url, Bitmap favicon) {
                progress.setVisibility(View.VISIBLE);
                textLoading.setVisibility(url.startsWith(server) ? View.GONE : View.VISIBLE);
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                progress.setVisibility(View.GONE);
                textLoading.setVisibility(View.GONE);
                applyInputFix(view);
                // The server injects data-theme into <html>, so the correct
                // skin is already applied on first paint. Read it back so the
                // native picker highlights the server-wide selection.
                view.evaluateJavascript(
                        "document.documentElement.getAttribute('data-theme')||''",
                        value -> {
                            if (value != null) {
                                String theme = value.replace("\"", "").trim();
                                if (!theme.isEmpty() && !theme.equals(config.getTheme())) {
                                    config.setTheme(theme);
                                }
                            }
                        });
            }

            @Override
            public void onReceivedError(WebView view, int errorCode, String description, String failingUrl) {
                textLoading.setText("加载失败：" + description);
                textLoading.setVisibility(View.VISIBLE);
            }
        });

        web.setWebChromeClient(new WebChromeClient() {
            @Override
            public void onProgressChanged(WebView view, int newProgress) {
                progress.setProgress(newProgress);
            }

            // Multiple file + folder selection for uploads.
            @Override
            public boolean onShowFileChooser(WebView webView, ValueCallback<Uri[]> callback,
                                             FileChooserParams params) {
                if (filePathCallback != null) filePathCallback.onReceiveValue(null);
                filePathCallback = callback;

                Intent intent;
                Intent base = params.createIntent();
                // WebView signals a webkitdirectory input with ACTION_OPEN_DOCUMENT_TREE.
                boolean directoryMode = base != null &&
                        Intent.ACTION_OPEN_DOCUMENT_TREE.equals(base.getAction());
                if (directoryMode) {
                    choosingDirectory = true;
                    intent = base;
                    intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                            | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION);
                } else {
                    choosingDirectory = false;
                    intent = base;
                    if (intent == null) intent = new Intent(Intent.ACTION_GET_CONTENT);
                    intent.setType("*/*");
                    intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
                    intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                }
                try {
                    startActivityForResult(Intent.createChooser(intent, getString(R.string.choose_file)), REQ_FILE);
                } catch (Exception e) {
                    filePathCallback = null;
                    choosingDirectory = false;
                    Toast.makeText(MainActivity.this, "无法打开文件选择器", Toast.LENGTH_SHORT).show();
                    return false;
                }
                return true;
            }
        });

        web.setDownloadListener((DownloadListener) (url, userAgent, contentDisposition, mimetype, length) -> {
            String name = guessName(contentDisposition, url);
            startDownload(url, name);
        });
    }

    private void syncCookieAndLoad() {
        CookieManager cm = CookieManager.getInstance();
        cm.setAcceptCookie(true);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) cm.setAcceptThirdPartyCookies(web, true);
        String cookie = config.getCookie();
        if (!cookie.isEmpty()) {
            // Drop any stale value, then keep the session for 30 days in WebView.
            cm.setCookie(server, "lywp_session=; Max-Age=0; Path=/");
            cm.setCookie(server, cookie + "; Max-Age=2592000; Path=/");
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            cm.flush();
        }
        web.loadUrl(server + "/");
    }

    /**
     * Make the search field usable on older server binaries too: the SVG icon
     * used to swallow taps and the narrow layout squeezed the input to a few
     * pixels, so the soft keyboard never opened. Newer pages already contain
     * these fixes; the injected handlers are only installed on old pages.
     */
    private void applyInputFix(WebView view) {
        String js = "(function(){"
                + "var s=document.getElementById('lywp_input_fix');"
                + "if(!s){s=document.createElement('style');s.id='lywp_input_fix';"
                + "s.textContent='.search-box svg{pointer-events:none!important}"
                + "@media(max-width:540px){.header{flex-wrap:wrap;height:auto;min-height:56px;padding:6px 10px}"
                + ".header-logo{flex:1 1 auto;max-width:none}.header-actions{flex:0 0 auto}"
                + ".search-box{order:3;flex:1 0 100%;max-width:none;margin:0}}';"
                + "document.head.appendChild(s);}"
                + "var i=document.getElementById('searchInput');"
                + "if(i&&i.hasAttribute('onkeyup')){"
                + "i.removeAttribute('onkeyup');"
                + "i.setAttribute('autocomplete','off');"
                + "i.addEventListener('click',function(){this.focus();});"
                + "i.addEventListener('input',function(e){"
                + "if(e.isComposing||e.keyCode===229)return;"
                + "if(!this.value.trim()&&typeof isSearchMode!=='undefined'&&isSearchMode){"
                + "isSearchMode=false;searchItems=[];selectedItems.clear();"
                + "if(typeof updateBatchButtons==='function')updateBatchButtons();"
                + "if(typeof loadFiles==='function')loadFiles(currentPath);}});"
                + "i.addEventListener('keydown',function(e){"
                + "if(e.isComposing||e.keyCode===229)return;"
                + "if(e.key==='Enter'||e.keyCode===13){var v=this.value.trim();"
                + "if(v){e.preventDefault();if(typeof doSearch==='function')doSearch(v);}}});}"
                + "})()";
        view.evaluateJavascript(js, null);
    }

    private void applyLocalThemeToWeb(String theme) {
        // The server injects data-theme into <html> at page generation; if the
        // client just changed the skin, update the DOM immediately as well.
        String js = "document.documentElement.setAttribute('data-theme'," + jsQuote(theme) + ");" +
                "window.LYWP_THEME=" + jsQuote(theme) + ";" +
                "if(typeof applySkin==='function'){applySkin(" + jsQuote(theme) + ",false);}";
        web.evaluateJavascript(js, null);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode != REQ_FILE || filePathCallback == null) return;

        Uri[] results = null;
        if (resultCode == RESULT_OK && data != null) {
            if (choosingDirectory && data.getData() != null) {
                // Recursively expand the picked document tree into file Uris.
                DocumentFile root = DocumentFile.fromTreeUri(this, data.getData());
                List<Uri> files = new ArrayList<>();
                if (root != null) collectFiles(root, files);
                results = files.toArray(new Uri[0]);
            } else {
                List<Uri> uris = new ArrayList<>();
                if (data.getClipData() != null) {
                    for (int i = 0; i < data.getClipData().getItemCount(); i++) {
                        uris.add(data.getClipData().getItemAt(i).getUri());
                    }
                } else if (data.getData() != null) {
                    uris.add(data.getData());
                }
                results = uris.toArray(new Uri[0]);
            }
        }
        filePathCallback.onReceiveValue(results);
        filePathCallback = null;
        choosingDirectory = false;
    }

    private void collectFiles(DocumentFile dir, List<Uri> out) {
        DocumentFile[] children = dir.listFiles();
        for (DocumentFile child : children) {
            if (child.isDirectory()) collectFiles(child, out);
            else if (child.isFile()) out.add(child.getUri());
        }
    }

    private void startDownload(String url, String name) {
        try {
            DownloadManager.Request req = new DownloadManager.Request(Uri.parse(url));
            String cookie = config.getCookie();
            if (!cookie.isEmpty()) req.addRequestHeader("Cookie", cookie);
            req.setTitle(name);
            req.setDescription("老杨网盘下载");
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
            req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
            req.allowScanningByMediaScanner();
            DownloadManager dm = (DownloadManager) getSystemService(Context.DOWNLOAD_SERVICE);
            dm.enqueue(req);
            Toast.makeText(this, getString(R.string.download_started), Toast.LENGTH_SHORT).show();
        } catch (Exception e) {
            Toast.makeText(this, "下载失败：" + e.getMessage(), Toast.LENGTH_LONG).show();
        }
    }

    private String guessName(String disposition, String url) {
        String name = "";
        if (disposition != null) {
            int i = disposition.indexOf("filename=");
            if (i >= 0) {
                name = disposition.substring(i + 9).trim();
                name = name.replace("\"", "");
            }
        }
        if (name.isEmpty()) {
            int i = url.lastIndexOf('/');
            if (i >= 0) name = url.substring(i + 1);
            int q = name.indexOf('?');
            if (q >= 0) name = name.substring(0, q);
        }
        return name.isEmpty() ? "download" : name;
    }

    private void showSkinPicker() {
        View view = getLayoutInflater().inflate(R.layout.dialog_skin, null);
        GridView grid = view.findViewById(R.id.skinGrid);
        SkinAdapter adapter = new SkinAdapter(this, config.getTheme());
        grid.setAdapter(adapter);
        AlertDialog dialog = new AlertDialog.Builder(this)
                .setTitle(R.string.skin_title)
                .setView(view)
                .setNegativeButton(android.R.string.cancel, null)
                .create();
        grid.setOnItemClickListener((parent, v, position, id) -> {
            Skins.Skin skin = Skins.ALL[position];
            adapter.setSelected(skin.id);
            adapter.notifyDataSetChanged();
            applySkin(skin.id);
            dialog.dismiss();
        });
        dialog.show();
    }

    private void applySkin(String theme) {
        config.setTheme(theme);
        applyLocalThemeToWeb(theme);
        executor.execute(() -> {
            try {
                ApiClient.Result result = ApiClient.setTheme(server, config.getCookie(), theme);
                main.post(() -> Toast.makeText(this,
                        result.ok() ? R.string.skin_saved : R.string.skin_save_failed,
                        Toast.LENGTH_SHORT).show());
            } catch (Exception e) {
                main.post(() -> Toast.makeText(this, R.string.skin_save_failed, Toast.LENGTH_SHORT).show());
            }
        });
    }

    private void showSettings() {
        String[] items = {
                getString(R.string.btn_refresh),
                getString(R.string.btn_logout),
                getString(R.string.about_title),
                "切换服务器"
        };
        new AlertDialog.Builder(this)
                .setTitle(R.string.btn_settings)
                .setItems(items, (d, which) -> {
                    if (which == 0) web.reload();
                    else if (which == 1) logout();
                    else if (which == 2) showAbout();
                    else switchServer();
                })
                .show();
    }

    private void logout() {
        executor.execute(() -> {
            try {
                ApiClient.get(server + "/auth.php?action=logout", config.getCookie(), 8000);
            } catch (Exception ignored) {}
            main.post(() -> {
                config.clearSession();
                CookieManager.getInstance().setCookie(server, "lywp_session=; Max-Age=0; Path=/");
                Intent intent = new Intent(this, ConnectActivity.class);
                intent.setFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_NEW_TASK);
                startActivity(intent);
                finish();
            });
        });
    }

    /** Called when the web page's own logout button is used. */
    private void clearSessionAndGoLogin() {
        config.clearSession();
        CookieManager cm = CookieManager.getInstance();
        cm.setCookie(server, "lywp_session=; Max-Age=0; Path=/");
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) cm.flush();
        Intent intent = new Intent(this, ConnectActivity.class);
        intent.setFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_NEW_TASK);
        startActivity(intent);
        finish();
    }

    private void switchServer() {
        config.clearSession();
        Intent intent = new Intent(this, ConnectActivity.class);
        intent.setFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_NEW_TASK);
        startActivity(intent);
        finish();
    }

    private void showAbout() {
        new AlertDialog.Builder(this)
                .setTitle(R.string.about_title)
                .setMessage(getString(R.string.about_text) + "\n\n当前设备：" + Device.label(this)
                        + "\n服务器：" + server)
                .setPositiveButton(android.R.string.ok, null)
                .show();
    }

    private void onBack() {
        if (web.canGoBack()) {
            web.goBack();
        } else {
            long now = System.currentTimeMillis();
            if (now - lastBack < 2000) finish();
            else {
                Toast.makeText(this, "再按一次退出", Toast.LENGTH_SHORT).show();
                lastBack = now;
            }
        }
    }

    @Override
    public void onBackPressed() {
        onBack();
    }

    private void requestStoragePermission() {
        if (Build.VERSION.SDK_INT <= Build.VERSION_CODES.P) {
            if (ContextCompat.checkSelfPermission(this,
                    android.Manifest.permission.WRITE_EXTERNAL_STORAGE) != PackageManager.PERMISSION_GRANTED) {
                ActivityCompat.requestPermissions(this,
                        new String[]{android.Manifest.permission.WRITE_EXTERNAL_STORAGE}, REQ_PERM);
            }
        }
    }

    private static String jsQuote(String value) {
        if (value == null) value = "";
        StringBuilder sb = new StringBuilder("\"");
        for (char c : value.toCharArray()) {
            if (c == '"' || c == '\\') sb.append('\\').append(c);
            else if (c == '\n') sb.append("\\n");
            else sb.append(c);
        }
        return sb.append('"').toString();
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        executor.shutdownNow();
        if (filePathCallback != null) filePathCallback.onReceiveValue(null);
    }
}
