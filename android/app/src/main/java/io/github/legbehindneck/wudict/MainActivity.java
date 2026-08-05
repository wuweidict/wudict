// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// WuWeiDict's Android shell (D52): a WebView over the wudict server binary
// that ships inside the APK as libwudict.so. The Go program is unchanged —
// ServerProcess execs it as a child and it answers on 127.0.0.1:6888.
package io.github.legbehindneck.wudict;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.provider.Settings;
import android.view.Gravity;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.widget.TextView;

public class MainActivity extends Activity {

    private static final String PAGE_URL =
            "http://" + ServerProcess.HOST + ":" + ServerProcess.PORT + "/";

    // One server per app process, shared across activity recreation: the
    // child holds port 6888, so a second spawn would lose the port to it.
    private static ServerProcess server;

    private FrameLayout root;
    private TextView status;
    private WebView web;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        root = new FrameLayout(this);
        status = new TextView(this);
        status.setText(R.string.starting);
        root.addView(status, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.WRAP_CONTENT,
                FrameLayout.LayoutParams.WRAP_CONTENT,
                Gravity.CENTER));

        web = new WebView(this);
        web.getSettings().setJavaScriptEnabled(true);            // the UI is one SPA
        web.getSettings().setDomStorageEnabled(true);            // UI prefs live in localStorage
        web.getSettings().setMediaPlaybackRequiresUserGesture(false); // dictionary audio
        web.setWebViewClient(new WebViewClient());               // keep navigation inside
        WebView.setWebContentsDebuggingEnabled(BuildConfig.DEBUG);

        setContentView(root);
        ensureStorageAccess();

        if (server == null) {
            server = new ServerProcess(getApplication());
            server.start(new ServerProcess.Listener() {
                @Override public void onReady() { showPage(); }
                @Override public void onFailed(String message) { showFailure(message); }
            });
        } else {
            showPage();
        }
    }

    private void showPage() {
        runOnUiThread(() -> {
            if (web.getParent() == null) {
                root.removeAllViews();
                root.addView(web, new FrameLayout.LayoutParams(
                        FrameLayout.LayoutParams.MATCH_PARENT,
                        FrameLayout.LayoutParams.MATCH_PARENT));
            }
            web.loadUrl(PAGE_URL);
        });
    }

    private void showFailure(String message) {
        runOnUiThread(() ->
                status.setText(getString(R.string.server_failed, message)));
    }

    // Dictionaries are read from the shared "Dictionaries" folder (D52).
    // That needs all-files access on API 30+, legacy WRITE below; either way
    // the server starts — it reports the folder as empty until files arrive.
    private void ensureStorageAccess() {
        if (Build.VERSION.SDK_INT >= 30) {
            if (!Environment.isExternalStorageManager()) {
                new AlertDialog.Builder(this)
                        .setTitle(R.string.storage_title)
                        .setMessage(R.string.storage_message)
                        .setPositiveButton(R.string.storage_grant, (dialog, which) ->
                                startActivity(new Intent(
                                        Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                                        Uri.parse("package:" + getPackageName()))))
                        .setNegativeButton(R.string.storage_later, null)
                        .show();
            }
        } else if (checkSelfPermission(Manifest.permission.WRITE_EXTERNAL_STORAGE)
                != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(
                    new String[]{Manifest.permission.WRITE_EXTERNAL_STORAGE}, 1);
        }
    }

    @Override
    public void onBackPressed() {
        if (web.getParent() != null && web.canGoBack()) {
            web.goBack(); // the SPA keeps its own history per search
        } else {
            super.onBackPressed();
        }
    }

    @Override
    protected void onDestroy() {
        // The server is bound to the app's lifetime (D52): finishing kills
        // it; swiping the task away kills the process group, which includes
        // the child. Recreation keeps it.
        if (isFinishing() && server != null) {
            server.stop();
            server = null;
        }
        web.destroy();
        super.onDestroy();
    }
}
