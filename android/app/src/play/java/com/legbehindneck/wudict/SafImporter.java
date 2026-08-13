// Copyright (C) 2026 glowinthedark
//
// SPDX-License-Identifier: GPL-3.0-or-later

// Copies a user-picked folder out of the Storage Access Framework and into the
// app's own dictionary folder, where the server can open it as ordinary paths
// (D62).
//
// It mirrors the WHOLE picked subtree rather than filtering by extension, and
// that is not laziness. Every format here is a bundle whose parts are found by
// path arithmetic in the Go code — .mdx beside its .mdd, StarDict's
// .ifo/.idx/.dict.dz/.syn, a DSL beside its _abrv and its res/ folder of
// media (internal/format/stardict/stardict.go:153,402 and
// internal/format/dsl/dsl.go:121). Relative layout has to survive the copy
// exactly, and the user picking the folder IS the statement of what to bring.
package com.legbehindneck.wudict;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.ContentResolver;
import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.os.StatFs;
import android.provider.DocumentsContract;
import android.provider.OpenableColumns;
import android.util.Log;

import java.io.File;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.List;

final class SafImporter {

    private static final String TAG = "wudict";

    private static final int BUF = 1 << 20;            // 1 MiB: these are multi-GB files
    private static final long SLACK = 64L << 20;       // leave the volume some room
    private static final int MAX_DEPTH = 32;           // a provider is not required to be sane
    private static final int MAX_ENTRIES = 100_000;

    // One import at a time. Two concurrent copies into the same destination
    // would race on the same .part files.
    private static volatile boolean running;
    private static volatile boolean cancelled;

    private SafImporter() {
    }

    /** One entry to copy: where to read it from, and where it goes relative to the destination root. */
    private static final class Item {
        final Uri src;
        final String rel;   // sanitised, '/'-separated, never absolute or escaping
        final long size;    // -1 when the provider does not say

        Item(Uri src, String rel, long size) {
            this.src = src;
            this.rel = rel;
            this.size = size;
        }
    }

    // ── entry points ─────────────────────────────────────────────────────

    static void importTree(Activity a, Uri tree) {
        if (!claim(a)) return;
        run(a, () -> {
            String rootId = DocumentsContract.getTreeDocumentId(tree);
            String folder = safeName(displayName(a.getContentResolver(),
                    DocumentsContract.buildDocumentUriUsingTree(tree, rootId)));
            List<Item> items = new ArrayList<>();
            enumerate(a.getContentResolver(), tree, rootId, "", items);
            return new Batch(folder == null ? "Imported" : folder, items, true);
        });
    }

    /** Share-target route: a flat set of documents, no tree grant, no folder name. */
    static void importDocuments(Activity a, List<Uri> docs) {
        if (!claim(a)) return;
        run(a, () -> {
            ContentResolver cr = a.getContentResolver();
            List<Item> items = new ArrayList<>();
            for (Uri u : docs) {
                String name = safeName(displayName(cr, u));
                if (name == null) continue;
                items.add(new Item(u, name, sizeOf(cr, u)));
            }
            return new Batch("Imported", items, false);
        });
    }

    // ── the work ─────────────────────────────────────────────────────────

    private static final class Batch {
        final String folder;
        final List<Item> items;
        final boolean deletable; // a tree grant may allow removing the originals

        Batch(String folder, List<Item> items, boolean deletable) {
            this.folder = folder;
            this.items = items;
            this.deletable = deletable;
        }
    }

    private interface Plan {
        Batch build() throws Exception;
    }

    private static boolean claim(Activity a) {
        if (running) {
            toastDialog(a, a.getString(R.string.import_busy));
            return false;
        }
        running = true;
        cancelled = false;
        return true;
    }

    private static void run(Activity a, Plan plan) {
        AlertDialog dialog = new AlertDialog.Builder(a)
                .setTitle(R.string.import_title)
                .setMessage(a.getString(R.string.import_scanning))
                .setCancelable(false)
                .setNegativeButton(R.string.import_cancel, (d, w) -> cancelled = true)
                .create();
        dialog.show();

        Thread t = new Thread(() -> {
            String summary;
            try {
                summary = copy(a, plan.build(), dialog);
            } catch (Exception e) {
                Log.w(TAG, "import failed", e);
                summary = a.getString(R.string.import_failed, String.valueOf(e.getMessage()));
            } finally {
                running = false;
            }
            final String msg = summary;
            a.runOnUiThread(() -> {
                if (a.isFinishing() || a.isDestroyed()) return;
                dialog.dismiss();
                toastDialog(a, msg);
            });
        }, "wudict-import");
        t.setDaemon(true);
        t.start();
    }

    private static String copy(Activity a, Batch batch, AlertDialog dialog) {
        if (batch.items.isEmpty()) return a.getString(R.string.import_nothing);

        File root = new File(AppDirs.appDicts(a), batch.folder);
        long need = 0;
        for (Item it : batch.items) {
            if (it.size > 0) need += it.size;
        }
        long free = new StatFs(AppDirs.appDicts(a).getAbsolutePath()).getAvailableBytes();
        if (need + SLACK > free) {
            return a.getString(R.string.import_no_space, mb(need), mb(free));
        }

        ContentResolver cr = a.getContentResolver();
        int done = 0, skipped = 0;
        List<Uri> copied = new ArrayList<>();
        List<String> failed = new ArrayList<>();

        for (Item it : batch.items) {
            if (cancelled) break;
            File dst = resolve(root, it.rel);
            if (dst == null) {
                failed.add(it.rel);
                continue;
            }
            // Already there at the same size: a re-import to pick up newly
            // added dictionaries must not re-copy gigabytes it already has.
            if (dst.isFile() && it.size >= 0 && dst.length() == it.size) {
                skipped++;
                copied.add(it.src);
                continue;
            }
            final int n = done + skipped + failed.size() + 1;
            final int total = batch.items.size();
            a.runOnUiThread(() -> {
                if (!a.isFinishing() && !a.isDestroyed()) {
                    dialog.setMessage(a.getString(R.string.import_progress, n, total, it.rel));
                }
            });
            if (copyOne(cr, it.src, dst)) {
                done++;
                copied.add(it.src);
            } else {
                failed.add(it.rel);
            }
        }

        rescan();
        a.runOnUiThread(() -> {
            if (!a.isFinishing() && !a.isDestroyed() && a instanceof MainActivity) {
                ((MainActivity) a).reloadPage(); // the panel is showing a stale library
            }
        });

        if (cancelled) {
            return a.getString(R.string.import_cancelled, done);
        }
        if (batch.deletable && !copied.isEmpty() && failed.isEmpty()) {
            offerDelete(a, cr, copied, done + skipped);
            return null; // offerDelete owns the closing message
        }
        if (!failed.isEmpty()) {
            return a.getString(R.string.import_partial, done + skipped, failed.size());
        }
        return a.getString(R.string.import_done, done + skipped);
    }

    // Writes to <name>.part and renames on success, so an interrupted import
    // never leaves a truncated file that discovery would open as a dictionary
    // and a re-run resumes cleanly.
    private static boolean copyOne(ContentResolver cr, Uri src, File dst) {
        File parent = dst.getParentFile();
        if (parent != null && !parent.isDirectory() && !parent.mkdirs()) {
            Log.w(TAG, "cannot create " + parent);
            return false;
        }
        File part = new File(dst.getPath() + ".part");
        try (InputStream in = cr.openInputStream(src);
             OutputStream out = new FileOutputStream(part)) {
            if (in == null) return false;
            byte[] buf = new byte[BUF];
            for (int n; (n = in.read(buf)) > 0; ) {
                if (cancelled) {
                    part.delete();
                    return false;
                }
                out.write(buf, 0, n);
            }
        } catch (IOException | SecurityException | IllegalArgumentException e) {
            Log.w(TAG, "copy failed: " + src, e);
            part.delete();
            return false;
        }
        if (dst.exists() && !dst.delete()) {
            part.delete();
            return false;
        }
        if (!part.renameTo(dst)) {
            part.delete();
            return false;
        }
        return true;
    }

    // The originals are the user's, so removing them is offered, never done.
    // Taking it up turns the import into a move, which is what keeps a
    // multi-GB collection from existing twice.
    private static void offerDelete(Activity a, ContentResolver cr, List<Uri> docs, int count) {
        a.runOnUiThread(() -> {
            if (a.isFinishing() || a.isDestroyed()) return;
            new AlertDialog.Builder(a)
                    .setTitle(R.string.import_title)
                    .setMessage(a.getString(R.string.import_done_offer_delete, count))
                    .setPositiveButton(R.string.import_delete_originals, (d, w) -> {
                        Thread t = new Thread(() -> {
                            int gone = 0;
                            for (Uri u : docs) {
                                try {
                                    if (DocumentsContract.deleteDocument(cr, u)) gone++;
                                } catch (Exception e) {
                                    Log.w(TAG, "could not delete " + u, e);
                                }
                            }
                            final int n = gone;
                            a.runOnUiThread(() -> {
                                if (!a.isFinishing() && !a.isDestroyed()) {
                                    toastDialog(a, a.getString(R.string.import_deleted, n));
                                }
                            });
                        }, "wudict-import-delete");
                        t.setDaemon(true);
                        t.start();
                    })
                    .setNegativeButton(R.string.import_keep_originals, null)
                    .show();
        });
    }

    // ── SAF plumbing ─────────────────────────────────────────────────────

    // Breadth-first rather than recursive: a provider is free to report a
    // directory tree that is deep, cyclic, or both, and a stack overflow in a
    // background thread is a crash, not an error message.
    private static void enumerate(ContentResolver cr, Uri tree, String rootId,
                                 String rootRel, List<Item> out) {
        Deque<String[]> queue = new ArrayDeque<>(); // {docId, relPath, depth}
        queue.add(new String[]{rootId, rootRel, "0"});
        while (!queue.isEmpty() && !cancelled && out.size() < MAX_ENTRIES) {
            String[] cur = queue.poll();
            int depth = Integer.parseInt(cur[2]);
            if (depth > MAX_DEPTH) continue;
            Uri children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, cur[0]);
            try (Cursor c = cr.query(children, new String[]{
                    DocumentsContract.Document.COLUMN_DOCUMENT_ID,
                    DocumentsContract.Document.COLUMN_DISPLAY_NAME,
                    DocumentsContract.Document.COLUMN_MIME_TYPE,
                    DocumentsContract.Document.COLUMN_SIZE,
            }, null, null, null)) {
                if (c == null) continue;
                while (c.moveToNext() && !cancelled && out.size() < MAX_ENTRIES) {
                    String id = c.getString(0);
                    String name = safeName(c.getString(1));
                    String mime = c.getString(2);
                    if (id == null || name == null) continue;
                    String rel = cur[1].isEmpty() ? name : cur[1] + "/" + name;
                    if (DocumentsContract.Document.MIME_TYPE_DIR.equals(mime)) {
                        queue.add(new String[]{id, rel, String.valueOf(depth + 1)});
                    } else {
                        long size = c.isNull(3) ? -1 : c.getLong(3);
                        out.add(new Item(
                                DocumentsContract.buildDocumentUriUsingTree(tree, id), rel, size));
                    }
                }
            } catch (Exception e) {
                Log.w(TAG, "cannot list " + cur[0], e); // one unreadable folder is not fatal
            }
        }
    }

    private static String displayName(ContentResolver cr, Uri uri) {
        try (Cursor c = cr.query(uri, new String[]{OpenableColumns.DISPLAY_NAME},
                null, null, null)) {
            if (c != null && c.moveToFirst() && !c.isNull(0)) return c.getString(0);
        } catch (Exception e) {
            Log.w(TAG, "no display name for " + uri, e);
        }
        String last = uri.getLastPathSegment();
        if (last == null) return null;
        int cut = last.lastIndexOf('/');
        return cut >= 0 ? last.substring(cut + 1) : last;
    }

    private static long sizeOf(ContentResolver cr, Uri uri) {
        try (Cursor c = cr.query(uri, new String[]{OpenableColumns.SIZE}, null, null, null)) {
            if (c != null && c.moveToFirst() && !c.isNull(0)) return c.getLong(0);
        } catch (Exception e) {
            Log.w(TAG, "no size for " + uri, e);
        }
        return -1;
    }

    // ── hostile-name defence ─────────────────────────────────────────────

    // A DISPLAY_NAME comes from a provider, which is another app. Anything
    // that could climb out of the destination is rejected outright rather than
    // rewritten: a dictionary whose parts were silently renamed would fail
    // later, in the Go code, as a missing sibling.
    private static String safeName(String raw) {
        if (raw == null) return null;
        String n = raw.trim();
        if (n.isEmpty() || n.equals(".") || n.equals("..")) return null;
        if (n.indexOf('/') >= 0 || n.indexOf('\\') >= 0 || n.indexOf('\0') >= 0) return null;
        return n;
    }

    // Second line of defence: whatever the segments were, the resulting path
    // must still be inside the destination root.
    private static File resolve(File root, String rel) {
        File dst = new File(root, rel);
        try {
            String base = root.getCanonicalPath() + File.separator;
            if (!dst.getCanonicalPath().startsWith(base)) {
                Log.w(TAG, "refusing to write outside " + root + ": " + rel);
                return null;
            }
        } catch (IOException e) {
            return null;
        }
        return dst;
    }

    // ── after ────────────────────────────────────────────────────────────

    // The server already exposes a rescan (internal/server/server.go:116);
    // nothing new is added to its API for the port's sake.
    private static void rescan() {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL("http://" + ServerProcess.HOST + ":"
                    + ServerProcess.PORT + "/api/rescan").openConnection();
            c.setConnectTimeout(2000);
            c.setReadTimeout(120_000); // a rescan of a fresh import indexes headwords
            c.getResponseCode();
        } catch (Exception e) {
            Log.w(TAG, "rescan after import failed", e); // the next launch picks it up
        } finally {
            if (c != null) c.disconnect();
        }
    }

    private static void toastDialog(Context c, String message) {
        if (message == null) return;
        new AlertDialog.Builder(c)
                .setMessage(message)
                .setPositiveButton(android.R.string.ok, null)
                .show();
    }

    private static String mb(long bytes) {
        return (bytes >> 20) + " MB";
    }
}
