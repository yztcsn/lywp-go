package com.lywp.android;

import android.app.Activity;
import android.content.Intent;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.View;
import android.widget.ArrayAdapter;
import android.widget.CheckBox;
import android.widget.AutoCompleteTextView;
import android.widget.Button;
import android.widget.EditText;
import android.widget.TextView;
import android.widget.Toast;

import java.io.IOException;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public class ConnectActivity extends Activity {
    /** Set only after a fresh login so MainActivity can open without remember. */
    static boolean freshLogin = false;

    private AutoCompleteTextView editServer;
    private EditText editPassword;
    private CheckBox checkRemember;
    private Button btnConnect;
    private TextView textError;
    private Config config;
    private final ExecutorService executor = Executors.newSingleThreadExecutor();
    private final Handler main = new Handler(Looper.getMainLooper());

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_connect);

        config = new Config(this);
        editServer = findViewById(R.id.editServer);
        editPassword = findViewById(R.id.editPassword);
        checkRemember = findViewById(R.id.checkRemember);
        btnConnect = findViewById(R.id.btnConnect);
        textError = findViewById(R.id.textError);

        String saved = config.getServer();
        editServer.setText(saved);
        editServer.setAdapter(new ArrayAdapter<>(this,
                android.R.layout.simple_dropdown_item_1line, config.getHistory()));

        ((TextView) findViewById(R.id.textDevice)).setText(
                getString(R.string.device_phone) + " / " + getString(R.string.device_tablet)
                        + " · 当前设备：" + Device.label(this));

        btnConnect.setOnClickListener(v -> attemptLogin());
        autoLogin();
    }

    /** Resume a valid remembered login without asking for the password again. */
    private void autoLogin() {
        if (!config.canResumeSession()) return;
        String server = config.getServer();
        String password = config.getPassword();
        btnConnect.setEnabled(false);
        btnConnect.setText(R.string.auto_login);
        executor.execute(() -> {
            try {
                ApiClient.Result result = ApiClient.login(server, password);
                if (!result.ok() || result.cookie == null || result.cookie.isEmpty()) {
                    throw new IOException("auto login rejected");
                }
                config.saveLogin(server, password, result.cookie, true);
                main.post(this::goMainActivity);
            } catch (Exception e) {
                config.clearSession();
                main.post(() -> {
                    restoreButton();
                    showError(getString(R.string.login_expired));
                });
            }
        });
    }

    private void goMainActivity() {
        Intent intent = new Intent(ConnectActivity.this, MainActivity.class);
        intent.setFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP | Intent.FLAG_ACTIVITY_NEW_TASK);
        startActivity(intent);
        finish();
    }

    private void attemptLogin() {
        String server = Config.normalize(editServer.getText().toString());
        String password = editPassword.getText().toString();

        if (server.isEmpty()) {
            showError(getString(R.string.err_server_required));
            return;
        }
        if (!server.startsWith("http://") && !server.startsWith("https://")) {
            showError(getString(R.string.err_server_prefix));
            return;
        }
        if (password.isEmpty()) {
            showError(getString(R.string.err_password_required));
            return;
        }

        showError(null);
        btnConnect.setEnabled(false);
        btnConnect.setText(R.string.connecting);

        executor.execute(() -> {
            try {
                ApiClient.Result result = ApiClient.login(server, password);
                if (!result.ok()) {
                    main.post(() -> {
                        restoreButton();
                        showError(getString(R.string.err_login_failed) + " (" + result.code + ")");
                    });
                    return;
                }
                String cookie = result.cookie;
                if (cookie == null || cookie.isEmpty()) {
                    main.post(() -> {
                        restoreButton();
                        showError(getString(R.string.err_login_failed));
                    });
                    return;
                }
                config.saveLogin(server, password, cookie, checkRemember.isChecked());
                freshLogin = true;
                main.post(this::goMainActivity);
            } catch (Exception e) {
                main.post(() -> {
                    restoreButton();
                    showError(getString(R.string.err_network) + "：" + e.getMessage());
                });
            }
        });
    }

    private void restoreButton() {
        btnConnect.setEnabled(true);
        btnConnect.setText(R.string.btn_connect);
    }

    private void showError(String message) {
        if (message == null || message.isEmpty()) {
            textError.setVisibility(View.GONE);
        } else {
            textError.setText(message);
            textError.setVisibility(View.VISIBLE);
        }
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        executor.shutdownNow();
    }
}
